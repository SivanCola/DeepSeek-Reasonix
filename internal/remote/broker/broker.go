// Package broker implements the local Provider Broker that remote-runtime
// reaches through an SSH reverse tunnel bound only to 127.0.0.1. API keys stay
// on the local machine; the remote side only receives non-sensitive catalog
// descriptors and typed stream chunks.
package broker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"reasonix/internal/provider"
)

// Protocol path prefix for broker HTTP endpoints.
const APIPrefix = "/broker/v1"

// CapabilityToken is a 256-bit random token scoped to one connection.
type CapabilityToken [32]byte

// NewCapabilityToken generates a random capability token.
func NewCapabilityToken() (CapabilityToken, error) {
	var t CapabilityToken
	if _, err := rand.Read(t[:]); err != nil {
		return CapabilityToken{}, err
	}
	return t, nil
}

// String returns the hex encoding (for file storage only; never logs).
func (t CapabilityToken) String() string { return hex.EncodeToString(t[:]) }

// ParseCapabilityToken decodes a hex token.
func ParseCapabilityToken(s string) (CapabilityToken, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != 32 {
		return CapabilityToken{}, fmt.Errorf("invalid capability token")
	}
	var t CapabilityToken
	copy(t[:], b)
	return t, nil
}

// Equal compares tokens in constant time.
func (t CapabilityToken) Equal(other CapabilityToken) bool {
	return subtle.ConstantTimeCompare(t[:], other[:]) == 1
}

// FingerprintHash is a stable directory key derived from a host key fingerprint.
func FingerprintHash(fingerprint string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(fingerprint)))
	return hex.EncodeToString(sum[:16])
}

// TrustRecord is the durable host authorization for remote provider use.
type TrustRecord struct {
	HostID              string    `json:"hostId"`
	HostKeyAlgorithm    string    `json:"hostKeyAlgorithm"`
	HostKeyFingerprint  string    `json:"hostKeyFingerprint"`
	AllowedProviderRefs []string  `json:"allowedProviderRefs"`
	ApprovedAt          time.Time `json:"approvedAt"`
}

// Scope is the runtime authorization for one SSH connection.
type Scope struct {
	Token         CapabilityToken
	HostID        string
	Fingerprint   string
	Workspace     string
	AllowedRefs   map[string]struct{}
	CreatedAt     time.Time
	ExpiresAt     time.Time // zero = until revoke
	RequestLimit  int       // max concurrent streams; 0 = default
	RatePerMinute int       // 0 = default
}

// Server is the local broker HTTP surface.
type Server struct {
	mu       sync.Mutex
	resolver provider.Resolver
	scopes   map[string]*liveScope // token hex → scope
	logger   *slog.Logger

	// defaults
	maxConcurrent int
	ratePerMinute int
}

type liveScope struct {
	Scope
	inflight atomic.Int32
	// simple sliding window for rate limit
	windowStart time.Time
	windowCount int
}

// Options configures a broker Server.
type Options struct {
	Resolver      provider.Resolver
	Logger        *slog.Logger
	MaxConcurrent int // default 8
	RatePerMinute int // default 120
}

// NewServer builds a broker server.
func NewServer(opts Options) *Server {
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 8
	}
	if opts.RatePerMinute <= 0 {
		opts.RatePerMinute = 120
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		resolver:      opts.Resolver,
		scopes:        map[string]*liveScope{},
		logger:        log,
		maxConcurrent: opts.MaxConcurrent,
		ratePerMinute: opts.RatePerMinute,
	}
}

