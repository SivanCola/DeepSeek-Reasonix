package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// regConv is a conversation that exposes a tool registry, the way
// *control.Controller does.
type regConv struct {
	*fakeConv
	reg *tool.Registry
}

func (r regConv) ToolRegistry() *tool.Registry { return r.reg }

// stubTool is the minimal Tool the wiring tests register.
type stubTool struct{ name string }

func (s stubTool) Name() string                                             { return s.name }
func (s stubTool) Description() string                                      { return s.name + " desc" }
func (s stubTool) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (s stubTool) Execute(context.Context, json.RawMessage) (string, error) { return "", nil }
func (s stubTool) ReadOnly() bool                                           { return true }

func newRegConv(names ...string) regConv {
	reg := tool.NewRegistry()
	for _, n := range names {
		reg.Add(stubTool{name: n})
	}
	return regConv{fakeConv: newInstantConv(), reg: reg}
}

func exportedNames(reg *tool.Registry) []string {
	schemas := reg.Schemas()
	out := make([]string, 0, len(schemas))
	for _, s := range schemas {
		out = append(out, s.Name)
	}
	return out
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestWireDeferredToolsHoldsBackMCPAndInstallsSearch(t *testing.T) {
	conv := newRegConv("bash", "read_file", "mcp__figma__get_screenshot", "mcp__sheets__read_range")

	if got := wireDeferredTools(conv, DeferredPolicy{}); got != 2 {
		t.Fatalf("wireDeferredTools deferred %d tools, want 2", got)
	}

	exported := exportedNames(conv.reg)
	for _, name := range exported {
		if strings.HasPrefix(name, tool.MCPNamePrefix) {
			t.Fatalf("MCP tool %q still in the provider-visible list: %v", name, exported)
		}
	}
	if !contains(exported, tool.SearchToolName) {
		t.Fatalf("search tool was not installed: %v", exported)
	}
	if !contains(exported, "bash") || !contains(exported, "read_file") {
		t.Fatalf("core coding tools were disturbed: %v", exported)
	}
}

// Skills reach the model through one run_skill dispatcher, so the default
// policy must leave them alone.
func TestWireDeferredToolsLeavesSkillDispatcherInCore(t *testing.T) {
	conv := newRegConv("bash", "run_skill", "read_skill", "mcp__figma__get_screenshot")

	wireDeferredTools(conv, DeferredPolicy{})

	exported := exportedNames(conv.reg)
	if !contains(exported, "run_skill") || !contains(exported, "read_skill") {
		t.Fatalf("skill tools were deferred: %v", exported)
	}
}

// With nothing held back the search tool would be a schema in the prefix with
// nothing to find.
func TestWireDeferredToolsSkipsSearchWhenNothingDeferred(t *testing.T) {
	conv := newRegConv("bash", "read_file")

	if got := wireDeferredTools(conv, DeferredPolicy{}); got != 0 {
		t.Fatalf("wireDeferredTools deferred %d tools, want 0", got)
	}
	if exported := exportedNames(conv.reg); contains(exported, tool.SearchToolName) {
		t.Fatalf("search tool installed with an empty deferred tier: %v", exported)
	}
}

func TestWireDeferredToolsHonoursAnEmptyPolicy(t *testing.T) {
	conv := newRegConv("bash", "mcp__figma__get_screenshot")

	if got := wireDeferredTools(conv, DeferredPolicy{Prefixes: []string{}}); got != 0 {
		t.Fatalf("an explicitly empty policy deferred %d tools, want 0", got)
	}
	if exported := exportedNames(conv.reg); !contains(exported, "mcp__figma__get_screenshot") {
		t.Fatalf("empty policy still deferred an MCP tool: %v", exported)
	}
}

func TestWireDeferredToolsIgnoresConversationsWithoutARegistry(t *testing.T) {
	if got := wireDeferredTools(newInstantConv(), DeferredPolicy{}); got != 0 {
		t.Fatalf("wireDeferredTools = %d for a registry-less conversation, want 0", got)
	}
}

func TestOpenWiresDeferredToolsBeforeTheFirstTurn(t *testing.T) {
	conv := newRegConv("bash", "mcp__figma__get_screenshot")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	exported := exportedNames(conv.reg)
	if contains(exported, "mcp__figma__get_screenshot") {
		t.Fatalf("Open left MCP tools in the provider-visible list: %v", exported)
	}
	if !contains(exported, tool.SearchToolName) {
		t.Fatalf("Open did not install the search tool: %v", exported)
	}
}

func TestFirstTurnAnnouncesTheRosterAndLaterTurnsDoNot(t *testing.T) {
	conv := newRegConv("bash", "mcp__figma__get_screenshot")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	if err := h.Send(context.Background(), "first"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if err := h.Send(context.Background(), "second"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	conv.mu.Lock()
	inputs := append([]string(nil), conv.inputs...)
	conv.mu.Unlock()

	if len(inputs) != 2 {
		t.Fatalf("conversation saw %d turns, want 2", len(inputs))
	}
	if !strings.Contains(inputs[0], "mcp__figma__get_screenshot") {
		t.Fatalf("first turn did not announce the roster:\n%s", inputs[0])
	}
	if !strings.HasSuffix(inputs[0], "first") {
		t.Fatalf("the user's own text was lost from the first turn:\n%s", inputs[0])
	}
	if strings.Contains(inputs[1], "mcp__figma__get_screenshot") {
		t.Fatalf("the roster was repeated on a later turn:\n%s", inputs[1])
	}
	if inputs[1] != "second" {
		t.Fatalf("second turn = %q, want the bare user text", inputs[1])
	}
}

// A server that finishes connecting mid-session is announced on the next turn,
// without repeating what the model already knows.
func TestLateDeferredToolIsAnnouncedOnTheNextTurn(t *testing.T) {
	conv := newRegConv("bash", "mcp__figma__get_screenshot")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	if err := h.Send(context.Background(), "first"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	conv.reg.AddDeferred(stubTool{name: "mcp__slack__post_message"})
	if err := h.Send(context.Background(), "second"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	conv.mu.Lock()
	second := conv.inputs[1]
	conv.mu.Unlock()

	if !strings.Contains(second, "mcp__slack__post_message") {
		t.Fatalf("late tool was never announced:\n%s", second)
	}
	if strings.Contains(second, "mcp__figma__get_screenshot") {
		t.Fatalf("already-announced tool was repeated:\n%s", second)
	}
}

func TestActivatedToolIsNotAnnounced(t *testing.T) {
	conv := newRegConv("bash", "mcp__figma__get_screenshot")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	// The model already loaded it, so it is in the tool list and needs no
	// roster line.
	conv.reg.Activate("mcp__figma__get_screenshot")

	if err := h.Send(context.Background(), "first"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	conv.mu.Lock()
	first := conv.inputs[0]
	conv.mu.Unlock()

	if first != "first" {
		t.Fatalf("an activated tool was announced anyway:\n%s", first)
	}
}

func TestOpenResetsAnnouncementsForTheNewConversation(t *testing.T) {
	first := newRegConv("bash", "mcp__figma__get_screenshot")
	second := newRegConv("bash", "mcp__figma__get_screenshot")
	h := NewHost(builderFor(first, second))

	mustOpen(t, h)
	if err := h.Send(context.Background(), "on first"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	mustOpen(t, h)
	defer h.Close()
	if err := h.Send(context.Background(), "on second"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	second.mu.Lock()
	got := second.inputs[0]
	second.mu.Unlock()

	if !strings.Contains(got, "mcp__figma__get_screenshot") {
		t.Fatalf("a fresh conversation inherited the old announcements:\n%s", got)
	}
}
