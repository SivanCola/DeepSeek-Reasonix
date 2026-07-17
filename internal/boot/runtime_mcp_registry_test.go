package boot

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/tool"
)

type runtimeMCPTestTool struct{ name string }

func (t *runtimeMCPTestTool) Name() string            { return t.name }
func (t *runtimeMCPTestTool) Description() string     { return t.name }
func (t *runtimeMCPTestTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *runtimeMCPTestTool) ReadOnly() bool          { return true }
func (t *runtimeMCPTestTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}

func TestRuntimeMCPRegistryPublishesPostSnapshotSwapToExecutionAndLegacy(t *testing.T) {
	fallback := tool.NewRegistry()
	fallback.Add(&runtimeMCPTestTool{name: "mcp__svc__connect"})
	router := newRuntimeMCPRegistry(fallback)

	var legacy, execution *tool.Registry
	router.bindSnapshot(func() []*tool.Registry {
		legacy = fallback.Clone()
		execution = legacy.Clone()
		return []*tool.Registry{execution, legacy}
	})

	// Mirror plugin.lazySpawn.trySwap: remove the cache-miss placeholder, then
	// publish the real tools through the same writer.
	router.RemovePrefix("mcp__svc__")
	router.Add(&runtimeMCPTestTool{name: "mcp__svc__search"})

	for name, reg := range map[string]*tool.Registry{"execution": execution, "legacy": legacy} {
		if _, ok := reg.Get("mcp__svc__connect"); ok {
			t.Fatalf("%s kept cache-miss placeholder after swap", name)
		}
		if _, ok := reg.Get("mcp__svc__search"); !ok {
			t.Fatalf("%s missed real tool after swap", name)
		}
	}
	if _, ok := fallback.Get("mcp__svc__search"); ok {
		t.Fatal("post-snapshot swap wrote to abandoned bootstrap registry")
	}
}

func TestRuntimeMCPRegistryExplicitReplaceResumesBothTargets(t *testing.T) {
	legacy := tool.NewRegistry()
	execution := tool.NewRegistry()
	router := newRuntimeMCPRegistry(tool.NewRegistry())
	router.bindSnapshot(func() []*tool.Registry { return []*tool.Registry{execution, legacy} })

	for _, reg := range []*tool.Registry{execution, legacy} {
		reg.SuspendPrefix("mcp__svc__")
	}
	names := router.replacePrefix("mcp__svc__", []tool.Tool{
		&runtimeMCPTestTool{name: "mcp__svc__z"},
		&runtimeMCPTestTool{name: "mcp__svc__a"},
	})
	if got, want := names, []string{"mcp__svc__a", "mcp__svc__z"}; !sameStrings(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for label, reg := range map[string]*tool.Registry{"execution": execution, "legacy": legacy} {
		if _, ok := reg.Get("mcp__svc__a"); !ok {
			t.Fatalf("%s remained suspended after explicit reconnect", label)
		}
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
