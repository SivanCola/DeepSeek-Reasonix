package session

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// Live checks run the real kernel against a real provider. They are skipped
// unless REASONIX_LIVE is set, so the ordinary suite stays hermetic and free.
//
// Run one with a disposable home and workspace so a live check can never read
// or write the developer's real config, credentials, or sessions:
//
//	REASONIX_LIVE=1 REASONIX_HOME=/tmp/rx-home go test -run TestLive ./internal/session/
func liveOrSkip(t *testing.T) {
	t.Helper()

	if os.Getenv("REASONIX_LIVE") == "" {
		t.Skip("live provider check; set REASONIX_LIVE=1 with a disposable REASONIX_HOME to run")
	}
	if os.Getenv("REASONIX_HOME") == "" {
		t.Fatal("refusing to run live against the default Reasonix home; set REASONIX_HOME to a disposable directory")
	}
}

// liveWorkspace returns the workspace a live check runs against.
//
// It defaults to a fresh temp directory, but MCP state — including the schema
// cache that decides whether a server is presented from cache or through a
// one-time connect stub — is keyed by workspace path. A per-run temp directory
// therefore always exercises the cold path. Set REASONIX_LIVE_WORKSPACE to a
// stable throwaway directory to reach the warm one.
func liveWorkspace(t *testing.T) string {
	t.Helper()

	if dir := os.Getenv("REASONIX_LIVE_WORKSPACE"); dir != "" {
		return dir
	}
	return t.TempDir()
}

