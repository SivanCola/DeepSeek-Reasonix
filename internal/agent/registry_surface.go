package agent

import (
	"strings"
	"sync"

	"reasonix/internal/tool"
)

// RegistrySurface names one of the three provider-visible tool surfaces built
// at boot and reused for the process lifetime.
type RegistrySurface string

const (
	SurfaceLegacy             RegistrySurface = "legacy"
	SurfaceCapabilityClassic  RegistrySurface = "capability-classic"
	SurfaceCapabilityHashline RegistrySurface = "capability-hashline"
)

// RegistryBundle holds the three provider surfaces plus the shared execution
// registry used by use_capability / search_capabilities for optional tools.
// Provider-visible schemas on capability surfaces never gain MCP tools at
// runtime; only the execution registry and internal catalog update.
type RegistryBundle struct {
	mu sync.RWMutex

	Legacy             *tool.Registry
	CapabilityClassic  *tool.Registry
	CapabilityHashline *tool.Registry
	// Execution holds every enabled tool for proxy dispatch (includes optional
	// tools not on the capability provider surface).
	Execution *tool.Registry
}

// ProviderRegistry returns the registry for the given contract.
// Falls back to Legacy when a capability surface was not built.
func (b *RegistryBundle) ProviderRegistry(c RuntimeContract) *tool.Registry {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	c = NormalizeRuntimeContract(&c)
	if !c.IsCapabilitySurface() {
		if b.Legacy != nil {
			return b.Legacy
		}
		// Single-surface boots store the active registry in Execution.
		return b.Execution
	}
	if c.IsHashline() {
		if b.CapabilityHashline != nil {
			return b.CapabilityHashline
		}
		if b.CapabilityClassic != nil {
			return b.CapabilityClassic
		}
	} else if b.CapabilityClassic != nil {
		return b.CapabilityClassic
	}
	if b.Legacy != nil {
		return b.Legacy
	}
	return b.Execution
}

// ExecutionRegistry returns the full proxy dispatch registry.
func (b *RegistryBundle) ExecutionRegistry() *tool.Registry {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.Execution != nil {
		return b.Execution
	}
	return b.Legacy
}

// SurfaceName returns the surface key for diagnostics.
func (b *RegistryBundle) SurfaceName(c RuntimeContract) RegistrySurface {
	c = NormalizeRuntimeContract(&c)
	if !c.IsCapabilitySurface() {
		return SurfaceLegacy
	}
	if c.IsHashline() {
		return SurfaceCapabilityHashline
	}
	return SurfaceCapabilityClassic
}

// HasCapabilitySurfaces reports whether classic/hashline provider registries
// were built (so Resume can switch without a full rebuild).
func (b *RegistryBundle) HasCapabilitySurfaces() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.CapabilityClassic != nil || b.CapabilityHashline != nil
}

// MCPWriteTargets returns registries that receive MCP tool mutations:
// Execution (always) and Legacy when distinct. Capability surfaces are never
// included — their provider-visible schema stays stable across MCP connect.
func (b *RegistryBundle) MCPWriteTargets() []*tool.Registry {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []*tool.Registry
	seen := map[*tool.Registry]bool{}
	add := func(r *tool.Registry) {
		if r == nil || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}
	add(b.Execution)
	add(b.Legacy)
	return out
}

// CoreCapabilityToolNames are always provider-visible on capability surfaces
// (before classic/hashline edit tools).
var CoreCapabilityToolNames = []string{
	"ask",
	"bash",
	"bash_output",
	"kill_shell",
	"wait",
	"search_capabilities",
	"use_capability",
}

// ClassicEditToolNames are provider-visible only on classic edit protocol.
var ClassicEditToolNames = []string{
	"read_file",
	"edit_file",
	"write_file",
}

// HashlineEditToolNames are provider-visible only on hashline edit protocol.
var HashlineEditToolNames = []string{
	"hashline_read",
	"hashline_edit",
	"hashline_grep",
}

// ClassicExcludedFromHashline must not appear on hashline provider surface or
// capability catalog (and use_capability hard-rejects them).
var ClassicExcludedFromHashline = []string{
	"read_file",
	"edit_file",
	"write_file",
	"multi_edit",
	"grep",
}

// HashlineExcludedFromClassic are rejected via use_capability on classic sessions.
var HashlineExcludedFromClassic = []string{
	"hashline_read",
	"hashline_edit",
	"hashline_grep",
}

// IsProviderVisibleCore reports whether name is always on capability surfaces.
func IsProviderVisibleCore(name string) bool {
	for _, n := range CoreCapabilityToolNames {
		if n == name {
			return true
		}
	}
	return false
}

// IsCrossProtocolTool reports whether capability_id / tool name is forbidden
// under the given edit protocol when routed through use_capability.
// editProtocol accepts classic|hashline|classic-v1|hashline-v1; empty skips.
func IsCrossProtocolTool(editProtocol, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	switch normalizeEditProtocol(editProtocol) {
	case EditProtocolHashline:
		for _, n := range ClassicExcludedFromHashline {
			if n == toolName {
				return true
			}
		}
	case EditProtocolClassic:
		for _, n := range HashlineExcludedFromClassic {
			if n == toolName {
				return true
			}
		}
	}
	return false
}

// FilterRegistryBySets copies tools from src whose names are in allow (nil = all)
// and not in deny. Distinct from FilterRegistry (whitelist + exclude list) used
// by sub-agent assembly.
func FilterRegistryBySets(src *tool.Registry, allow map[string]bool, deny map[string]bool) *tool.Registry {
	out := tool.NewRegistry()
	if src == nil {
		return out
	}
	for _, name := range src.Names() {
		if deny != nil && deny[name] {
			continue
		}
		if allow != nil && !allow[name] {
			continue
		}
		if t, ok := src.Get(name); ok {
			out.Add(t)
		}
	}
	return out
}

// AllowSet builds a set from names.
func AllowSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
