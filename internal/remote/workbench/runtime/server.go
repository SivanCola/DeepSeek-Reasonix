// Package runtime implements the per-workspace remote-runtime that attach
// proxies dial over a private Unix socket. Controllers and sessions live here;
// SSH attach only owns the Desktop/Broker channel generation.
package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"reasonix/internal/remote/protocol"
	"reasonix/internal/remote/workbench/files"
	"reasonix/internal/rpcwire"
)

const (
	// GracePeriod is how long a runtime stays alive after unexpected detach.
	GracePeriod = 5 * time.Minute
	// DefaultSocketDir is under ~/.reasonix/workbench-runtime/
	defaultSocketDirName = "workbench-runtime"
)

// Options configures a workspace runtime.
type Options struct {
	Workspace string
	Version   string
	// BuildController is optional; when nil, session create still works for
	// protocol smoke tests but Submit returns unavailable.
	BuildController func(ctx context.Context, model string) (SessionController, error)
	// Broker is optional Host-side broker client (calls Desktop over reverse RPC).
	// When nil, model turns fail with CAPABILITY_UNAVAILABLE.
	Logger io.Writer
}

// SessionController is the minimal control surface used by the runtime.
type SessionController interface {
	ModelRef() string
	Label() string
	History() []map[string]any
	Running() bool
	Submit(input string) error
	Cancel()
	Close()
	SessionPath() string
	SetSessionPath(path string)
}

// Server is one workspace runtime process endpoint.
type Server struct {
	opts Options

	mu       sync.Mutex
	sessions map[string]*session
	gen      uint64 // current attach generation
	// lastDetach is when the last attach generation closed; zero if attached.
	lastDetach time.Time
	attached   bool
	closing    bool

	ln     net.Listener
	socket string
}

type session struct {
	id     string
	ctrl   SessionController
	model  string
	rev    int64
	digest string
}

// New creates a runtime server (does not listen yet).
func New(opts Options) *Server {
	return &Server{
		opts:     opts,
		sessions: map[string]*session{},
	}
}

// SocketPath returns the private Unix socket path for this workspace.
func SocketPath(home, workspace string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(workspace)))
	dir := filepath.Join(home, ".reasonix", defaultSocketDirName, hex.EncodeToString(sum[:8]))
	return filepath.Join(dir, "runtime.sock")
}

// ListenAndServe binds the workspace socket and serves attach connections.
func (s *Server) ListenAndServe(ctx context.Context, socket string) error {
	if strings.TrimSpace(s.opts.Workspace) == "" {
		return errors.New("workspace required")
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.socket = socket
	s.mu.Unlock()

	go s.graceLoop(ctx)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return err
			}
		}
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) graceLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			shouldExit := !s.attached && !s.lastDetach.IsZero() && time.Since(s.lastDetach) >= GracePeriod && !s.hasBusyLocked()
			s.mu.Unlock()
			if shouldExit {
				s.snapshotAndClose()
				if s.ln != nil {
					_ = s.ln.Close()
				}
				return
			}
		}
	}
}

func (s *Server) hasBusyLocked() bool {
	for _, sess := range s.sessions {
		if sess.ctrl != nil && sess.ctrl.Running() {
			return true
		}
	}
	return false
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	s.mu.Lock()
	s.gen++
	gen := s.gen
	s.attached = true
	s.lastDetach = time.Time{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.gen == gen {
			s.attached = false
			s.lastDetach = time.Now()
		}
		s.mu.Unlock()
	}()

	wire := rpcwire.NewConn(conn, conn, rpcwire.Options{
		Name: "workbench-runtime", StrictJSONRPC: true,
		MaxInboundBytes: protocol.FrameBytes, MaxOutboundBytes: protocol.FrameBytes,
	})
	// Minimal RuntimeAPI handlers for workbench hydrate/control.
	wire.Handle(string(protocol.MethodRemoteInitialize), s.handleInitialize)
	wire.Handle(string(protocol.MethodRemotePing), s.handlePing)
	wire.Handle(string(protocol.MethodRemoteDetach), s.handleDetach)
	wire.Handle(string(protocol.MethodSessionList), s.handleSessionList)
	wire.Handle(string(protocol.MethodSessionCreate), s.handleSessionCreate)
	wire.Handle(string(protocol.MethodSessionHistory), s.handleSessionHistory)
	wire.Handle(string(protocol.MethodSessionSubmit), s.handleSessionSubmit)
	wire.Handle(string(protocol.MethodTurnCancel), s.handleTurnCancel)
	wire.Handle(string(protocol.MethodFileList), s.handleFileList)
	wire.Handle(string(protocol.MethodFilePreview), s.handleFilePreview)
	wire.Handle(string(protocol.MethodHostCapabilities), s.handleCapabilities)

	_ = wire.Serve(ctx)
}

