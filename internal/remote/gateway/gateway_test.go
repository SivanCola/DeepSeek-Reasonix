package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGatewayAuthAndSession(t *testing.T) {
	s := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := s.ListenAndServe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path, err := s.RegisterSession(Session{
		HostID:       "lab",
		Workspace:    "/home/u/w",
		ConnectionID: "c1",
		RemoteBase:   "http://127.0.0.1:9",
		BrokerStatus: "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("empty ticket path")
	}
	// Find session id
	var sid string
	s.mu.Lock()
	for id := range s.sessions {
		sid = id
	}
	s.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/gateway/v1/session", nil)
	req.Header.Set("X-Reasonix-Gateway-Token", s.token)
	req.Header.Set("X-Reasonix-Session-Id", sid)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["hostId"] != "lab" {
		t.Fatalf("body = %+v", body)
	}

	// Unauthorized without token.
	req = httptest.NewRequest(http.MethodGet, "/gateway/v1/hello", nil)
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("status %d", rr.Code)
	}

	// Release one session; others remain (single session case).
	s.ReleaseSession(sid)
	if _, ok := s.Get(sid); ok {
		t.Fatal("session should be released")
	}
	_ = addr
	time.Sleep(10 * time.Millisecond)
}