// Issue registers a new capability scope and returns its token.
func (s *Server) Issue(scope Scope) (CapabilityToken, error) {
	if s == nil {
		return CapabilityToken{}, errors.New("broker: server is nil")
	}
	if scope.HostID == "" || scope.Fingerprint == "" || scope.Workspace == "" {
		return CapabilityToken{}, errors.New("broker: hostId, fingerprint, and workspace are required")
	}
	if scope.Token == (CapabilityToken{}) {
		tok, err := NewCapabilityToken()
		if err != nil {
			return CapabilityToken{}, err
		}
		scope.Token = tok
	}
	if scope.AllowedRefs == nil {
		scope.AllowedRefs = map[string]struct{}{}
	}
	if scope.CreatedAt.IsZero() {
		scope.CreatedAt = time.Now().UTC()
	}
	if scope.RequestLimit <= 0 {
		scope.RequestLimit = s.maxConcurrent
	}
	if scope.RatePerMinute <= 0 {
		scope.RatePerMinute = s.ratePerMinute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes[scope.Token.String()] = &liveScope{Scope: scope, windowStart: time.Now()}
	return scope.Token, nil
}

// Revoke invalidates a capability token.
func (s *Server) Revoke(token CapabilityToken) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.scopes, token.String())
}

// RevokeHost removes every token issued for hostID+fingerprint.
func (s *Server) RevokeHost(hostID, fingerprint string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, sc := range s.scopes {
		if sc.HostID == hostID && sc.Fingerprint == fingerprint {
			delete(s.scopes, k)
		}
	}
}

// Close revokes all tokens.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes = map[string]*liveScope{}
}

