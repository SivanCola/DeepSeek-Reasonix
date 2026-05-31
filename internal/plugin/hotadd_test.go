package plugin

import (
	"context"
	"testing"
	"time"
)

// TestHostAddRemove exercises the hot add/remove path behind `/mcp add` and
// `/mcp remove`: a server connects live into an existing host, its namespaced
// tools surface, a duplicate name is rejected, and removal disconnects it and
// reports the tool prefix to unregister.
func TestHostAddRemove(t *testing.T) {
	srv := mcpHTTPServer(t, false)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewHost()
	defer h.Close()

	spec := Spec{Name: "h", Type: "http", URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer secret"}}
	tools, err := h.Add(ctx, spec)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "mcp__h__greet" {
		t.Fatalf("tools = %v, want [mcp__h__greet]", names(tools))
	}
	if got := h.Servers(); len(got) != 1 || got[0].Name != "h" || got[0].Tools != 1 {
		t.Fatalf("Servers() = %+v, want one server 'h' with 1 tool", got)
	}

	// A second add under the same name is rejected (no duplicate connection).
	if _, err := h.Add(ctx, spec); err == nil {
		t.Error("Add of an already-connected name should error")
	}

	prefix, found := h.Remove("h")
	if !found || prefix != "mcp__h__" {
		t.Fatalf("Remove = (%q, %v), want (\"mcp__h__\", true)", prefix, found)
	}
	if len(h.Servers()) != 0 {
		t.Errorf("server should be gone after Remove, got %+v", h.Servers())
	}
	if len(h.Specs()) != 0 {
		t.Errorf("specs should be gone after Remove, got %+v", h.Specs())
	}
	if _, found := h.Remove("h"); found {
		t.Error("removing an absent server should report not found")
	}
}

func TestHostSpecsReturnsCopies(t *testing.T) {
	srv := mcpHTTPServer(t, false)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h := NewHost()
	defer h.Close()

	spec := Spec{
		Name:    "copy test",
		Type:    "http",
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer secret"},
		Env:     map[string]string{"A": "B"},
	}
	if _, err := h.Add(ctx, spec); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got := h.Specs()
	if len(got) != 1 {
		t.Fatalf("Specs len = %d, want 1", len(got))
	}
	if got[0].Name != spec.Name || got[0].Headers["Authorization"] != "Bearer secret" || got[0].Env["A"] != "B" {
		t.Fatalf("Specs = %+v, want original spec fields", got[0])
	}
	got[0].Headers["Authorization"] = "mutated"
	got[0].Env["A"] = "mutated"
	again := h.Specs()
	if again[0].Headers["Authorization"] != "Bearer secret" || again[0].Env["A"] != "B" {
		t.Fatalf("Specs should return deep copies, got %+v", again[0])
	}
}
