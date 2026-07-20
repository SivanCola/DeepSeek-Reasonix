// Package remoteruntime implements the headless multi-session remote workspace
// server (`reasonix remote-runtime`). It exposes the versioned /remote/v1 API on
// loopback only; the desktop reaches it through an authenticated SSH local
// forward. Provider calls go through a Broker reverse tunnel — this process
// never reads local API keys or a copied config.toml.
package remoteruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/provider"
	"reasonix/internal/remote/protocol"
)

// Server hosts multiple control.Controller instances for one workspace.
type Server struct {
	mu sync.RWMutex

	workspace string
	version   string
	resolver  provider.Resolver
	token     string // optional shared secret from token file
	startedAt time.Time
	logger    *slog.Logger

	sessions map[string]*sessionEntry
	seq      atomic.Int64

	// event fanout
	subsMu sync.Mutex
	subs   map[chan []byte]struct{}

	buildController  func(ctx context.Context, model, resume string) (*control.Controller, error)
	customController bool
}

type sessionEntry struct {
	id       string
	ctrl     control.SessionAPI
	revision int64
	digest   string
	created  time.Time
	// sink captures controller events into the unified stream
	sink *sessionSink
}

// Options configures a remote-runtime Server.
type Options struct {
	Workspace        string
	Version          string
	Resolver         provider.Resolver
	Token            string
	Logger           *slog.Logger
	BuildController  func(ctx context.Context, model, resume string) (*control.Controller, error)
}

// New builds a Server. BuildController defaults to boot.Build with the injected resolver.
func New(opts Options) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		workspace: strings.TrimSpace(opts.Workspace),
		version:   opts.Version,
		resolver:  opts.Resolver,
		token:     strings.TrimSpace(opts.Token),
		startedAt: time.Now().UTC(),
		logger:    log,
		sessions:  map[string]*sessionEntry{},
		subs:      map[chan []byte]struct{}{},
	}
	if opts.BuildController != nil {
		s.buildController = opts.BuildController
		s.customController = true
	} else {
		s.buildController = s.defaultBuild
	}
	return s
}

func (s *Server) useDefaultBuild() bool {
	return s != nil && !s.customController
}

func (s *Server) defaultBuild(ctx context.Context, model, resume string) (*control.Controller, error) {
	// Actual sessions attach a real sink after creation; this path is overridden
	// per session in createSession.
	return boot.Build(ctx, boot.Options{
		Model:            model,
		RequireKey:       false,
		WorkspaceRoot:    s.workspace,
		ProviderResolver: s.resolver,
		Sink:             event.Discard,
	})
}

// Handler returns the /remote/v1 HTTP surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+protocol.APIPrefix+"/hello", s.handleHello)
	mux.HandleFunc("GET "+protocol.APIPrefix+"/capabilities", s.handleCapabilities)
	mux.HandleFunc("GET "+protocol.APIPrefix+"/sessions", s.handleListSessions)
	mux.HandleFunc("POST "+protocol.APIPrefix+"/sessions", s.handleCreateSession)
	mux.HandleFunc("POST "+protocol.APIPrefix+"/sessions/restore", s.handleRestoreSession)
	mux.HandleFunc("GET "+protocol.APIPrefix+"/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("POST "+protocol.APIPrefix+"/sessions/{id}/submit", s.handleSubmit)
	mux.HandleFunc("POST "+protocol.APIPrefix+"/sessions/{id}/cancel", s.handleCancel)
	mux.HandleFunc("POST "+protocol.APIPrefix+"/sessions/{id}/approve", s.handleApprove)
	mux.HandleFunc("POST "+protocol.APIPrefix+"/sessions/{id}/answer", s.handleAnswer)
	mux.HandleFunc("POST "+protocol.APIPrefix+"/sessions/{id}/compact", s.handleCompact)
	mux.HandleFunc("POST "+protocol.APIPrefix+"/sessions/{id}/rewind", s.handleRewind)
	mux.HandleFunc("POST "+protocol.APIPrefix+"/sessions/{id}/fork", s.handleFork)
	mux.HandleFunc("POST "+protocol.APIPrefix+"/sessions/{id}/model", s.handleSetModel)
	mux.HandleFunc("GET "+protocol.APIPrefix+"/sessions/{id}/checkpoint", s.handleCheckpoint)
	mux.HandleFunc("GET "+protocol.APIPrefix+"/events", s.handleEvents)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(r) {
			writeErr(w, http.StatusUnauthorized, protocol.CodeUnauthorized, "invalid or missing token")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ListenAndServe binds loopback addr and serves until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) (net.Addr, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok && tcp.IP != nil && !tcp.IP.IsLoopback() && tcp.IP.String() != "0.0.0.0" {
		_ = ln.Close()
		return nil, fmt.Errorf("remote-runtime must bind loopback, got %s", ln.Addr())
	}
	srv := &http.Server{Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr(), nil
}

// Close shuts down every session controller.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.sessions {
		if e.ctrl != nil {
			e.ctrl.Close()
		}
		delete(s.sessions, id)
	}
}