// Handler returns the HTTP handler for /broker/v1/*.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+APIPrefix+"/catalog", s.handleCatalog)
	mux.HandleFunc("POST "+APIPrefix+"/stream", s.handleStream)
	mux.HandleFunc("GET "+APIPrefix+"/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Broker must only be reached via loopback reverse tunnels.
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// ListenAndServe binds addr (must be loopback) and serves until ctx cancel.
func (s *Server) ListenAndServe(ctx context.Context, addr string) (net.Addr, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		if tcp.IP != nil && !tcp.IP.IsLoopback() && tcp.IP.String() != "0.0.0.0" && tcp.IP.String() != "::" {
			_ = ln.Close()
			return nil, fmt.Errorf("broker: must bind loopback, got %s", ln.Addr())
		}
	}
	srv := &http.Server{Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	go func() {
		_ = srv.Serve(ln)
	}()
	return ln.Addr(), nil
}

func (s *Server) authenticate(r *http.Request) (*liveScope, error) {
	raw := strings.TrimSpace(r.Header.Get("X-Reasonix-Broker-Token"))
	if raw == "" {
		// Authorization: Bearer <token>
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			raw = strings.TrimSpace(auth[7:])
		}
	}
	if raw == "" {
		return nil, errUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.scopes[raw]
	if !ok {
		return nil, errUnauthorized
	}
	if !sc.ExpiresAt.IsZero() && time.Now().After(sc.ExpiresAt) {
		delete(s.scopes, raw)
		return nil, errUnauthorized
	}
	return sc, nil
}

func (s *Server) allowRef(sc *liveScope, ref string) bool {
	if sc == nil {
		return false
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	if len(sc.AllowedRefs) == 0 {
		return false
	}
	if _, ok := sc.AllowedRefs[ref]; ok {
		return true
	}
	// Allow name-only selection when catalog used name/model.
	for allowed := range sc.AllowedRefs {
		if allowed == ref || strings.HasPrefix(allowed, ref+"/") || strings.HasSuffix(allowed, "/"+ref) {
			return true
		}
	}
	return false
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	sc, err := s.authenticate(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid or missing capability token")
		return
	}
	if s.resolver == nil {
		writeJSON(w, http.StatusOK, catalogResponse{Providers: nil})
		return
	}
	all := s.resolver.Catalog()
	out := make([]provider.Descriptor, 0, len(all))
	for _, d := range all {
		if s.allowRef(sc, d.Ref) {
			out = append(out, d)
		}
	}
	writeJSON(w, http.StatusOK, catalogResponse{Providers: out})
}

type catalogResponse struct {
	Providers []provider.Descriptor `json:"providers"`
}

type streamRequest struct {
	ProviderRef string           `json:"providerRef"`
	Effort      *string          `json:"effort,omitempty"`
	Request     provider.Request `json:"request"`
}

type streamChunkWire struct {
	// Type uses the provider.ChunkType integer enum so local and broker modes
	// share the same stream discrimination without a parallel string dialect.
	Type      provider.ChunkType `json:"type"`
	Text      string             `json:"text,omitempty"`
	Signature string             `json:"signature,omitempty"`
	ToolCall  json.RawMessage    `json:"toolCall,omitempty"`
	ArgChars  int                `json:"argChars,omitempty"`
	Usage     json.RawMessage    `json:"usage,omitempty"`
	Error     string             `json:"error,omitempty"`
	ErrorCode string             `json:"errorCode,omitempty"`
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	sc, err := s.authenticate(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid or missing capability token")
		return
	}
	var body streamRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "malformed stream request")
		return
	}
	if !s.allowRef(sc, body.ProviderRef) {
		s.logger.Info("broker denied provider", "request_id", r.Header.Get("X-Request-Id"), "provider_ref", body.ProviderRef, "host_id", sc.HostID)
		writeErr(w, http.StatusForbidden, "broker_denied", "provider not authorized for this connection")
		return
	}
	if err := s.admit(sc); err != nil {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", err.Error())
		return
	}
	defer sc.inflight.Add(-1)

	start := time.Now()
	reqID := r.Header.Get("X-Request-Id")
	if reqID == "" {
		id := randomID()
		reqID = hex.EncodeToString(id[:8])
	}

	if s.resolver == nil {
		writeErr(w, http.StatusServiceUnavailable, "broker_unavailable", "no provider resolver")
		return
	}
	p, err := s.resolver.Resolve(provider.Selection{Ref: body.ProviderRef, Effort: body.Effort})
	if err != nil {
		s.logger.Info("broker resolve failed", "request_id", reqID, "provider_ref", body.ProviderRef, "err_code", "resolve_failed", "duration_ms", time.Since(start).Milliseconds())
		writeErr(w, http.StatusBadRequest, "invalid_request", "provider resolve failed")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, err := p.Stream(r.Context(), body.Request)
	if err != nil {
		_ = writeNDJSON(w, streamChunkWire{Type: provider.ChunkError, Error: redactErr(err), ErrorCode: "stream_open"})
		flusher.Flush()
		s.logger.Info("broker stream open failed", "request_id", reqID, "provider_ref", body.ProviderRef, "duration_ms", time.Since(start).Milliseconds())
		return
	}
	for chunk := range ch {
		wire := chunkToWire(chunk)
		if err := writeNDJSON(w, wire); err != nil {
			return
		}
		flusher.Flush()
	}
	s.logger.Info("broker stream done", "request_id", reqID, "provider_ref", body.ProviderRef, "duration_ms", time.Since(start).Milliseconds())
}

func (s *Server) admit(sc *liveScope) error {
	if int(sc.inflight.Add(1)) > sc.RequestLimit {
		sc.inflight.Add(-1)
		return errors.New("too many concurrent broker streams")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Sub(sc.windowStart) >= time.Minute {
		sc.windowStart = now
		sc.windowCount = 0
	}
	if sc.windowCount >= sc.RatePerMinute {
		sc.inflight.Add(-1)
		return errors.New("broker rate limit exceeded")
	}
	sc.windowCount++
	return nil
}

func chunkToWire(c provider.Chunk) streamChunkWire {
	w := streamChunkWire{Type: c.Type, Text: c.Text, Signature: c.Signature, ArgChars: c.ArgChars}
	if c.ToolCall != nil {
		if b, err := json.Marshal(c.ToolCall); err == nil {
			w.ToolCall = b
		}
	}
	if c.Usage != nil {
		if b, err := json.Marshal(c.Usage); err == nil {
			w.Usage = b
		}
	}
	if c.Err != nil {
		w.Type = provider.ChunkError
		w.Error = redactErr(c.Err)
		w.ErrorCode = "stream_error"
	}
	return w
}

func redactErr(err error) string {
	if err == nil {
		return ""
	}
	// Never echo raw provider bodies; AuthError already sanitizes Error().
	msg := err.Error()
	if len(msg) > 240 {
		msg = msg[:240] + "…"
	}
	return msg
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func writeNDJSON(w http.ResponseWriter, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}

func randomID() [16]byte {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return b
}

var errUnauthorized = errors.New("unauthorized")