func (s *Server) handleInitialize(ctx context.Context, params json.RawMessage) (any, error) {
	// Accept initialize; Schema Hash enforcement happens in attach bootstrap.
	return map[string]any{
		"hostEpoch": "he_1",
		"lease":     map[string]any{"leaseId": "lease_local", "ttlMs": 30000, "pingIntervalMs": 10000},
		"host": map[string]any{
			"os": runtimeGOOS(), "arch": runtimeGOARCH(),
			"shellKind": "sh", "sandboxBackend": "none",
		},
		"capabilities": map[string]any{
			"features": map[string]any{
				"coreSession": true, "primaryFileQueries": true,
				"userShell": false, "jobCancel": true,
			},
		},
		"buildId": map[string]any{
			"productVersion":  s.opts.Version,
			"protocolVersion": protocol.ProtocolVersion,
			"schemaHash":      protocol.SchemaHash(),
		},
	}, nil
}

func (s *Server) handlePing(ctx context.Context, params json.RawMessage) (any, error) {
	return map[string]any{"hostEpoch": "he_1", "leaseTtlMs": 30000}, nil
}

func (s *Server) handleDetach(ctx context.Context, params json.RawMessage) (any, error) {
	return map[string]any{"detached": true}, nil
}

func (s *Server) handleCapabilities(ctx context.Context, params json.RawMessage) (any, error) {
	return map[string]any{
		"hostEpoch": "he_1",
		"capabilities": map[string]any{
			"features": map[string]any{
				"coreSession": true, "primaryFileQueries": true,
				"userShell": false, "jobCancel": true,
			},
		},
	}, nil
}

func (s *Server) handleSessionList(ctx context.Context, params json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, len(s.sessions))
	for _, sess := range s.sessions {
		label := sess.model
		if sess.ctrl != nil {
			label = sess.ctrl.Label()
		}
		out = append(out, map[string]any{
			"id": sess.id, "label": label, "modelRef": sess.model,
			"running": sess.ctrl != nil && sess.ctrl.Running(),
		})
	}
	return map[string]any{"sessions": out}, nil
}

func (s *Server) handleSessionCreate(ctx context.Context, params json.RawMessage) (any, error) {
	var body struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(params, &body)
	id := "rs_" + randomHex(8)
	var ctrl SessionController
	var err error
	if s.opts.BuildController != nil {
		ctrl, err = s.opts.BuildController(ctx, body.Model)
		if err != nil {
			return nil, &rpcwire.RPCError{Code: rpcwire.ErrInternal, Message: err.Error()}
		}
	}
	s.mu.Lock()
	s.sessions[id] = &session{id: id, ctrl: ctrl, model: body.Model}
	s.mu.Unlock()
	return map[string]any{
		"session": map[string]any{"id": id, "modelRef": body.Model, "label": body.Model},
	}, nil
}