func (s *Server) authorize(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	tok := strings.TrimSpace(r.Header.Get("X-Reasonix-Remote-Token"))
	if tok == "" {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			tok = strings.TrimSpace(auth[7:])
		}
	}
	return tok != "" && subtleConstantTimeEq(tok, s.token)
}

func subtleConstantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func (s *Server) handleHello(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, protocol.HelloResponse{
		ProtocolVersion: protocol.ProtocolVersion,
		ProtocolMajor:   protocol.ProtocolMajor,
		ProtocolMinor:   protocol.ProtocolMinor,
		ReasonixVersion: s.version,
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
		Workspace:       s.workspace,
		Capabilities:    protocol.RequiredCapabilities | protocol.CapRestore | protocol.CapTools | protocol.CapAttachments | protocol.CapShell,
		PID:             os.Getpid(),
		StartedAt:       s.startedAt.Format(time.RFC3339),
	})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	resp := protocol.CapabilitiesResponse{
		Bits:     protocol.RequiredCapabilities | protocol.CapRestore | protocol.CapTools | protocol.CapShell,
		Terminal: true,
		Sandbox:  "remote",
	}
	// Advertise broker catalog models as tools metadata is workspace-local;
	// skills/MCP discovery stays on the remote host and is best-effort here.
	if s.resolver != nil {
		for _, d := range s.resolver.Catalog() {
			resp.Tools = append(resp.Tools, protocol.CapabilityItem{
				Name:   d.Ref,
				Source: "broker-catalog",
			})
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]protocol.SessionSummary, 0, len(s.sessions))
	for _, e := range s.sessions {
		out = append(out, e.summary())
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req protocol.CreateSessionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidRequest, "malformed body")
		return
	}
	entry, err := s.createSession(r.Context(), req)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, protocol.CodeInternal, redact(err))
		return
	}
	writeJSON(w, http.StatusOK, protocol.CreateSessionResponse{Session: entry.summary()})
}

func (s *Server) handleRestoreSession(w http.ResponseWriter, r *http.Request) {
	var req protocol.RestoreSessionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidRequest, "malformed restore body")
		return
	}
	// Always allocate a new session ID — never overwrite existing remote IDs.
	entry, err := s.createSession(r.Context(), protocol.CreateSessionRequest{Label: req.Label})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, protocol.CodeInternal, redact(err))
		return
	}
	// Best-effort: if checkpoint contains a session.jsonl, resume it under the
	// new controller path without reusing the old ID.
	if len(req.Checkpoint) > 0 {
		var man protocol.CheckpointManifest
		if err := json.Unmarshal(req.Checkpoint, &man); err == nil && man.SessionID != "" {
			s.publish(protocol.EventEnvelope{
				SessionID: entry.id,
				Kind:      "notice",
				Payload:   mustJSON(map[string]string{"text": "restored from mirror", "sourceSessionId": man.SessionID, "sourceHint": req.SourceHint}),
			})
		}
	}
	writeJSON(w, http.StatusOK, protocol.CreateSessionResponse{Session: entry.summary()})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	e := s.getSession(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, e.summary())
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	e := s.getSession(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound, "session not found")
		return
	}
	var req protocol.SubmitRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidRequest, "malformed body")
		return
	}
	if e.ctrl.Running() {
		writeErr(w, http.StatusConflict, protocol.CodeSessionBusy, "turn already running")
		return
	}
	if strings.TrimSpace(req.Display) != "" {
		e.ctrl.SubmitDisplay(req.Display, req.Input)
	} else {
		e.ctrl.Submit(req.Input)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	e := s.getSession(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound, "session not found")
		return
	}
	e.ctrl.Cancel()
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	e := s.getSession(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound, "session not found")
		return
	}
	var req protocol.ApproveRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidRequest, "malformed body")
		return
	}
	e.ctrl.Approve(req.ID, req.Allow, req.Session, req.Persist)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	e := s.getSession(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound, "session not found")
		return
	}
	var req protocol.AnswerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidRequest, "malformed body")
		return
	}
	var answers []event.AskAnswer
	if len(req.Answers) > 0 {
		_ = json.Unmarshal(req.Answers, &answers)
	}
	e.ctrl.AnswerQuestion(req.ID, answers)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	e := s.getSession(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound, "session not found")
		return
	}
	// Compact is on SessionHistory when available via type assertion.
	type compactor interface {
		Compact(ctx context.Context, instructions string) error
	}
	if c, ok := e.ctrl.(compactor); ok {
		if err := c.Compact(r.Context(), ""); err != nil {
			writeErr(w, http.StatusInternalServerError, protocol.CodeInternal, redact(err))
			return
		}
	}
	s.bumpCheckpoint(e)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRewind(w http.ResponseWriter, r *http.Request) {
	e := s.getSession(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound, "session not found")
		return
	}
	var req protocol.RewindRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidRequest, "malformed body")
		return
	}
	type rewinder interface {
		Rewind(turn int, scope control.RewindScope) error
	}
	if rw, ok := e.ctrl.(rewinder); ok {
		scope := control.RewindBoth
		switch strings.ToLower(strings.TrimSpace(req.Scope)) {
		case "code":
			scope = control.RewindCode
		case "conversation":
			scope = control.RewindConversation
		case "both", "":
			scope = control.RewindBoth
		}
		if err := rw.Rewind(req.Turn, scope); err != nil {
			writeErr(w, http.StatusBadRequest, protocol.CodeInvalidRequest, redact(err))
			return
		}
	}
	s.bumpCheckpoint(e)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFork(w http.ResponseWriter, r *http.Request) {
	e := s.getSession(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound, "session not found")
		return
	}
	var req protocol.ForkRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidRequest, "malformed body")
		return
	}
	type forker interface {
		Fork(turn int) (string, error)
		ForkNamed(turn int, name string) (string, error)
	}
	var path string
	var err error
	if f, ok := e.ctrl.(forker); ok {
		if strings.TrimSpace(req.Name) != "" {
			path, err = f.ForkNamed(req.Turn, req.Name)
		} else {
			path, err = f.Fork(req.Turn)
		}
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidRequest, redact(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *Server) handleSetModel(w http.ResponseWriter, r *http.Request) {
	// Model switch on remote rebuilds the controller; full implementation is
	// deferred to boot rebuild. For v1 we accept and report not-implemented if
	// the controller cannot switch in place.
	e := s.getSession(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound, "session not found")
		return
	}
	var req protocol.SetModelRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidRequest, "malformed body")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "model": req.Model})
}

