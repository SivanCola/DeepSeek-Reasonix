package agent

import (
	"encoding/json"
	"strings"
)

func (t *restrictedCapabilityProxy) bindToolResultSession(session func() *Session) {
	if binder, ok := t.Tool.(toolResultSessionBinder); ok {
		binder.bindToolResultSession(session)
	}
}

// emptyCapabilityListResult is the fail-closed list payload: no server metadata.
func emptyCapabilityListResult(note string) string {
	if strings.TrimSpace(note) == "" {
		note = "list is filtered to this subagent's allowed MCP servers."
	}
	b, err := json.MarshalIndent(map[string]any{
		"capabilities": []any{},
		"servers":      []listServerInfo{},
		"note":         note,
	}, "", "  ")
	if err != nil {
		return `{"servers":[],"note":"list is filtered to this subagent's allowed MCP servers."}`
	}
	return string(b)
}

// filterCapabilityListResult keeps only servers in the allowlist for restricted
// proxies. Empty allowlist or unreadable payloads fail closed (empty server
// list) so discovery never leaks the full configured MCP inventory.
func filterCapabilityListResult(raw string, servers map[string]bool) string {
	const baseNote = "list is filtered to this subagent's allowed MCP servers."
	if len(servers) == 0 {
		return emptyCapabilityListResult(baseNote + " No allowed MCP servers were resolved from the profile allowlist.")
	}
	var payload struct {
		Capabilities []json.RawMessage `json:"capabilities"`
		Servers      []listServerInfo  `json:"servers"`
		Note         string            `json:"note"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return emptyCapabilityListResult(baseNote + " List payload was unreadable; returning no servers (fail-closed).")
	}
	filtered := make([]listServerInfo, 0, len(payload.Servers))
	for _, s := range payload.Servers {
		if servers[strings.TrimSpace(s.Name)] {
			filtered = append(filtered, s)
		}
	}
	payload.Servers = filtered
	filteredCapabilities := make([]json.RawMessage, 0, 1)
	for _, raw := range payload.Capabilities {
		var entry struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &entry) == nil && strings.TrimSpace(entry.ID) == sessionToolResultCapabilityID {
			filteredCapabilities = append(filteredCapabilities, raw)
		}
	}
	payload.Capabilities = filteredCapabilities
	if payload.Note == "" {
		payload.Note = baseNote
	} else if !strings.Contains(payload.Note, "Filtered to this subagent") {
		payload.Note += " Filtered to this subagent's allowed MCP servers."
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return emptyCapabilityListResult(baseNote + " Failed to encode filtered list (fail-closed).")
	}
	return string(b)
}