func (s *Server) handleSessionHistory(ctx context.Context, params json.RawMessage) (any, error) {
	// Params vary by full protocol; accept target.sessionId loosely.
	var body struct {
		Target struct {
			SessionID string `json:"sessionId"`
		} `json:"target"`
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(params, &body)
	sid := body.Target.SessionID
	if sid == "" {
		sid = body.SessionID
	}
	s.mu.Lock()
	sess := s.sessions[sid]
	s.mu.Unlock()
	if sess == nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInvalidParams, Message: "session not found"}
	}
	msgs := []map[string]any{}
	if sess.ctrl != nil {
		msgs = sess.ctrl.History()
	}
	return map[string]any{
		"messages": msgs, "startTurn": 0, "endTurn": 0, "totalTurns": 0,
	}, nil
}

func (s *Server) handleSessionSubmit(ctx context.Context, params json.RawMessage) (any, error) {
	var body struct {
		Target struct {
			SessionID string `json:"sessionId"`
		} `json:"target"`
		Input string `json:"input"`
		Text  string `json:"text"`
	}
	_ = json.Unmarshal(params, &body)
	sid := body.Target.SessionID
	input := body.Input
	if input == "" {
		input = body.Text
	}
	s.mu.Lock()
	sess := s.sessions[sid]
	s.mu.Unlock()
	if sess == nil || sess.ctrl == nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInvalidParams, Message: "session not found"}
	}
	if err := sess.ctrl.Submit(input); err != nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInternal, Message: err.Error()}
	}
	return map[string]any{"kind": "turn", "turnId": "t_" + randomHex(4)}, nil
}

func (s *Server) handleTurnCancel(ctx context.Context, params json.RawMessage) (any, error) {
	var body struct {
		Target struct {
			SessionID string `json:"sessionId"`
		} `json:"target"`
	}
	_ = json.Unmarshal(params, &body)
	s.mu.Lock()
	sess := s.sessions[body.Target.SessionID]
	s.mu.Unlock()
	if sess != nil && sess.ctrl != nil {
		sess.ctrl.Cancel()
	}
	return map[string]any{"status": "cancel_requested"}, nil
}

func (s *Server) handleFileList(ctx context.Context, params json.RawMessage) (any, error) {
	var body struct {
		Path string `json:"path"`
		Ref  string `json:"directoryRef"`
	}
	_ = json.Unmarshal(params, &body)
	rel := body.Ref
	if rel == "" {
		rel = body.Path
	}
	entries, _, err := files.ListDir(s.opts.Workspace, rel)
	if err != nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInvalidParams, Message: err.Error()}
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"name": e.Name(), "isDir": e.IsDir(),
		})
	}
	return map[string]any{"entries": out}, nil
}

func (s *Server) handleFilePreview(ctx context.Context, params json.RawMessage) (any, error) {
	var body struct {
		Path string `json:"path"`
		Ref  string `json:"fileRef"`
	}
	_ = json.Unmarshal(params, &body)
	rel := body.Ref
	if rel == "" {
		rel = body.Path
	}
	data, err := files.ReadFile(s.opts.Workspace, rel, 1<<20)
	if err != nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInvalidParams, Message: err.Error()}
	}
	return map[string]any{
		"kind": "text", "body": string(data), "binary": false,
	}, nil
}

func (s *Server) snapshotAndClose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		if sess.ctrl != nil {
			sess.ctrl.Close()
		}
	}
	s.sessions = map[string]*session{}
	if s.socket != "" {
		_ = os.Remove(s.socket)
	}
}

// Close forces runtime shutdown.
func (s *Server) Close() {
	s.snapshotAndClose()
	if s.ln != nil {
		_ = s.ln.Close()
	}
}

// ForceDetachForTest marks detached for grace tests.
func (s *Server) ForceDetachForTest() {
	s.mu.Lock()
	s.attached = false
	s.lastDetach = time.Now()
	s.mu.Unlock()
}

// Attached reports whether an attach generation is live.
func (s *Server) Attached() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attached
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func runtimeGOOS() string { return goruntime.GOOS }

func runtimeGOARCH() string { return goruntime.GOARCH }