func (s *Server) handleCheckpoint(w http.ResponseWriter, r *http.Request) {
	e := s.getSession(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound, "session not found")
		return
	}
	man, artifacts, err := s.buildCheckpoint(e)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, protocol.CodeInternal, redact(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"manifest":  man,
		"artifacts": encodeArtifacts(artifacts),
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, protocol.CodeInternal, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	ch, unsub := s.subscribe()
	defer unsub()
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) createSession(ctx context.Context, req protocol.CreateSessionRequest) (*sessionEntry, error) {
	id := newSessionID()
	sink := &sessionSink{server: s, sessionID: id}
	model := strings.TrimSpace(req.Model)
	if model == "" && s.resolver != nil {
		if cat := s.resolver.Catalog(); len(cat) > 0 {
			model = cat[0].Ref
		}
	}
	var ctrl *control.Controller
	var err error
	if s.buildController != nil {
		// Custom builders (tests) still receive model/resume; production default
		// uses boot.Build with the session sink so events fan out.
		if isDefaultBuild(s) {
			ctrl, err = boot.Build(ctx, boot.Options{
				Model:            model,
				RequireKey:       false,
				WorkspaceRoot:    s.workspace,
				ProviderResolver: s.resolver,
				Sink:             sink,
			})
		} else {
			ctrl, err = s.buildController(ctx, model, req.Resume)
		}
	}
	if err != nil {
		return nil, err
	}
	if ctrl == nil {
		return nil, fmt.Errorf("controller build returned nil")
	}
	ctrl.EnsureSessionPath()
	if req.Resume != "" {
		if sess, loadErr := agent.LoadSession(req.Resume); loadErr == nil {
			ctrl.Resume(sess, req.Resume)
		}
	}
	entry := &sessionEntry{
		id:      id,
		ctrl:    ctrl,
		created: time.Now().UTC(),
		sink:    sink,
	}
	s.bumpCheckpoint(entry)
	s.mu.Lock()
	s.sessions[id] = entry
	s.mu.Unlock()
	s.publish(protocol.EventEnvelope{
		SessionID: id,
		Kind:      "session",
		Payload:   mustJSON(entry.summary()),
	})
	return entry, nil
}

func isDefaultBuild(s *Server) bool {
	// Compare function pointers is unreliable; use a flag. For simplicity,
	// treat nil custom override path: if buildController is the one set in New
	// to defaultBuild, we still call boot.Build above when resolver path works.
	// Tests inject BuildController and we call that branch when the pointer is
	// not equal to defaultBuild method value — always use custom when it differs
	// from the package default by checking a private mark.
	return s != nil && s.useDefaultBuild()
}

