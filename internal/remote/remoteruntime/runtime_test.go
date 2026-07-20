package remoteruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"reasonix/internal/control"
	"reasonix/internal/provider"
	"reasonix/internal/remote/protocol"
)

type fakeSession struct {
	control.SessionAPI
	id      string
	label   string
	model   string
	path    string
	running bool
}

func (f *fakeSession) Label() string         { return f.label }
func (f *fakeSession) ModelRef() string      { return f.model }
func (f *fakeSession) SessionPath() string   { return f.path }
func (f *fakeSession) SessionDir() string    { return "" }
func (f *fakeSession) WorkspaceRoot() string { return "/tmp/ws" }
func (f *fakeSession) Running() bool         { return f.running }
func (f *fakeSession) Close()                {}
func (f *fakeSession) Submit(input string)   {}
func (f *fakeSession) SubmitDisplay(display, input string) {
}
func (f *fakeSession) Cancel() {}
func (f *fakeSession) Approve(id string, allow, session, persist bool) {
}
func (f *fakeSession) AnswerQuestion(id string, answers []interface{}) {}
func (f *fakeSession) NewSession() error                               { return nil }
func (f *fakeSession) ClearSession() error                             { return nil }
func (f *fakeSession) Resume(s interface{}, path string)               {}
func (f *fakeSession) SetSessionPath(p string)                         { f.path = p }
func (f *fakeSession) EnsureSessionPath()                              {}

// fakeCtrl is a *control.Controller stand-in returned by buildController for tests.
// We only exercise hello/list/create through a custom builder that injects sessions
// without boot.Build. createSession expects *control.Controller, so tests for hello
// and protocol compatibility don't need it.

func TestHelloCompatible(t *testing.T) {
	srv := New(Options{Workspace: "/home/u/proj", Version: "test"})
	req := httptest.NewRequest(http.MethodGet, protocol.APIPrefix+"/hello", nil)
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
	srv := New(Options{Workspace: "/w", Resolver: resolver})
	req := httptest.NewRequest(http.MethodGet, protocol.APIPrefix+"/capabilities", nil)
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

// Keep context import used if build paths expand.
var _ = context.Background
