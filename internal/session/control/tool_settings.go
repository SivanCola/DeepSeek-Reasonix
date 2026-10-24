package control

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"reasonix/internal/contract/config"
	"reasonix/internal/contract/tool"
)

// ToolSetting is one tool as the settings page lists it.
type ToolSetting struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Disabled    bool   `json:"disabled"`
	// RequiredBy names the host feature that needs the tool; such a tool has no switch.
	RequiredBy string `json:"requiredBy,omitempty"`
	// ByProject marks a removal the project file made and the user file does not; this page cannot lift it.
	ByProject bool `json:"byProject,omitempty"`
}

// ToolSettings is the tools a request carries plus the ones the user removed,
// as the user file records the removals. A project file can add to the list; its
// removals are listed as ByProject and are not what this page edits.
type ToolSettings struct {
	Tools []ToolSetting `json:"tools"`
	Path  string        `json:"path"`
}

// ToolRequiredError is the refusal to remove a tool the host needs.
type ToolRequiredError struct{ Tool, By string }

func (e *ToolRequiredError) Error() string {
	return fmt.Sprintf("%s cannot be turned off: it is needed for %s", e.Tool, tool.FeatureLabel(e.By))
}

// ToolSettings lists the provider-visible tools of this session and the names
// the user file removed, which the running session no longer carries.
func (c *Controller) ToolSettings() ToolSettings {
	path := config.UserConfigPath()
	removed := config.LoadForEdit(path).DisabledTools()
	out := ToolSettings{Path: path, Tools: []ToolSetting{}}
	seen := map[string]bool{}
	reg := c.mcp.registry()
	if reg != nil {
		for _, schema := range reg.Schemas() {
			item := ToolSetting{Name: schema.Name, Description: firstDescriptionLine(schema.Description)}
			if t, ok := reg.Get(schema.Name); ok {
				if required, ok := t.(tool.HostRequired); ok {
					item.RequiredBy = required.HostRequiredBy()
				}
			}
			seen[item.Name] = true
			out.Tools = append(out.Tools, item)
		}
	}
	for _, name := range reg.DisabledNames() {
		if !seen[name] && !slices.Contains(removed, name) {
			seen[name] = true
			out.Tools = append(out.Tools, ToolSetting{Name: name, Disabled: true, ByProject: true})
		}
	}
	for _, name := range removed {
		if !seen[name] {
			out.Tools = append(out.Tools, ToolSetting{Name: name})
		}
	}
	for i := range out.Tools {
		out.Tools[i].Disabled = out.Tools[i].ByProject || slices.Contains(removed, out.Tools[i].Name)
	}
	sort.Slice(out.Tools, func(i, j int) bool { return out.Tools[i].Name < out.Tools[j].Name })
	return out
}

// SaveToolDisabled adds or removes one tool from the user file's list. A tool
// the host needs is refused; the removal takes effect in sessions opened after.
func (c *Controller) SaveToolDisabled(name string, disabled bool) error {
	name = strings.TrimSpace(name)
	if disabled {
		if reg := c.mcp.registry(); reg != nil {
			if t, ok := reg.Get(name); ok {
				if required, ok := t.(tool.HostRequired); ok {
					return &ToolRequiredError{Tool: name, By: required.HostRequiredBy()}
				}
			}
		}
	}
	unlock := config.LockUserConfigEdits()
	defer unlock()
	path := config.UserConfigPath()
	cfg := config.LoadForEdit(path)
	cfg.SetToolDisabled(name, disabled)
	return cfg.SaveTo(path)
}

func firstDescriptionLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if runes := []rune(line); len(runes) > 200 {
		return string(runes[:200]) + "…"
	}
	return line
}
