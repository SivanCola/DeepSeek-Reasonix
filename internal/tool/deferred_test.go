package tool

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func schemaNames(t *testing.T, r *Registry) []string {
	t.Helper()

	schemas := r.Schemas()
	names := make([]string, 0, len(schemas))
	for _, s := range schemas {
		names = append(names, s.Name)
	}
	return names
}

// A registry nobody defers anything in must behave exactly as it did before the
// deferred tier existed: every tool exported, sorted by name. The CLI and the
// full desktop depend on this, and so do their prefix goldens.
func TestDeferredTierIsInertWhenUnused(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "write_file"})
	r.Add(stubTool{name: "bash"})
	r.Add(stubTool{name: "read_file"})

	want := []string{"bash", "read_file", "write_file"}
	if got := schemaNames(t, r); !reflect.DeepEqual(got, want) {
		t.Fatalf("Schemas() = %v, want %v", got, want)
	}
}

func TestAddDeferredWithholdsFromSchemas(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "bash"})
	r.AddDeferred(stubTool{name: "mcp__figma__get_design_context", server: "figma", raw: "get_design_context"})

	if got, want := schemaNames(t, r), []string{"bash"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deferred tool leaked into Schemas(): got %v, want %v", got, want)
	}

	// Withheld from the provider, but still fully registered host-side: the
	// host can resolve and execute it, which is what lets a search tool
	// activate it by name later.
	if _, ok := r.Get("mcp__figma__get_design_context"); !ok {
		t.Fatal("deferred tool should still be resolvable via Get")
	}
	if !r.IsDeferred("mcp__figma__get_design_context") {
		t.Fatal("IsDeferred should report true for a withheld tool")
	}
}

// The load-bearing cache invariant. Tool schemas sit at the front of the
// provider-visible prefix, so a newly activated tool must land at the tail even
// when its name would sort first — splicing it into sorted position shifts
// every schema after it and invalidates the cached prefix from that point.
func TestActivateAppendsToTailPreservingCorePrefix(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "bash"})
	r.Add(stubTool{name: "read_file"})
	r.Add(stubTool{name: "write_file"})
	// Sorts before every core tool above, precisely to catch a sorted splice.
	r.AddDeferred(stubTool{name: "aaa_deferred"})

	before := schemaNames(t, r)

	moved := r.Activate("aaa_deferred")
	if want := []string{"aaa_deferred"}; !reflect.DeepEqual(moved, want) {
		t.Fatalf("Activate returned %v, want %v", moved, want)
	}

	after := schemaNames(t, r)
	want := []string{"bash", "read_file", "write_file", "aaa_deferred"}
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("Schemas() = %v, want activated tool appended at the tail: %v", after, want)
	}

	// Growth must be strictly additive: everything cached before the
	// activation still occupies the same position afterwards.
	if !reflect.DeepEqual(after[:len(before)], before) {
		t.Fatalf("activation rewrote the cached prefix: %v became %v", before, after[:len(before)])
	}
}

// Defer is the session-assembly counterpart to AddDeferred: it demotes core
// tools on purpose, which is safe only before the first provider request.
func TestDeferDemotesRegisteredCoreTools(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "bash"})
	r.Add(stubTool{name: "mcp__figma__get_screenshot"})
	r.Add(stubTool{name: "mcp__sheets__read_range"})

	moved := r.Defer("mcp__figma__get_screenshot", "mcp__sheets__read_range", "never_registered")
	if len(moved) != 2 {
		t.Fatalf("Defer moved %v, want the two registered MCP tools", moved)
	}

	if got, want := schemaNames(t, r), []string{"bash"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Schemas() = %v, want only the core tool", got)
	}
	// Deferred, not unregistered: the host can still resolve and activate them.
	if _, ok := r.Get("mcp__figma__get_screenshot"); !ok {
		t.Fatal("Defer unregistered a tool instead of withholding it")
	}
	if len(r.DeferredRoster()) != 2 {
		t.Fatalf("roster = %v, want both deferred tools", r.DeferredRoster())
	}
}

func TestDeferIsIdempotent(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "mcp__figma__get_screenshot"})

	if moved := r.Defer("mcp__figma__get_screenshot"); len(moved) != 1 {
		t.Fatalf("first Defer moved %v, want one name", moved)
	}
	if moved := r.Defer("mcp__figma__get_screenshot"); len(moved) != 0 {
		t.Fatalf("second Defer moved %v, want nothing", moved)
	}
}

// Deferring an activated tool must not leave it in the exported tail.
func TestDeferAfterActivationDoesNotDuplicateInSchemas(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "bash"})
	r.AddDeferred(stubTool{name: "mcp__figma__get_screenshot"})
	r.Activate("mcp__figma__get_screenshot")

	// Already deferred, so this is a no-op rather than a second demotion.
	if moved := r.Defer("mcp__figma__get_screenshot"); len(moved) != 0 {
		t.Fatalf("Defer moved %v, want nothing for an already-deferred tool", moved)
	}
	want := []string{"bash", "mcp__figma__get_screenshot"}
	if got := schemaNames(t, r); !reflect.DeepEqual(got, want) {
		t.Fatalf("Schemas() = %v, want %v", got, want)
	}
}

func TestActivateIsIdempotentAndIgnoresUnknownNames(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "bash"})
	r.AddDeferred(stubTool{name: "deferred_one"})

	if moved := r.Activate("deferred_one"); len(moved) != 1 {
		t.Fatalf("first Activate moved %v, want one name", moved)
	}
	// Re-activating, activating a core tool, and activating a name that was
	// never registered must all be no-ops — a duplicate entry in the tail
	// would send the same schema twice.
	if moved := r.Activate("deferred_one", "bash", "never_registered"); len(moved) != 0 {
		t.Fatalf("second Activate moved %v, want nothing", moved)
	}
	want := []string{"bash", "deferred_one"}
	if got := schemaNames(t, r); !reflect.DeepEqual(got, want) {
		t.Fatalf("Schemas() = %v, want %v", got, want)
	}
}

