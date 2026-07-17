package boot

import (
	"sync"

	"reasonix/internal/tool"
)

// runtimeMCPRegistry routes MCP tools that arrive after boot. Before the
// RuntimeContract surfaces are snapshotted it writes to the bootstrap registry;
// afterwards it mirrors mutations to Execution and Legacy only. Capability
// provider registries are deliberately never targets.
//
// It also implements plugin's lazy registry writer contract, so a background
// cache-miss handshake cannot publish real tools into the abandoned pre-clone
// registry.
type runtimeMCPRegistry struct {
	mu       sync.Mutex
	fallback *tool.Registry
	targets  []*tool.Registry
}

func newRuntimeMCPRegistry(fallback *tool.Registry) *runtimeMCPRegistry {
	return &runtimeMCPRegistry{fallback: fallback}
}

// bindSnapshot runs snapshot while runtime MCP writes are paused, then installs
// its returned targets before writes resume. This closes the race where a
// background lazy handshake could finish between Clone and target binding.
func (r *runtimeMCPRegistry) bindSnapshot(snapshot func() []*tool.Registry) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets = uniqueRegistries(snapshot())
}

func (r *runtimeMCPRegistry) Add(t tool.Tool) {
	if r == nil || t == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, reg := range r.targetsLocked() {
		reg.Add(t)
	}
}

func (r *runtimeMCPRegistry) RemovePrefix(prefix string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for _, reg := range r.targetsLocked() {
		removed += reg.RemovePrefix(prefix)
	}
	return removed
}

// replacePrefix is used by explicit connect flows. Unlike a late background
// publish, an explicit reconnect intentionally clears a prior suspension.
func (r *runtimeMCPRegistry) replacePrefix(prefix string, tools []tool.Tool) []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for i, reg := range r.targetsLocked() {
		reg.ResumePrefix(prefix)
		reg.RemovePrefix(prefix)
		added := addTools(reg, tools)
		if i == 0 {
			names = added
		}
	}
	return names
}

func (r *runtimeMCPRegistry) targetsLocked() []*tool.Registry {
	if len(r.targets) > 0 {
		return r.targets
	}
	if r.fallback != nil {
		return []*tool.Registry{r.fallback}
	}
	return nil
}

func uniqueRegistries(in []*tool.Registry) []*tool.Registry {
	out := make([]*tool.Registry, 0, len(in))
	seen := map[*tool.Registry]bool{}
	for _, reg := range in {
		if reg == nil || seen[reg] {
			continue
		}
		seen[reg] = true
		out = append(out, reg)
	}
	return out
}
