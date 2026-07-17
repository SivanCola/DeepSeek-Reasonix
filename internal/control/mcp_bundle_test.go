package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
	"reasonix/internal/tool"
)

// mockMCPHTTPServer serves a minimal MCP HTTP surface with one tool.
func mockMCPHTTPServer(t *testing.T, toolName string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-03-26",
				"serverInfo":      map[string]any{"name": "mock", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        toolName,
				"description": "mock tool",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
			}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
}

func testBundleSurfaces() (legacy, exec, classic, hash *tool.Registry, bundle *agent.RegistryBundle) {
	legacy = tool.NewRegistry()
	legacy.Add(&resumeSurfaceTool{name: "read_file"})
	legacy.Add(&resumeSurfaceTool{name: "bash"})

	exec = tool.NewRegistry()
	exec.Add(&resumeSurfaceTool{name: "read_file"})
	exec.Add(&resumeSurfaceTool{name: "bash"})
	exec.Add(&resumeSurfaceTool{name: "grep"})
	exec.Add(&resumeSurfaceTool{name: "use_capability"})
	exec.Add(&resumeSurfaceTool{name: "search_capabilities"})
	exec.Add(&resumeSurfaceTool{name: "hashline_read"})

	classic = tool.NewRegistry()
	classic.Add(&resumeSurfaceTool{name: "read_file"})
	classic.Add(&resumeSurfaceTool{name: "bash"})
	classic.Add(&resumeSurfaceTool{name: "use_capability"})
	classic.Add(&resumeSurfaceTool{name: "search_capabilities"})

	hash = tool.NewRegistry()
	hash.Add(&resumeSurfaceTool{name: "bash"})
	hash.Add(&resumeSurfaceTool{name: "hashline_read"})
	hash.Add(&resumeSurfaceTool{name: "use_capability"})
	hash.Add(&resumeSurfaceTool{name: "search_capabilities"})

	bundle = &agent.RegistryBundle{
		Legacy:             legacy,
		Execution:          exec,
		CapabilityClassic:  classic,
		CapabilityHashline: hash,
	}
	return
}

func TestV2ConnectMCPDoesNotPolluteCapabilityProviderSchema(t *testing.T) {
	server := mockMCPHTTPServer(t, "search")
	defer server.Close()

	legacy, exec, classic, hash, bundle := testBundleSurfaces()
	host := plugin.NewHost()
	defer host.Close()

	execAgent := agent.New(&resumeFakeProv{}, classic, agent.NewSession("sys"), agent.Options{}, event.Discard)
	_ = execAgent.SetRuntimeContract(agent.DefaultRuntimeContract())
	execAgent.SetRegistryBundle(bundle)
	ctrl := New(Options{
		Host:           host,
		Registry:       classic,
		RegistryBundle: bundle,
		Executor:       execAgent,
		Runner:         execAgent,
	})

	beforeClassic := classic.Schemas()
	beforeHash := hash.Schemas()

	entry := config.PluginEntry{Name: "mocksvc", Type: "http", URL: server.URL}
	if _, err := ctrl.ConnectMCPServer(entry); err != nil {
		t.Fatalf("ConnectMCPServer: %v", err)
	}
	mcpName := "mcp__mocksvc__search"
	if _, ok := classic.Get(mcpName); ok {
		t.Fatalf("capability-classic polluted with %s; tools=%v", mcpName, classic.Names())
	}
	if _, ok := hash.Get(mcpName); ok {
		t.Fatalf("capability-hashline polluted with %s", mcpName)
	}
	if _, ok := exec.Get(mcpName); !ok {
		t.Fatalf("execution missing %s; tools=%v", mcpName, exec.Names())
	}
	if _, ok := legacy.Get(mcpName); !ok {
		t.Fatalf("legacy mirror missing %s", mcpName)
	}
	if after := classic.Schemas(); !reflect.DeepEqual(beforeClassic, after) {
		t.Fatalf("classic provider schema/order changed: before=%v after=%v", beforeClassic, after)
	}
	if after := hash.Schemas(); !reflect.DeepEqual(beforeHash, after) {
		t.Fatalf("hashline provider schema/order changed: before=%v after=%v", beforeHash, after)
	}
}

func TestMCPConnectSurvivesLegacyToV2Resume(t *testing.T) {
	server := mockMCPHTTPServer(t, "ping")
	defer server.Close()

	legacy, exec, classic, _, bundle := testBundleSurfaces()
	host := plugin.NewHost()
	defer host.Close()

	execAgent := agent.New(&resumeFakeProv{}, legacy, agent.NewSession("sys"), agent.Options{}, event.Discard)
	_ = execAgent.SetRuntimeContract(agent.LegacyDefaultRuntimeContract())
	execAgent.SetRegistryBundle(bundle)
	ctrl := New(Options{Host: host, Registry: legacy, RegistryBundle: bundle, Executor: execAgent, Runner: execAgent})

	entry := config.PluginEntry{Name: "svc", Type: "http", URL: server.URL}
	if _, err := ctrl.ConnectMCPServer(entry); err != nil {
		t.Fatalf("ConnectMCPServer: %v", err)
	}
	mcpName := "mcp__svc__ping"
	if _, ok := legacy.Get(mcpName); !ok {
		t.Fatal("legacy missing MCP after connect")
	}

	v2 := agent.DefaultRuntimeContract()
	if err := execAgent.ApplyRuntimeContract(v2, bundle); err != nil {
		t.Fatal(err)
	}
	if _, ok := classic.Get(mcpName); ok {
		t.Fatal("v2 classic surface must not gain MCP tools on resume")
	}
	if _, ok := exec.Get(mcpName); !ok {
		t.Fatal("execution lost MCP tools after resume to v2")
	}
	if !ctrl.DisconnectMCPServer("svc") {
		t.Fatal("DisconnectMCPServer returned false")
	}
	if _, ok := exec.Get(mcpName); ok {
		t.Fatal("execution still has MCP after disconnect")
	}
	if _, ok := legacy.Get(mcpName); ok {
		t.Fatal("legacy still has MCP after disconnect")
	}
}