// PinPrefix is the cache-safe alternative to RemovePrefix for a disconnected
// MCP server: the exported schemas must not move by a single byte.
func TestPinPrefixKeepsSchemasIdenticalWhileRemovePrefixDoesNot(t *testing.T) {
	build := func() *Registry {
		r := NewRegistry()
		r.Add(stubTool{name: "bash"})
		r.Add(stubTool{name: "mcp__figma__get_design_context", server: "figma", raw: "get_design_context"})
		r.Add(stubTool{name: "write_file"})
		return r
	}

	pinned := build()
	baseline := pinned.Schemas()

	if n := pinned.PinPrefix("mcp__figma__", "figma MCP server disconnected"); n != 1 {
		t.Fatalf("PinPrefix marked %d tools, want 1", n)
	}
	if got := pinned.Schemas(); !reflect.DeepEqual(got, baseline) {
		t.Fatalf("PinPrefix changed the provider-visible prefix:\n got %v\nwant %v", got, baseline)
	}

	reason, unavailable := pinned.Availability("mcp__figma__get_design_context")
	if !unavailable || reason != "figma MCP server disconnected" {
		t.Fatalf("Availability = (%q, %v), want the pinned reason", reason, unavailable)
	}

	// Contrast: the existing removal path drops the name outright, which is
	// exactly the prefix churn the pinned roster avoids.
	removed := build()
	removed.RemovePrefix("mcp__figma__")
	if got := removed.Schemas(); reflect.DeepEqual(got, baseline) {
		t.Fatal("RemovePrefix should change the exported schemas; test no longer proves the contrast")
	}
}

func TestReRegisteringClearsPinnedUnavailability(t *testing.T) {
	r := NewRegistry()
	tool := stubTool{name: "mcp__figma__get_design_context", server: "figma", raw: "get_design_context"}
	r.Add(tool)
	r.PinPrefix("mcp__figma__", "disconnected")

	// Reconnecting re-registers the tool with a fresh handle.
	r.Add(tool)
	if reason, unavailable := r.Availability(tool.name); unavailable {
		t.Fatalf("reconnect left the tool pinned: %q", reason)
	}
}

// An MCP reconnect that re-registers through the deferred path must not yank a
// tool the provider has already been told about.
func TestAddDeferredDoesNotDemoteACoreTool(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "bash"})

	r.AddDeferred(stubTool{name: "bash"})

	if r.IsDeferred("bash") {
		t.Fatal("AddDeferred demoted an already-exported core tool")
	}
	if got, want := schemaNames(t, r), []string{"bash"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Schemas() = %v, want %v", got, want)
	}
}

// blockingDescTool parks a caller inside a tool callback so a test can prove
// the registry lock is not held across it.
type blockingDescTool struct {
	name    string
	entered chan<- struct{}
	release <-chan struct{}
}

func (b *blockingDescTool) Name() string { return b.name }
func (b *blockingDescTool) Description() string {
	close(b.entered)
	<-b.release
	return "blocked"
}
func (b *blockingDescTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (b *blockingDescTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (b *blockingDescTool) ReadOnly() bool { return true }

// Deterministic regression for the AB-BA shape ContractEntries was already
// bitten by: a lazy MCP placeholder takes its spawn mutex inside tool callbacks
// while that spawn's swap path needs the registry write lock. With the roster
// parked inside Description(), a registry writer must still complete.
func TestDeferredRosterDoesNotHoldRegistryLockAcrossToolCallbacks(t *testing.T) {
	reg := NewRegistry()
	entered := make(chan struct{})
	release := make(chan struct{})
	reg.AddDeferred(&blockingDescTool{name: "blocking_deferred", entered: entered, release: release})

	rosterCh := make(chan []DeferredEntry, 1)
	go func() { rosterCh <- reg.DeferredRoster() }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("DeferredRoster never reached the tool callback")
	}

	added := make(chan struct{})
	go func() {
		reg.Add(stubTool{name: "bash"})
		close(added)
	}()
	select {
	case <-added:
	case <-time.After(5 * time.Second):
		t.Fatal("Add blocked: DeferredRoster holds the registry lock across a tool callback")
	}

	close(release)
	select {
	case <-rosterCh:
	case <-time.After(5 * time.Second):
		t.Fatal("DeferredRoster did not return after release")
	}
}

func TestDeferredRosterReportsStateInNameOrder(t *testing.T) {
	r := NewRegistry()
	r.Add(stubTool{name: "bash"})
	r.AddDeferred(stubTool{name: "zeta_tool"})
	r.AddDeferred(stubTool{name: "alpha_tool"})
	r.Activate("zeta_tool")
	r.PinPrefix("alpha_", "server down")

	roster := r.DeferredRoster()
	if len(roster) != 2 {
		t.Fatalf("roster = %v, want the two deferred tools only", roster)
	}
	if roster[0].Name != "alpha_tool" || roster[1].Name != "zeta_tool" {
		t.Fatalf("roster not in name order: %v", roster)
	}
	if roster[0].Activated || roster[0].Unavailable != "server down" {
		t.Fatalf("alpha_tool entry = %+v, want inactive and unavailable", roster[0])
	}
	if !roster[1].Activated || roster[1].Unavailable != "" {
		t.Fatalf("zeta_tool entry = %+v, want activated and available", roster[1])
	}
	if roster[0].Description != "alpha_tool desc" {
		t.Fatalf("roster dropped the description used for matching: %+v", roster[0])
	}
}
