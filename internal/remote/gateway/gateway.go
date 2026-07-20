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
	CreatedAt      time.Time
}

// Server is the local loopback gateway for remote AppBridge children.
type Server struct {
	mu       sync.Mutex
	sessions map[string]*Session
	tickets  map[string]*ticket // ticket id → session id (one-shot)
	token    string             // long-lived child auth token for this process
	ln       net.Listener
	srv      *http.Server
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
	// Child also needs the gateway base URL; parent fills after listen.
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

// Handler serves gateway RPC.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gateway/v1/hello", s.handleHello)
	mux.HandleFunc("GET /gateway/v1/session", s.handleSession)
	mux.HandleFunc("GET /gateway/v1/remote/hello", s.proxyRemote("GET", protocol.APIPrefix+"/hello"))
	mux.HandleFunc("GET /gateway/v1/remote/sessions", s.proxyRemote("GET", protocol.APIPrefix+"/sessions"))
	mux.HandleFunc("POST /gateway/v1/remote/sessions", s.proxyRemote("POST", protocol.APIPrefix+"/sessions"))
	mux.HandleFunc("POST /gateway/v1/remote/sessions/{id}/submit", s.proxyRemotePath)
	mux.HandleFunc("POST /gateway/v1/remote/sessions/{id}/cancel", s.proxyRemotePath)
	mux.HandleFunc("GET /gateway/v1/remote/events", s.proxyEvents)
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
		"id":             sess.ID,
		"hostId":         sess.HostID,
		"workspace":      sess.Workspace,
		"connectionId":   sess.ConnectionID,
		"brokerStatus":   sess.BrokerStatus,
		"mirrorRevision": sess.MirrorRevision,
		"executionTarget": target.ExecutionTarget{
			Kind:         target.KindSSH,
			HostID:       sess.HostID,
			Workspace:    sess.Workspace,
			ConnectionID: sess.ConnectionID,
		},
	})
}

func (s *Server) proxyRemote(method, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := s.sessionFrom(r)
		if sess == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		s.doProxy(w, r, sess, method, path)
	}
}

func (s *Server) proxyRemotePath(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFrom(r)
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	// Map /gateway/v1/remote/sessions/{id}/... → /remote/v1/sessions/{id}/...
	suffix := strings.TrimPrefix(r.URL.Path, "/gateway/v1/remote")
	s.doProxy(w, r, sess, r.Method, protocol.APIPrefix+suffix)
}

func (s *Server) proxyEvents(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFrom(r)
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	s.doProxy(w, r, sess, http.MethodGet, protocol.APIPrefix+"/events")
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
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "remote unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if flusher, ok := w.(http.Flusher); ok {
		// Stream SSE / large bodies without buffering the whole response.
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
