// Package gateway implements the local Remote Gateway that child desktop
// windows use over loopback RPC. Authentication uses a one-shot ticket file
// (mode 0600); the token never appears in argv, URL query strings, logs, or DOM.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/remote/protocol"
	"reasonix/internal/remote/target"
)

// Session is one remote desktop window's gateway binding.
type Session struct {
	ID           string
	HostID       string
	Workspace    string
	ConnectionID string
	// RemoteBase is the local-forwarded remote-runtime base URL (loopback).
	RemoteBase string
	// RemoteToken authenticates to remote-runtime.
	RemoteToken string
	// Fingerprint is the trusted host key fingerprint for this connection.
	Fingerprint string
	// BrokerStatus is a non-sensitive status string for the UI.
	BrokerStatus string
	// MirrorRevision is the latest applied local mirror revision (0 if none).
	MirrorRevision int64
	// ActiveRemoteSession is the last remote-runtime session id used by the child.
	ActiveRemoteSession string
	CreatedAt           time.Time
}

// DirEntry is a workspace directory listing entry (SFTP-backed).
type DirEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"isDir"`
	Size      int64  `json:"size"`
	MtimeUnix int64  `json:"mtimeUnix"`
	Symlink   bool   `json:"symlink"`
}

// FilePreview is a workspace file read result.
type FilePreview struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Size      int64  `json:"size"`
	MtimeUnix int64  `json:"mtimeUnix"`
	Truncated bool   `json:"truncated"`
	Binary    bool   `json:"binary"`
	Err       string `json:"err,omitempty"`
}

// WriteResult reports a workspace write outcome.
type WriteResult struct {
	OK           bool  `json:"ok"`
	Conflict     bool  `json:"conflict"`
	NewMtimeUnix int64 `json:"newMtimeUnix"`
}

// WorkspaceBackend is optional parent-process access to remote files/Git via
// the live SSH connection. Nil disables /gateway/v1/fs and /gateway/v1/git.
type WorkspaceBackend interface {
	ListDir(ctx context.Context, hostID, path string) ([]DirEntry, error)
	ReadFile(ctx context.Context, hostID, path string) (FilePreview, error)
	WriteFile(ctx context.Context, hostID, path, body string, expectMtime int64) (WriteResult, error)
	GitStatus(ctx context.Context, hostID, workspace string) (string, error)
	GitDiff(ctx context.Context, hostID, workspace string) (string, error)
}

// Server is the local loopback gateway for remote AppBridge children.
type Server struct {
	mu       sync.Mutex
	sessions map[string]*Session
	tickets  map[string]*ticket // ticket id → session id (one-shot)
	token    string             // long-lived child auth token for this process
	ln       net.Listener
	srv      *http.Server
	backend  WorkspaceBackend
}

type ticket struct {
	sessionID string
	expires   time.Time
}

// New creates a gateway server.
func New() *Server {
	tok, _ := randomHex(32)
	return &Server{
		sessions: map[string]*Session{},
		tickets:  map[string]*ticket{},
		token:    tok,
	}
}

// SetWorkspaceBackend attaches SFTP/Git accessors from the parent desktop process.
func (s *Server) SetWorkspaceBackend(b WorkspaceBackend) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.backend = b
	s.mu.Unlock()
}

// ListenAndServe binds a loopback port and serves until ctx cancel.
func (s *Server) ListenAndServe(ctx context.Context) (net.Addr, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.ln = ln
	s.srv = &http.Server{Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shCtx)
	}()
	go func() { _ = s.srv.Serve(ln) }()
	return ln.Addr(), nil
}

// Close stops the server and drops all sessions.
func (s *Server) Close() {
	if s.srv != nil {
		_ = s.srv.Close()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = map[string]*Session{}
	s.tickets = map[string]*ticket{}
}

// Token returns the gateway process token used by child windows (never log it).
func (s *Server) Token() string {
	if s == nil {
		return ""
	}
	return s.token
}

// RegisterSession records a remote window session and returns a one-shot ticket
// path the child process consumes to obtain the gateway token.
func (s *Server) RegisterSession(sess Session) (ticketPath string, err error) {
	if sess.ID == "" {
		id, err := randomHex(12)
		if err != nil {
			return "", err
		}
		sess.ID = "gws_" + id
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now().UTC()
	}
	ticketID, err := randomHex(16)
	if err != nil {
		return "", err
	}
	ticketName := ".remote-gateway-" + ticketID
	dir := config.MemoryUserDir()
	if dir == "" {
		return "", fmt.Errorf("cannot resolve gateway ticket directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, ticketName)
	payload := map[string]string{
		"gatewayToken": s.token,
		"sessionId":    sess.ID,
		"hostId":       sess.HostID,
		"workspace":    sess.Workspace,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.sessions[sess.ID] = &sess
	s.tickets[ticketID] = &ticket{sessionID: sess.ID, expires: time.Now().Add(2 * time.Minute)}
	s.mu.Unlock()
	time.AfterFunc(2*time.Minute, func() { _ = os.Remove(path) })
	return path, nil
}

// ReleaseSession drops one window's gateway session without affecting others.
func (s *Server) ReleaseSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// Get returns a session by id.
func (s *Server) Get(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

// SetActiveRemoteSession remembers which remote-runtime session the child is driving.
func (s *Server) SetActiveRemoteSession(gatewaySessionID, remoteSessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[gatewaySessionID]; ok {
		sess.ActiveRemoteSession = remoteSessionID
	}
}

// Handler serves gateway RPC.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gateway/v1/hello", s.handleHello)
	mux.HandleFunc("GET /gateway/v1/session", s.handleSession)
	mux.HandleFunc("POST /gateway/v1/session/active", s.handleSetActiveRemote)
	// Catch-all remote-runtime proxy: /gateway/v1/remote/... → /remote/v1/...
	mux.HandleFunc("/gateway/v1/remote/", s.proxyRemoteCatchAll)
	// Workspace file/Git (parent SSH).
	mux.HandleFunc("GET /gateway/v1/fs/list", s.handleFSList)
	mux.HandleFunc("GET /gateway/v1/fs/read", s.handleFSRead)
	mux.HandleFunc("POST /gateway/v1/fs/write", s.handleFSWrite)
	mux.HandleFunc("GET /gateway/v1/git/status", s.handleGitStatus)
	mux.HandleFunc("GET /gateway/v1/git/diff", s.handleGitDiff)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) authorize(r *http.Request) bool {
	tok := strings.TrimSpace(r.Header.Get("X-Reasonix-Gateway-Token"))
	if tok == "" {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			tok = strings.TrimSpace(auth[7:])
		}
	}
	return tok != "" && tok == s.token
}

func (s *Server) handleHello(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"version": protocol.ProtocolVersion,
	})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sid := strings.TrimSpace(r.Header.Get("X-Reasonix-Session-Id"))
	sess, ok := s.Get(sid)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":                  sess.ID,
		"hostId":              sess.HostID,
		"workspace":           sess.Workspace,
		"connectionId":        sess.ConnectionID,
		"brokerStatus":        sess.BrokerStatus,
		"mirrorRevision":      sess.MirrorRevision,
		"activeRemoteSession": sess.ActiveRemoteSession,
		"executionTarget": target.ExecutionTarget{
			Kind:         target.KindSSH,
			HostID:       sess.HostID,
			Workspace:    sess.Workspace,
			ConnectionID: sess.ConnectionID,
		},
	})
}

func (s *Server) handleSetActiveRemote(w http.ResponseWriter, r *http.Request) {
	sid := strings.TrimSpace(r.Header.Get("X-Reasonix-Session-Id"))
	var body struct {
		RemoteSessionID string `json:"remoteSessionId"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.SetActiveRemoteSession(sid, strings.TrimSpace(body.RemoteSessionID))
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// proxyRemoteCatchAll maps /gateway/v1/remote/<rest> → <RemoteBase>/remote/v1/<rest>
// including nested session control paths and SSE events.
func (s *Server) proxyRemoteCatchAll(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFrom(r)
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/gateway/v1/remote")
	// Preserve leading slash; empty → hello not used.
	if rest == "" || rest == "/" {
		rest = ""
	}
	path := protocol.APIPrefix + rest
	s.doProxy(w, r, sess, r.Method, path)
}

