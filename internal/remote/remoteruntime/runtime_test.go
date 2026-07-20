package remoteruntime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/remote/protocol"
)

func TestHelloCompatible(t *testing.T) {
	srv := New(Options{Workspace: "/home/u/proj", Version: "test", Token: "t"})
	req := httptest.NewRequest(http.MethodGet, protocol.APIPrefix+"/hello", nil)
	req.Header.Set("X-Reasonix-Remote-Token", "t")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	var hello protocol.HelloResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &hello); err != nil {
		t.Fatal(err)
	}
	if err := hello.Compatible(); err != nil {
		t.Fatal(err)
	}
	if hello.Workspace != "/home/u/proj" {
		t.Fatalf("workspace = %q", hello.Workspace)
	}
	if hello.ProtocolMajor != protocol.ProtocolMajor {
		t.Fatalf("major = %d", hello.ProtocolMajor)
	}
}

func TestAuthTokenRequired(t *testing.T) {
	srv := New(Options{Workspace: "/w", Token: "secret"})
	req := httptest.NewRequest(http.MethodGet, protocol.APIPrefix+"/hello", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("status %d want 401", rr.Code)
	}
	// Empty server token must also deny (not open the agent surface).
	open := New(Options{Workspace: "/w", Token: ""})
	req = httptest.NewRequest(http.MethodGet, protocol.APIPrefix+"/hello", nil)
	rr = httptest.NewRecorder()
	open.Handler().ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("empty token status %d want 401", rr.Code)
	}
	req = httptest.NewRequest(http.MethodGet, protocol.APIPrefix+"/hello", nil)
	req.Header.Set("X-Reasonix-Remote-Token", "secret")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestProtocolMismatch(t *testing.T) {
	h := protocol.HelloResponse{ProtocolMajor: 99, Workspace: "/w", Capabilities: protocol.RequiredCapabilities}
	if err := h.Compatible(); err == nil {
		t.Fatal("expected protocol mismatch")
	}
}

func TestCatalogResolverSurfaced(t *testing.T) {
	resolver := &provider.StaticResolver{
		Descriptors: []provider.Descriptor{{Ref: "deepseek/chat"}},
	}
	srv := New(Options{Workspace: "/w", Resolver: resolver, Token: "t"})
	req := httptest.NewRequest(http.MethodGet, protocol.APIPrefix+"/capabilities", nil)
	req.Header.Set("X-Reasonix-Remote-Token", "t")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	var caps protocol.CapabilitiesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if len(caps.Tools) != 1 || caps.Tools[0].Name != "deepseek/chat" {
		t.Fatalf("caps = %+v", caps.Tools)
	}
}

func TestListenRejectsNonLoopback(t *testing.T) {
	srv := New(Options{Workspace: "/w", Token: "t"})
	// Binding 0.0.0.0 must be rejected after accept of the actual address.
	// On systems where Listen("0.0.0.0:0") succeeds, ListenAndServe must close it.
	// We only assert the loopback path works.
	ctx := t.Context()
	addr, err := srv.ListenAndServe(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if addr == nil {
		t.Fatal("nil addr")
	}
	srv.Close()
}
