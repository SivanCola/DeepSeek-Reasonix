package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newSearchFixture(t *testing.T) (*Registry, Tool) {
	t.Helper()

	r := NewRegistry()
	r.Add(stubTool{name: "bash"})
	r.AddDeferred(descTool{name: "mcp__figma__get_design_context", desc: "Retrieve design context for a Figma node."})
	r.AddDeferred(descTool{name: "mcp__figma__get_screenshot", desc: "Capture a screenshot of a Figma frame."})
	r.AddDeferred(descTool{name: "mcp__sheets__read_range", desc: "Read a spreadsheet range."})
	r.AddDeferred(descTool{name: "mcp__slack__post_message", desc: "Post a message to a Slack channel."})
	return r, NewSearchTool(r)
}

// descTool is a stub whose description is distinct from its name, so ranking
// tests can tell name matches from description matches.
type descTool struct {
	name string
	desc string
}

func (d descTool) Name() string                                             { return d.name }
func (d descTool) Description() string                                      { return d.desc }
func (d descTool) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (d descTool) Execute(context.Context, json.RawMessage) (string, error) { return "", nil }
func (d descTool) ReadOnly() bool                                           { return true }

func runSearch(t *testing.T, s Tool, args string) string {
	t.Helper()

	out, err := s.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Execute(%s) failed: %v", args, err)
	}
	return out
}

func TestSearchSelectLoadsExactNames(t *testing.T) {
	r, s := newSearchFixture(t)

	out := runSearch(t, s, `{"query":"select:mcp__sheets__read_range,mcp__slack__post_message"}`)
	if !strings.Contains(out, "mcp__sheets__read_range") || !strings.Contains(out, "mcp__slack__post_message") {
		t.Fatalf("select result missing the requested tools:\n%s", out)
	}

	// select: loads every name given, ignoring the default result cap.
	if got, want := r.ActivatedNames(), []string{"mcp__sheets__read_range", "mcp__slack__post_message"}; len(got) != len(want) {
		t.Fatalf("ActivatedNames() = %v, want %v", got, want)
	}
}

func TestSearchActivationAppendsToSchemasTail(t *testing.T) {
	r, s := newSearchFixture(t)
	before := schemaNames(t, r)

	runSearch(t, s, `{"query":"select:mcp__figma__get_screenshot"}`)

	after := schemaNames(t, r)
	if len(after) != len(before)+1 {
		t.Fatalf("Schemas() went from %v to %v, want exactly one addition", before, after)
	}
	if after[len(after)-1] != "mcp__figma__get_screenshot" {
		t.Fatalf("activated tool is not at the tail: %v", after)
	}
	if got := after[:len(before)]; !equalStrings(got, before) {
		t.Fatalf("search rewrote the cached prefix: %v became %v", before, got)
	}
}

func TestSearchRanksNameMatchesAboveDescriptionMatches(t *testing.T) {
	r := NewRegistry()
	r.AddDeferred(descTool{name: "unrelated_helper", desc: "Mentions figma only in passing."})
	r.AddDeferred(descTool{name: "mcp__figma__get_screenshot", desc: "Capture a frame."})
	s := NewSearchTool(r)

	runSearch(t, s, `{"query":"figma","max_results":1}`)

	activated := r.ActivatedNames()
	if len(activated) != 1 || activated[0] != "mcp__figma__get_screenshot" {
		t.Fatalf("ActivatedNames() = %v, want the name match to win", activated)
	}
}

func TestSearchRequiredTermFiltersByName(t *testing.T) {
	r, s := newSearchFixture(t)

	runSearch(t, s, `{"query":"+figma screenshot design"}`)

	for _, name := range r.ActivatedNames() {
		if !strings.Contains(name, "figma") {
			t.Fatalf("required term ignored: %q activated", name)
		}
	}
	if len(r.ActivatedNames()) != 2 {
		t.Fatalf("ActivatedNames() = %v, want both figma tools", r.ActivatedNames())
	}
}

