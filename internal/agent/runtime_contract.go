package agent

import (
	"fmt"
	"strings"
)

// Runtime contract constants pin the session-stable provider-visible surfaces.
// Mid-session switching of these values is forbidden: cache keys, tool schemas,
// and prompt layout must stay fixed for the life of a session.
const (
	ToolSurfaceLegacy      = "legacy-v1"
	ToolSurfaceCapability  = "capability-v2"
	EditProtocolClassic    = "classic-v1"
	EditProtocolHashline   = "hashline-v1"
	PromptLayoutSystem     = "system-v1"
	PromptLayoutSessionCtx = "session-context-v2"
)

// RuntimeContract is the session-stable protocol bundle persisted in BranchMeta.
// Old sidecars missing the field resolve to LegacyDefaultRuntimeContract.
type RuntimeContract struct {
	ToolSurface  string `json:"tool_surface"`  // legacy-v1 | capability-v2
	EditProtocol string `json:"edit_protocol"` // classic-v1 | hashline-v1
	PromptLayout string `json:"prompt_layout"` // system-v1 | session-context-v2
}

// LegacyDefaultRuntimeContract is used for pre-contract sessions and for
// corrupted sidecars that cannot be inferred safely.
func LegacyDefaultRuntimeContract() RuntimeContract {
	return RuntimeContract{
		ToolSurface:  ToolSurfaceLegacy,
		EditProtocol: EditProtocolClassic,
		PromptLayout: PromptLayoutSystem,
	}
}

// DefaultRuntimeContract is the contract used when tests or callers request the
// post-upgrade target (capability-v2 + session-context-v2 + classic). Product
// new-session defaults stay on LegacyDefaultRuntimeContract until the release
// gate in the capability-upgrade plan flips them.
func DefaultRuntimeContract() RuntimeContract {
	return RuntimeContract{
		ToolSurface:  ToolSurfaceCapability,
		EditProtocol: EditProtocolClassic,
		PromptLayout: PromptLayoutSessionCtx,
	}
}

// NormalizeRuntimeContract validates and canonicalizes a contract. Empty fields
// fall back to the legacy triple so old data remains readable.
func NormalizeRuntimeContract(c *RuntimeContract) RuntimeContract {
	if c == nil {
		return LegacyDefaultRuntimeContract()
	}
	out := *c
	if strings.TrimSpace(out.ToolSurface) == "" {
		out.ToolSurface = ToolSurfaceLegacy
	}
	if strings.TrimSpace(out.EditProtocol) == "" {
		out.EditProtocol = EditProtocolClassic
	}
	if strings.TrimSpace(out.PromptLayout) == "" {
		out.PromptLayout = PromptLayoutSystem
	}
	return out
}

// ValidateRuntimeContract returns an error for unknown enum values. Callers that
// load user config must reject illegal values instead of silently correcting them.
func ValidateRuntimeContract(c RuntimeContract) error {
	switch c.ToolSurface {
	case ToolSurfaceLegacy, ToolSurfaceCapability:
	default:
		return fmt.Errorf("unknown tool_surface %q (want %s|%s)", c.ToolSurface, ToolSurfaceLegacy, ToolSurfaceCapability)
	}
	switch c.EditProtocol {
	case EditProtocolClassic, EditProtocolHashline:
	default:
		return fmt.Errorf("unknown edit_protocol %q (want %s|%s)", c.EditProtocol, EditProtocolClassic, EditProtocolHashline)
	}
	switch c.PromptLayout {
	case PromptLayoutSystem, PromptLayoutSessionCtx:
	default:
		return fmt.Errorf("unknown prompt_layout %q (want %s|%s)", c.PromptLayout, PromptLayoutSystem, PromptLayoutSessionCtx)
	}
	// Hashline only exists as a capability-surface edit protocol.
	if c.EditProtocol == EditProtocolHashline && c.ToolSurface != ToolSurfaceCapability {
		return fmt.Errorf("edit_protocol %s requires tool_surface %s", EditProtocolHashline, ToolSurfaceCapability)
	}
	return nil
}