func (s *Server) sessionFrom(r *http.Request) *Session {
	sid := strings.TrimSpace(r.Header.Get("X-Reasonix-Session-Id"))
	sess, ok := s.Get(sid)
	if !ok {
		return nil
	}
	return sess
}

func (s *Server) doProxy(w http.ResponseWriter, r *http.Request, sess *Session, method, path string) {
	url := strings.TrimRight(sess.RemoteBase, "/") + path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), method, url, r.Body)
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	req.Header.Set("X-Reasonix-Remote-Token", sess.RemoteToken)
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	// Long-lived SSE: no client timeout.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "remote unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		// Avoid double chunked encoding issues.
		if strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if flusher, ok := w.(http.Flusher); ok {
		buf := make([]byte, 32*1024)
		for {
			nr, err := resp.Body.Read(buf)
			if nr > 0 {
				_, _ = w.Write(buf[:nr])
				flusher.Flush()
			}
			if err != nil {
				break
			}
		}
		return
	}
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) backendOrNil() WorkspaceBackend {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend
}

func (s *Server) handleFSList(w http.ResponseWriter, r *http.Request) {
	b := s.backendOrNil()
	sess := s.sessionFrom(r)
	if b == nil || sess == nil {
		http.Error(w, "fs unavailable", http.StatusNotImplemented)
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = sess.Workspace
	}
	entries, err := b.ListDir(r.Context(), sess.HostID, path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
}

func (s *Server) handleFSRead(w http.ResponseWriter, r *http.Request) {
	b := s.backendOrNil()
	sess := s.sessionFrom(r)
	if b == nil || sess == nil {
		http.Error(w, "fs unavailable", http.StatusNotImplemented)
		return
	}
	path := r.URL.Query().Get("path")
	prev, err := b.ReadFile(r.Context(), sess.HostID, path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(prev)
}

func (s *Server) handleFSWrite(w http.ResponseWriter, r *http.Request) {
	b := s.backendOrNil()
	sess := s.sessionFrom(r)
	if b == nil || sess == nil {
		http.Error(w, "fs unavailable", http.StatusNotImplemented)
		return
	}
	var body struct {
		Path        string `json:"path"`
		Body        string `json:"body"`
		ExpectMtime int64  `json:"expectMtime"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	res, err := b.WriteFile(r.Context(), sess.HostID, body.Path, body.Body, body.ExpectMtime)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	b := s.backendOrNil()
	sess := s.sessionFrom(r)
	if b == nil || sess == nil {
		http.Error(w, "git unavailable", http.StatusNotImplemented)
		return
	}
	out, err := b.GitStatus(r.Context(), sess.HostID, sess.Workspace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": out})
}

func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	b := s.backendOrNil()
	sess := s.sessionFrom(r)
	if b == nil || sess == nil {
		http.Error(w, "git unavailable", http.StatusNotImplemented)
		return
	}
	out, err := b.GitDiff(r.Context(), sess.HostID, sess.Workspace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"diff": out})
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ChildTicket describes the payload written for a remote window child process.
type ChildTicket struct {
	GatewayURL   string `json:"gatewayUrl"`
	GatewayToken string `json:"gatewayToken"`
	SessionID    string `json:"sessionId"`
	HostID       string `json:"hostId"`
	Workspace    string `json:"workspace"`
	Title        string `json:"title,omitempty"`
}

// WriteChildTicket writes a mode-0600 ticket file and returns its base name.
func WriteChildTicket(t ChildTicket) (string, error) {
	dir := config.MemoryUserDir()
	if dir == "" {
		return "", fmt.Errorf("cannot resolve ticket directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".remote-window-")
	if err != nil {
		return "", err
	}
	path := f.Name()
	_ = f.Chmod(0o600)
	if err := json.NewEncoder(f).Encode(t); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return filepath.Base(path), nil
}