func TestSearchRespectsMaxResults(t *testing.T) {
	r, s := newSearchFixture(t)

	runSearch(t, s, `{"query":"mcp","max_results":2}`)

	if got := r.ActivatedNames(); len(got) != 2 {
		t.Fatalf("ActivatedNames() = %v, want 2", got)
	}
}

// An unavailable capability must be reported without being activated: it cannot
// run, so its schema would occupy prefix bytes for nothing.
func TestSearchReportsUnavailableWithoutActivating(t *testing.T) {
	r, s := newSearchFixture(t)
	r.PinPrefix("mcp__slack__", "slack MCP server disconnected")

	out := runSearch(t, s, `{"query":"select:mcp__slack__post_message"}`)

	if !strings.Contains(out, "slack MCP server disconnected") {
		t.Fatalf("result did not explain the unavailability:\n%s", out)
	}
	if got := r.ActivatedNames(); len(got) != 0 {
		t.Fatalf("ActivatedNames() = %v, want nothing activated", got)
	}
	for _, name := range schemaNames(t, r) {
		if name == "mcp__slack__post_message" {
			t.Fatal("unavailable tool leaked into Schemas()")
		}
	}
}

func TestSearchReportsNoMatchWithoutActivating(t *testing.T) {
	r, s := newSearchFixture(t)

	out := runSearch(t, s, `{"query":"kubernetes"}`)

	if !strings.Contains(out, "No deferred tool matched") {
		t.Fatalf("unexpected miss message:\n%s", out)
	}
	if got := r.ActivatedNames(); len(got) != 0 {
		t.Fatalf("a miss activated %v", got)
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	_, s := newSearchFixture(t)

	if _, err := s.Execute(context.Background(), json.RawMessage(`{"query":"  "}`)); err == nil {
		t.Fatal("empty query should be rejected")
	}
}

func TestSearchIsInertWithNothingDeferred(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "bash"})
	s := NewSearchTool(r)

	out := runSearch(t, s, `{"query":"anything"}`)
	if !strings.Contains(out, "No tools are deferred") {
		t.Fatalf("unexpected message with an empty deferred tier:\n%s", out)
	}
}

// The search tool's own description sits in the provider prefix, so it must not
// name the tools it can find — a late MCP connection would rewrite it.
func TestSearchDescriptionIsIndependentOfRoster(t *testing.T) {
	empty := NewSearchTool(NewRegistry())
	populated, _ := newSearchFixture(t)

	if empty.Description() != NewSearchTool(populated).Description() {
		t.Fatal("search description varies with the roster; it would churn the cached prefix")
	}
}

func TestRenderDeferredRosterListsOnlyPendingTools(t *testing.T) {
	r, s := newSearchFixture(t)
	r.PinPrefix("mcp__slack__", "server down")
	runSearch(t, s, `{"query":"select:mcp__sheets__read_range"}`)

	msg := RenderDeferredRoster(r.DeferredRoster())

	if strings.Contains(msg, "mcp__sheets__read_range") {
		t.Fatalf("roster still advertises an already-loaded tool:\n%s", msg)
	}
	if !strings.Contains(msg, "mcp__figma__get_screenshot") {
		t.Fatalf("roster dropped a pending tool:\n%s", msg)
	}
	if !strings.Contains(msg, "[unavailable: server down]") {
		t.Fatalf("roster hid an unavailable tool's reason:\n%s", msg)
	}
	if !strings.Contains(msg, SearchToolName) {
		t.Fatalf("roster does not tell the model how to load a tool:\n%s", msg)
	}
}

func TestRenderDeferredRosterEmptyWhenNothingPending(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "bash"})

	if msg := RenderDeferredRoster(r.DeferredRoster()); msg != "" {
		t.Fatalf("RenderDeferredRoster() = %q, want empty", msg)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
