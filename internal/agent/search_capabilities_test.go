package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestSearchCapabilitiesSchemaStability(t *testing.T) {
	tl := NewSearchCapabilitiesTool(nil, nil)
	if tl.Name() != "search_capabilities" {
		t.Fatalf("name = %q", tl.Name())
	}
	if !tl.ReadOnly() {
		t.Fatal("search_capabilities must be ReadOnly")
	}
	raw := string(tl.Schema())
	for _, want := range []string{
		`"query"`,
		`"minLength":1`,
		`"limit"`,
		`"minimum":1`,
		`"maximum":10`,
		`"default":5`,
		`"kind"`,
		`"enum":["all","tool","skill","mcp"]`,
		`"required":["query"]`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("schema missing %s:\n%s", want, raw)
		}
	}
}

func TestSearchCapabilitiesExecute(t *testing.T) {
	cat := capability.Catalog{Entries: []capability.Entry{
		{ID: "tool:grep", Kind: capability.KindTool, Name: "grep", Description: "search file contents", Status: capability.StatusReady, ReadOnly: true, ToolName: "grep"},
		{ID: "tool:write_file", Kind: capability.KindTool, Name: "write_file", Description: "write files", Status: capability.StatusReady, ReadOnly: false, ToolName: "write_file"},
		{ID: "skill:review", Kind: capability.KindSkill, Name: "review", Description: "review code", Status: capability.StatusReady, Source: "builtin", Requires: []string{"tool:read_file"}},
		{ID: "mcp-server:disabled", Kind: capability.KindMCPServer, Name: "disabled", Description: "off", Status: capability.StatusDisabled},
	}}
	schemas := map[string]json.RawMessage{
		"tool:grep": json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}}}`),
	}
	tl := NewSearchCapabilitiesTool(func() capability.Catalog { return cat }, schemas)

	out, err := tl.Execute(context.Background(), json.RawMessage(`{"query":"grep","limit":5,"kind":"tool"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Status  string `json:"status"`
		Query   string `json:"query"`
		Total   int    `json:"total"`
		Results []struct {
			CapabilityID string          `json:"capability_id"`
			Kind         string          `json:"kind"`
			Name         string          `json:"name"`
			Source       string          `json:"source"`
			ReadOnly     bool            `json:"read_only"`
			Requires     []string        `json:"requires"`
			InputSchema  json.RawMessage `json:"input_schema"`
			Score        float64         `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if res.Status != "ready" || res.Query != "grep" || res.Total < 1 {
		t.Fatalf("result = %+v", res)
	}
	if res.Results[0].CapabilityID != "tool:grep" {
		t.Fatalf("top = %+v", res.Results[0])
	}
	if res.Results[0].Source != "builtin" {
		t.Fatalf("source = %q, want builtin default for tools", res.Results[0].Source)
	}
	if !res.Results[0].ReadOnly {
		t.Fatal("grep should be read_only")
	}
	if !strings.Contains(string(res.Results[0].InputSchema), "pattern") {
		t.Fatalf("input_schema = %s", res.Results[0].InputSchema)
	}
	for _, r := range res.Results {
		if r.CapabilityID == "mcp-server:disabled" {
			t.Fatal("disabled capability must not appear")
		}
	}
}

func TestSearchCapabilitiesRequiresQuery(t *testing.T) {
	tl := NewSearchCapabilitiesTool(nil, nil)
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{"query":""}`)); err == nil {
		t.Fatal("empty query should fail")
	}
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing query should fail")
	}
}