func TestMCPConnectSurvivesV2ToLegacyResume(t *testing.T) {
	server := mockMCPHTTPServer(t, "echo")
	defer server.Close()

	legacy, exec, classic, _, bundle := testBundleSurfaces()
	host := plugin.NewHost()
	defer host.Close()

	execAgent := agent.New(&resumeFakeProv{}, classic, agent.NewSession("sys"), agent.Options{}, event.Discard)
	_ = execAgent.SetRuntimeContract(agent.DefaultRuntimeContract())
	execAgent.SetRegistryBundle(bundle)
	ctrl := New(Options{Host: host, Registry: classic, RegistryBundle: bundle, Executor: execAgent, Runner: execAgent})

	entry := config.PluginEntry{Name: "echo", Type: "http", URL: server.URL}
	if _, err := ctrl.ConnectMCPServer(entry); err != nil {
		t.Fatalf("ConnectMCPServer: %v", err)
	}
	mcpName := "mcp__echo__echo"
	if _, ok := classic.Get(mcpName); ok {
		t.Fatal("v2 provider polluted on connect")
	}
	if _, ok := exec.Get(mcpName); !ok {
		t.Fatal("execution missing MCP after connect")
	}
	if _, ok := legacy.Get(mcpName); !ok {
		t.Fatal("legacy missing MCP after connect")
	}

	if err := execAgent.ApplyRuntimeContract(agent.LegacyDefaultRuntimeContract(), bundle); err != nil {
		t.Fatal(err)
	}
	if _, ok := legacy.Get(mcpName); !ok {
		t.Fatal("legacy lost MCP after resume from v2")
	}
	// Disconnect still clears both MCP write targets after surface switch.
	if !ctrl.DisconnectMCPServer("echo") {
		t.Fatal("disconnect failed")
	}
	if _, ok := exec.Get(mcpName); ok {
		t.Fatal("execution still has tool after disconnect post-resume")
	}
	if _, ok := legacy.Get(mcpName); ok {
		t.Fatal("legacy still has tool after disconnect post-resume")
	}
}

func TestMCPSuspendBlocksLateAdd(t *testing.T) {
	legacy, exec, classic, _, bundle := testBundleSurfaces()
	host := plugin.NewHost()
	defer host.Close()
	ctrl := New(Options{Host: host, Registry: classic, RegistryBundle: bundle})

	server := mockMCPHTTPServer(t, "t")
	defer server.Close()
	entry := config.PluginEntry{Name: "late", Type: "http", URL: server.URL}
	if _, err := ctrl.ConnectMCPServer(entry); err != nil {
		t.Fatalf("connect: %v", err)
	}
	mcpName := "mcp__late__t"
	if _, ok := exec.Get(mcpName); !ok {
		t.Fatal("expected tool after connect")
	}
	if !ctrl.UnregisterMCPServerTools("late") {
		t.Fatal("UnregisterMCPServerTools returned false")
	}
	if _, ok := exec.Get(mcpName); ok {
		t.Fatal("tool still present after suspend")
	}
	if _, ok := legacy.Get(mcpName); ok {
		t.Fatal("legacy tool still present after suspend")
	}
	// Late Add with suspended prefix must be rejected by Registry.Add.
	exec.Add(&resumeSurfaceTool{name: mcpName})
	if _, ok := exec.Get(mcpName); ok {
		t.Fatal("suspended prefix allowed late Add to revive tool")
	}
	legacy.Add(&resumeSurfaceTool{name: mcpName})
	if _, ok := legacy.Get(mcpName); ok {
		t.Fatal("suspended prefix allowed late Add on legacy")
	}
	// Capability surface must never have gained the tool.
	if _, ok := classic.Get(mcpName); ok {
		t.Fatal("classic surface had MCP tool")
	}
}

func TestDynamicBuiltinRegistrationDoesNotPolluteCapabilityProviderSchema(t *testing.T) {
	legacy, exec, classic, hash, bundle := testBundleSurfaces()
	ctrl := New(Options{Registry: classic, RegistryBundle: bundle})

	beforeClassic := classic.Schemas()
	beforeHash := hash.Schemas()
	ctrl.mcp.registerTool(&resumeSurfaceTool{name: "slash_command"})

	for label, reg := range map[string]*tool.Registry{"execution": exec, "legacy": legacy} {
		if _, ok := reg.Get("slash_command"); !ok {
			t.Fatalf("%s missing dynamic built-in", label)
		}
	}
	if after := classic.Schemas(); !reflect.DeepEqual(beforeClassic, after) {
		t.Fatalf("classic provider schema/order changed: before=%v after=%v", beforeClassic, after)
	}
	if after := hash.Schemas(); !reflect.DeepEqual(beforeHash, after) {
		t.Fatalf("hashline provider schema/order changed: before=%v after=%v", beforeHash, after)
	}
}