// Equal reports whether two contracts are identical after normalization.
func (c RuntimeContract) Equal(other RuntimeContract) bool {
	a := NormalizeRuntimeContract(&c)
	b := NormalizeRuntimeContract(&other)
	return a.ToolSurface == b.ToolSurface &&
		a.EditProtocol == b.EditProtocol &&
		a.PromptLayout == b.PromptLayout
}

// Clone returns a deep copy pointer suitable for BranchMeta persistence.
func (c RuntimeContract) Clone() *RuntimeContract {
	n := NormalizeRuntimeContract(&c)
	return &n
}

// ResolveRuntimeContractFromMeta returns the contract for a loaded BranchMeta.
// Missing fields always resolve to the legacy triple so old sessions keep their
// original prompt and tool protocol (no in-place migration).
func ResolveRuntimeContractFromMeta(meta BranchMeta) RuntimeContract {
	return NormalizeRuntimeContract(meta.RuntimeContract)
}

// Config capability_surface / edit_protocol / prompt_layout map to wire values.
// Empty capability_surface / prompt_layout keep the pre-gate legacy defaults;
// pass "stable" / "session_context" to opt into the upgrade target.
func RuntimeContractFromConfigValues(capabilitySurface, editProtocol, promptLayout string) (RuntimeContract, error) {
	c := LegacyDefaultRuntimeContract()

	switch strings.ToLower(strings.TrimSpace(capabilitySurface)) {
	case "stable":
		c.ToolSurface = ToolSurfaceCapability
	case "", "legacy":
		c.ToolSurface = ToolSurfaceLegacy
	default:
		return RuntimeContract{}, fmt.Errorf("tools.capability_surface: invalid value %q (want stable|legacy)", capabilitySurface)
	}

	switch strings.ToLower(strings.TrimSpace(editProtocol)) {
	case "", "classic":
		c.EditProtocol = EditProtocolClassic
	case "hashline":
		c.EditProtocol = EditProtocolHashline
	default:
		return RuntimeContract{}, fmt.Errorf("tools.edit_protocol: invalid value %q (want classic|hashline)", editProtocol)
	}

	switch strings.ToLower(strings.TrimSpace(promptLayout)) {
	case "session_context":
		c.PromptLayout = PromptLayoutSessionCtx
	case "", "legacy_system":
		c.PromptLayout = PromptLayoutSystem
	default:
		return RuntimeContract{}, fmt.Errorf("agent.prompt_layout: invalid value %q (want session_context|legacy_system)", promptLayout)
	}

	if err := ValidateRuntimeContract(c); err != nil {
		return RuntimeContract{}, err
	}
	return c, nil
}

// ParseEditProtocolFlag maps CLI --edit-protocol classic|hashline to contract values.
func ParseEditProtocolFlag(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "classic":
		return EditProtocolClassic, nil
	case "hashline":
		return EditProtocolHashline, nil
	default:
		return "", fmt.Errorf("--edit-protocol: invalid value %q (want classic|hashline)", v)
	}
}

// IsCapabilitySurface reports whether the contract uses the stable capability proxy.
func (c RuntimeContract) IsCapabilitySurface() bool {
	return NormalizeRuntimeContract(&c).ToolSurface == ToolSurfaceCapability
}

// IsHashline reports whether the contract uses Hashline edit tools.
func (c RuntimeContract) IsHashline() bool {
	return NormalizeRuntimeContract(&c).EditProtocol == EditProtocolHashline
}

// IsSessionContextLayout reports whether workspace/env/memory/skills live in a
// pinned synthetic user message rather than the system prompt.
func (c RuntimeContract) IsSessionContextLayout() bool {
	return NormalizeRuntimeContract(&c).PromptLayout == PromptLayoutSessionCtx
}