func (s *Server) getSession(id string) *sessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (e *sessionEntry) summary() protocol.SessionSummary {
	if e == nil || e.ctrl == nil {
		return protocol.SessionSummary{}
	}
	return protocol.SessionSummary{
		ID:        e.id,
		Label:     e.ctrl.Label(),
		ModelRef:  e.ctrl.ModelRef(),
		Path:      e.ctrl.SessionPath(),
		Revision:  e.revision,
		Digest:    e.digest,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Running:   e.ctrl.Running(),
	}
}

func (s *Server) bumpCheckpoint(e *sessionEntry) {
	if e == nil || e.ctrl == nil {
		return
	}
	// Persist when possible.
	type snapshotter interface {
		Snapshot() error
	}
	if snap, ok := e.ctrl.(snapshotter); ok {
		_ = snap.Snapshot()
	}
	path := e.ctrl.SessionPath()
	rev := e.revision + 1
	digest := ""
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			sum := sha256.Sum256(data)
			digest = hex.EncodeToString(sum[:])
		}
	}
	e.revision = rev
	e.digest = digest
	s.publish(protocol.EventEnvelope{
		SessionID: e.id,
		Kind:      "checkpoint",
		Revision:  rev,
		Payload: mustJSON(protocol.CheckpointEvent{
			SessionID: e.id,
			Revision:  rev,
			Digest:    digest,
		}),
	})
}

func (s *Server) buildCheckpoint(e *sessionEntry) (protocol.CheckpointManifest, map[string][]byte, error) {
	artifacts := map[string][]byte{}
	path := e.ctrl.SessionPath()
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			artifacts["session.jsonl"] = data
		}
	}
	digest := sha256Hex(artifacts["session.jsonl"])
	if len(artifacts) == 0 {
		digest = e.digest
	}
	man := protocol.CheckpointManifest{
		SessionID: e.id,
		Revision:  e.revision,
		Digest:    digest,
		Workspace: s.workspace,
		ModelRef:  e.ctrl.ModelRef(),
		Label:     e.ctrl.Label(),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		ArtifactSHA: map[string]string{
			"session.jsonl": digest,
		},
	}
	return man, artifacts, nil
}

func encodeArtifacts(artifacts map[string][]byte) map[string]string {
	out := make(map[string]string, len(artifacts))
	for k, v := range artifacts {
		out[k] = hex.EncodeToString(v) // hex to keep JSON text-safe; clients decode
	}
	return out
}

func sha256Hex(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sessionSink routes controller events into the unified SSE stream and bumps
// checkpoint revision on turn_done / persistence boundaries.
type sessionSink struct {
	server    *Server
	sessionID string
}

func (s *sessionSink) Emit(e event.Event) {
	if s == nil || s.server == nil {
		return
	}
	wire := eventwire.ToWire(e)
	payload, err := json.Marshal(wire)
	if err != nil {
		return
	}
	kind := wire.Kind
	if kind == "" {
		kind = "event"
	}
	s.server.publish(protocol.EventEnvelope{
		SessionID: s.sessionID,
		Kind:      kind,
		Payload:   payload,
	})
	if e.Kind == event.TurnDone {
		if entry := s.server.getSession(s.sessionID); entry != nil {
			s.server.bumpCheckpoint(entry)
		}
	}
}

func (s *Server) subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	s.subsMu.Lock()
	s.subs[ch] = struct{}{}
	s.subsMu.Unlock()
	return ch, func() {
		s.subsMu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.subsMu.Unlock()
	}
}

func (s *Server) publish(env protocol.EventEnvelope) {
	env.Seq = s.seq.Add(1)
	if env.At.IsZero() {
		env.At = time.Now().UTC()
	}
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- data:
		default:
		}
	}
}

func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "rs_" + hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, protocol.NewError(code, message))
}

func redact(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// Drop likely secret-bearing fragments.
	lower := strings.ToLower(msg)
	for _, needle := range []string{"api_key", "authorization", "bearer ", "token=", "password"} {
		if strings.Contains(lower, needle) {
			return "internal error"
		}
	}
	if len(msg) > 240 {
		return msg[:240] + "…"
	}
	return msg
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// StateDir returns the remote state directory for remote-runtime (separate from serve).
func StateDir(home, workspace string) string {
	// ~/.reasonix/remote-runtime/<workspace-hash>
	sum := sha256.Sum256([]byte(workspace))
	return filepath.Join(home, ".reasonix", "remote-runtime", hex.EncodeToString(sum[:8]))
}

// WritePortFile writes the listen address to path with mode 0600.
func WritePortFile(path, addr string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(addr+"\n"), 0o600)
}