// waitForMCPHandshake blocks until the registry holds MCP tools beyond a single
// "<server>__connect" placeholder, or the budget expires. It reports the names
// it ended up seeing.
//
// A cold schema cache presents one connect stub and spawns the real handshake in
// the background; on a first run that also means npx downloading the server.
// Closing the conversation before that lands is what keeps the cache cold
// forever, so a live check that wants the warm path has to wait for it once.
func waitForMCPHandshake(t *testing.T, reg *tool.Registry, budget time.Duration) []string {
	t.Helper()

	deadline := time.Now().Add(budget)
	for {
		var mcp []string
		for _, name := range reg.Names() {
			if strings.HasPrefix(name, tool.MCPNamePrefix) {
				mcp = append(mcp, name)
			}
		}
		connectOnly := len(mcp) == 1 && strings.HasSuffix(mcp[0], "__connect")
		if len(mcp) > 0 && !connectOnly {
			return mcp
		}
		if time.Now().After(deadline) {
			return mcp
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// schemaBytes approximates what the exported tool list costs in the
// provider-visible prefix: every name, description, and canonical schema is
// re-sent on each request.
func schemaBytes(reg *tool.Registry) int {
	total := 0
	for _, s := range reg.Schemas() {
		total += len(s.Name) + len(s.Description) + len(s.Parameters)
	}
	return total
}

// registeredSchemaBytes is the counterfactual: what the prefix would cost if
// every registered tool were exported, which is what the host sent before the
// deferred tier existed. Comparing against Schemas() is the only honest measure
// of the saving, since both sides must be read at the same moment — a cold MCP
// handshake keeps registering tools for seconds after assembly.
func registeredSchemaBytes(reg *tool.Registry) int {
	total := 0
	for _, name := range reg.Names() {
		t, ok := reg.Get(name)
		if !ok {
			continue
		}
		total += len(t.Name()) + len(t.Description()) + len(t.Schema())
	}
	return total
}

// collector records frames off the kernel's sink goroutines.
type collector struct {
	mu     sync.Mutex
	frames []Frame
	text   string
}

func (c *collector) sink(e event.Event) {
	frame, ok := TranslateEvent(e)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames = append(c.frames, frame)
	if frame.Kind == "text" {
		c.text += frame.Text
	}
}

func (c *collector) snapshot() ([]Frame, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Frame(nil), c.frames...), c.text
}

// TestLiveTurnReachesTheProvider is the end-to-end check the unit suite cannot
// give: a real session assembles, a real turn runs, and the frames the UI would
// render actually arrive.
func TestLiveTurnReachesTheProvider(t *testing.T) {
	liveOrSkip(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var c collector
	h := NewHost(nil)
	defer h.Close()

	// A throwaway workspace: the agent must not be pointed at a real checkout.
	if err := h.Open(ctx, Options{WorkspaceRoot: liveWorkspace(t), Sink: event.FuncSink(c.sink)}); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if err := h.Send(ctx, "Reply with exactly: OK"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	frames, text := c.snapshot()
	if text == "" {
		t.Fatalf("no answer text arrived; frames = %+v", frames)
	}
	t.Logf("answer: %q", text)

	var sawUsage bool
	for _, f := range frames {
		switch f.Kind {
		case "usage":
			sawUsage = true
			if f.CacheKnown {
				t.Logf("session cache hit rate: %.1f%%", f.CacheHitRate*100)
			} else {
				t.Log("session cache hit rate: unreported by this provider")
			}
		case "turn_done":
			// The kernel does not publish TurnDone for synchronous runs, which
			// is why the shell synthesises it from Send's return. If that ever
			// changes, the shell would close the turn twice.
			t.Error("the kernel published TurnDone; session.TurnDoneFrame now duplicates it")
		}
	}
	if !sawUsage {
		t.Error("no usage frame arrived, so the UI would never show a cache rate")
	}
}

// TestLiveDeferredWiring reports what the deferred tier actually did to a real
// assembled session. With no MCP servers configured it correctly defers
// nothing; with servers configured it should hold their tools back and install
// the search tool.
func TestLiveDeferredWiring(t *testing.T) {
	liveOrSkip(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	conv, err := KernelBuilder(ctx, Options{WorkspaceRoot: liveWorkspace(t)})
	if err != nil {
		t.Fatalf("KernelBuilder failed: %v", err)
	}
	defer conv.Close()

	reg := registryOf(conv)
	if reg == nil {
		t.Fatal("a real controller should expose its tool registry")
	}
	// Production order: wire at assembly time, before the background MCP
	// handshake lands. On a cold schema cache only a connect placeholder exists
	// at this point, so this is what proves the namespace claim covers the real
	// tools that arrive seconds later.
	atWiring := len(reg.Schemas())
	deferred := wireDeferredTools(conv, DeferredPolicy{})

	seen := waitForMCPHandshake(t, reg, 3*time.Minute)
	t.Logf("tools exported at wiring time: %d; MCP tools registered once the handshake landed: %d", atWiring, len(seen))
	t.Logf("deferred at wiring: %d; deferred roster now: %d", deferred, len(reg.DeferredRoster()))

	after := reg.Schemas()
	exported, registered := schemaBytes(reg), registeredSchemaBytes(reg)

	t.Logf("registered tools: %d (%d schema bytes) — what a host without the tier would send",
		len(reg.Names()), registered)
	t.Logf("exported tools:   %d (%d schema bytes)", len(after), exported)
	if registered > 0 {
		saved := registered - exported
		t.Logf("prefix saving per request: %d bytes (%.1f%%), roughly %d tokens at 4 bytes/token",
			saved, float64(saved)/float64(registered)*100, saved/4)
	}
	for _, entry := range reg.DeferredRoster() {
		t.Logf("  deferred: %s", entry.Name)
	}

	if deferred == 0 {
		t.Log("no MCP tools in this configuration, so nothing was deferred and no search tool was installed")
		return
	}

	var sawSearch bool
	for _, s := range after {
		if s.Name == tool.SearchToolName {
			sawSearch = true
		}
		if len(s.Name) >= len(tool.MCPNamePrefix) && s.Name[:len(tool.MCPNamePrefix)] == tool.MCPNamePrefix {
			t.Errorf("MCP tool %q survived in the exported list", s.Name)
		}
	}
	if !sawSearch {
		t.Error("tools were deferred but the search tool was not installed")
	}
}