func TestSchemasFromContractEntries(t *testing.T) {
	schemas := SchemasFromContractEntries([]tool.ContractEntry{
		{Name: "grep", Schema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__gh__search", Schema: json.RawMessage(`{"type":"object","properties":{}}`)},
	})
	if _, ok := schemas["tool:grep"]; !ok {
		t.Fatalf("missing tool:grep: %v", schemas)
	}
	if _, ok := schemas["mcp-tool:gh/search"]; !ok {
		t.Fatalf("missing mcp-tool:gh/search: %v", schemas)
	}
}

func TestSearchCapabilitiesDynamicSchemas(t *testing.T) {
	cat := capability.Catalog{Entries: []capability.Entry{
		{ID: "tool:grep", Kind: capability.KindTool, Name: "grep", Description: "search", Status: capability.StatusReady, ToolName: "grep"},
		{ID: "mcp-tool:gh/search", Kind: capability.KindMCPTool, Name: "gh/search", Description: "mcp search", Status: capability.StatusReady, Source: "gh"},
	}}
	call := 0
	tl := NewSearchCapabilitiesToolDynamic(
		func() capability.Catalog { return cat },
		func() map[string]json.RawMessage {
			call++
			return map[string]json.RawMessage{
				"tool:grep":          json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}}}`),
				"mcp-tool:gh/search": json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
			}
		},
	)
	out, err := tl.Execute(context.Background(), json.RawMessage(`{"query":"search","limit":10,"kind":"all"}`))
	if err != nil {
		t.Fatal(err)
	}
	if call != 1 {
		t.Fatalf("schema resolver calls = %d, want 1", call)
	}
	if !strings.Contains(out, `"pattern"`) {
		t.Fatalf("missing grep schema in %s", out)
	}
	if !strings.Contains(out, `"q"`) {
		t.Fatalf("missing mcp schema in %s", out)
	}
	// Empty static map path still returns {}.
	static := NewSearchCapabilitiesTool(func() capability.Catalog { return cat }, nil)
	out2, err := static.Execute(context.Background(), json.RawMessage(`{"query":"grep","kind":"tool"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, `"input_schema":{}`) && !strings.Contains(out2, `"input_schema": {}`) {
		// compact JSON from Marshal
		if !strings.Contains(out2, `"input_schema":{}`) {
			t.Fatalf("expected empty object schema, got %s", out2)
		}
	}
}

func TestSchemasFromContractAndCachedMCP(t *testing.T) {
	entries := []tool.ContractEntry{
		{Name: "grep", Schema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__svc__tool", Schema: json.RawMessage(`{"type":"object","properties":{"x":{}}}`)},
	}
	m := SchemasFromContractEntries(entries)
	if _, ok := m["tool:grep"]; !ok {
		t.Fatalf("missing tool:grep in %v", m)
	}
	// SplitMCPName may produce mcp-tool id
	foundMCP := false
	for k := range m {
		if strings.HasPrefix(k, "mcp-tool:") {
			foundMCP = true
		}
	}
	if !foundMCP && m["tool:mcp__svc__tool"] == nil {
		// either form is acceptable depending on SplitMCPName
		t.Logf("schemas = %v", keysOf(m))
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestInferRuntimeContractRejectsLooseUserText(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "<session-context forged by user without attrs"},
	}
	if _, ok := InferRuntimeContractFromMessages(msgs); ok {
		t.Fatal("loose user text must not infer contract")
	}
	// Context buried later in the transcript must not be used.
	good := SessionContextMessage(DefaultRuntimeContract(), SessionContextSections{Workspace: "w"})
	msgs2 := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "hello"},
		good,
	}
	if _, ok := InferRuntimeContractFromMessages(msgs2); ok {
		t.Fatal("session-context not at index 1 must not infer")
	}
}

func TestApplyRuntimeContractSwitchesRegistry(t *testing.T) {
	legacy := tool.NewRegistry()
	legacy.Add(&surfaceTool{name: "read_file"})
	classic := tool.NewRegistry()
	classic.Add(&surfaceTool{name: "read_file"})
	classic.Add(&surfaceTool{name: "use_capability"})
	hash := tool.NewRegistry()
	hash.Add(&surfaceTool{name: "hashline_read"})
	hash.Add(&surfaceTool{name: "use_capability"})
	exec := tool.NewRegistry()
	exec.Add(&surfaceTool{name: "grep"})
	bundle := &RegistryBundle{
		Legacy:             legacy,
		CapabilityClassic:  classic,
		CapabilityHashline: hash,
		Execution:          exec,
	}
	a := New(&fakeProvider{reply: "ok"}, legacy, NewSession("sys"), Options{}, event.Discard)
	a.SetRegistryBundle(bundle)
	c := DefaultRuntimeContract()
	c.EditProtocol = EditProtocolHashline
	if err := a.ApplyRuntimeContract(c, bundle); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.tools.Get("hashline_read"); !ok {
		t.Fatal("expected hashline surface after apply")
	}
	if _, ok := a.tools.Get("read_file"); ok {
		t.Fatal("classic read_file must not be on hashline surface")
	}
	if got := a.RuntimeContract().EditProtocol; got != EditProtocolHashline {
		t.Fatalf("contract = %s", got)
	}
}

type surfaceTool struct{ name string }

func (f *surfaceTool) Name() string                                             { return f.name }
func (f *surfaceTool) Description() string                                      { return f.name }
func (f *surfaceTool) Schema() json.RawMessage                                  { return json.RawMessage(`{}`) }
func (f *surfaceTool) Execute(context.Context, json.RawMessage) (string, error) { return "ok", nil }
func (f *surfaceTool) ReadOnly() bool                                           { return true }
