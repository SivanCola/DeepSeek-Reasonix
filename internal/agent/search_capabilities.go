package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/capability"
	"reasonix/internal/plugin"
	"reasonix/internal/tool"
)

// SearchCapabilitiesTool is the stable, read-only catalog search tool. It never
// starts MCP processes, opens network connections, or prompts for permission.
type SearchCapabilitiesTool struct {
	catalog func() capability.Catalog
	// schemas returns capability_id → input JSON Schema. Prefer a live function
	// over a static map so execution-registry and MCP cache schemas stay current
	// without touching the provider-visible tool schema.
	schemas func() map[string]json.RawMessage
}

// NewSearchCapabilitiesTool builds search_capabilities.
// catalog may be nil (empty results). schemas may be a static map or nil;
// prefer NewSearchCapabilitiesToolDynamic for production wiring.
func NewSearchCapabilitiesTool(catalog func() capability.Catalog, schemas map[string]json.RawMessage) *SearchCapabilitiesTool {
	var fn func() map[string]json.RawMessage
	if len(schemas) > 0 {
		copySchemas := make(map[string]json.RawMessage, len(schemas))
		for k, v := range schemas {
			copySchemas[k] = append(json.RawMessage(nil), v...)
		}
		fn = func() map[string]json.RawMessage { return copySchemas }
	}
	return &SearchCapabilitiesTool{catalog: catalog, schemas: fn}
}

// NewSearchCapabilitiesToolDynamic builds search_capabilities with a live schema
// resolver (execution registry + MCP cache). The resolver runs only inside
// Execute; it never mutates provider-visible tool schemas.
func NewSearchCapabilitiesToolDynamic(catalog func() capability.Catalog, schemas func() map[string]json.RawMessage) *SearchCapabilitiesTool {
	return &SearchCapabilitiesTool{catalog: catalog, schemas: schemas}
}

func (*SearchCapabilitiesTool) Name() string { return "search_capabilities" }

func (*SearchCapabilitiesTool) Description() string {
	return "Search the capability catalog by free-text query. Returns ranked tool, skill, and MCP capabilities with metadata and input schemas. Read-only: never starts MCP servers, network calls, or permission prompts. Prefer this before use_capability(action=\"inspect\") when looking up which capability_id to call."
}

func (*SearchCapabilitiesTool) ReadOnly() bool { return true }

func (*SearchCapabilitiesTool) Schema() json.RawMessage {
	// Stable schema — must not change across turns.
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"query":{"type":"string","minLength":1},
			"limit":{"type":"integer","minimum":1,"maximum":10,"default":5},
			"kind":{"type":"string","enum":["all","tool","skill","mcp"],"default":"all"}
		},
		"required":["query"]
	}`)
}

type searchCapabilitiesResult struct {
	Status  string                  `json:"status"`
	Query   string                  `json:"query"`
	Total   int                     `json:"total"`
	Results []searchCapabilitiesHit `json:"results"`
}

type searchCapabilitiesHit struct {
	CapabilityID string          `json:"capability_id"`
	Kind         string          `json:"kind"`
	Name         string          `json:"name"`
	Source       string          `json:"source"`
	Description  string          `json:"description"`
	Status       string          `json:"status"`
	ReadOnly     bool            `json:"read_only"`
	Requires     []string        `json:"requires"`
	InputSchema  json.RawMessage `json:"input_schema"`
	Score        float64         `json:"score"`
}

func (t *SearchCapabilitiesTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
		Kind  string `json:"kind"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	query := strings.TrimSpace(p.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	kind := capability.ParseSearchKind(p.Kind)
	switch kind {
	case capability.SearchKindAll, capability.SearchKindTool, capability.SearchKindSkill, capability.SearchKindMCP:
	default:
		return "", fmt.Errorf("kind must be one of all, tool, skill, mcp; got %q", p.Kind)
	}

	var entries []capability.Entry
	if t.catalog != nil {
		entries = t.catalog().Entries
	}
	hits := capability.Search(entries, capability.SearchOptions{
		Query: query,
		Limit: p.Limit,
		Kind:  kind,
	})

	schemaMap := map[string]json.RawMessage{}
	if t.schemas != nil {
		if m := t.schemas(); m != nil {
			schemaMap = m
		}
	}

	out := searchCapabilitiesResult{
		Status:  "ready",
		Query:   query,
		Total:   len(hits),
		Results: make([]searchCapabilitiesHit, 0, len(hits)),
	}
	for _, h := range hits {
		e := h.Entry
		requires := e.Requires
		if requires == nil {
			requires = []string{}
		}
		schema := resolveCapabilitySchema(schemaMap, e)
		status := string(e.Status)
		if status == "" {
			status = string(capability.StatusReady)
		}
		source := e.Source
		if source == "" && e.Kind == capability.KindTool {
			source = "builtin"
		}
		out.Results = append(out.Results, searchCapabilitiesHit{
			CapabilityID: e.ID,
			Kind:         string(e.Kind),
			Name:         e.Name,
			Source:       source,
			Description:  e.Description,
			Status:       status,
			ReadOnly:     e.ReadOnly,
			Requires:     requires,
			InputSchema:  schema,
			Score:        h.Score,
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func resolveCapabilitySchema(schemas map[string]json.RawMessage, e capability.Entry) json.RawMessage {
	empty := json.RawMessage(`{}`)
	if len(schemas) == 0 {
		return empty
	}
	if raw, ok := schemas[e.ID]; ok && len(raw) > 0 && string(raw) != "null" {
		return raw
	}
	if e.ToolName != "" {
		if raw, ok := schemas["tool:"+e.ToolName]; ok && len(raw) > 0 && string(raw) != "null" {
			return raw
		}
	}
	return empty
}

// SchemasFromContractEntries builds a capability_id → schema map from provider-
// visible or execution-registry contract snapshots.
func SchemasFromContractEntries(entries []tool.ContractEntry) map[string]json.RawMessage {
	if len(entries) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(entries))
	for _, e := range entries {
		if e.Name == "" || len(e.Schema) == 0 {
			continue
		}
		id := "tool:" + e.Name
		if server, raw, ok := tool.SplitMCPName(e.Name); ok {
			id = "mcp-tool:" + server + "/" + raw
		}
		out[id] = append(json.RawMessage(nil), e.Schema...)
	}
	return out
}

// SchemasFromCachedMCPTools merges MCP schema-cache / proxy-live tools into a
// capability_id → schema map. Does not start servers or network I/O.
func SchemasFromCachedMCPTools(byServer map[string][]plugin.CachedTool) map[string]json.RawMessage {
	if len(byServer) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage)
	for server, tools := range byServer {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}
		for _, ct := range tools {
			name := strings.TrimSpace(ct.Name)
			if name == "" || len(ct.Schema) == 0 {
				continue
			}
			id := "mcp-tool:" + server + "/" + name
			out[id] = append(json.RawMessage(nil), ct.Schema...)
		}
	}
	return out
}

// MergeSchemaMaps returns a new map with later maps overwriting earlier keys.
func MergeSchemaMaps(maps ...map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, m := range maps {
		for k, v := range m {
			if len(v) == 0 {
				continue
			}
			out[k] = append(json.RawMessage(nil), v...)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Ensure SearchCapabilitiesTool satisfies the tool contract.
var _ tool.Tool = (*SearchCapabilitiesTool)(nil)
