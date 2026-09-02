package cli

import (
	"reasonix/internal/control"
)

// slashCompletionCache memoizes the two expensive completion snapshots: the
// slash catalog (commands, skills, prompts) and the arg-completion data
// (config models/providers, plugin state, MCP names, memory refs). Assembling
// the arg data runs several config loads plus a plugin-state read per keystroke
// otherwise, which reads as visible input lag on cold Linux caches (#9503).
type slashCompletionCache struct {
	items []compItem
	// argData is the memoized snapshot; argBuilt separates "not built" from a
	// snapshot taken while modelRef was still "".
	argData  control.ArgData
	argModel string
	argBuilt bool
}

func (m *chatTUI) ensureSlashCache() *slashCompletionCache {
	if m.slashCache == nil {
		m.slashCache = &slashCompletionCache{}
	}
	return m.slashCache
}

// slashItems returns the cached slash catalog. Rebuilds only after
// invalidateSlashCatalog — never on ordinary keystrokes.
func (m *chatTUI) slashItems() []compItem {
	if c := m.slashCache; c != nil && c.items != nil {
		return c.items
	}
	// Immutable snapshot so keystroke filtering never mutates shared state.
	items := m.buildSlashCatalog()
	out := make([]compItem, len(items))
	copy(out, items)
	m.ensureSlashCache().items = out
	return out
}

// slashArgDataSnapshot returns the memoized arg-completion data. It rebuilds
// when the active model changed or the cache was invalidated; filtering stays
// in-memory, so keystrokes inside an open arg popup cost no I/O.
func (m *chatTUI) slashArgDataSnapshot() control.ArgData {
	if c := m.slashCache; c != nil && c.argBuilt && c.argModel == m.modelRef {
		return c.argData
	}
	c := m.ensureSlashCache()
	c.argData = m.slashArgData()
	c.argModel = m.modelRef
	c.argBuilt = true
	return c.argData
}

// prepareSlashArgSnapshot keeps one stable snapshot while an argument popup is
// open. A closed or different menu starts a new popup generation, so dynamic
// memory, MCP, plugin, skill, and effort data are refreshed before filtering.
func (m *chatTUI) prepareSlashArgSnapshot() {
	if m.completion.active && m.completion.kind == compSlashArg {
		return
	}
	if c := m.slashCache; c != nil {
		c.argData = control.ArgData{}
		c.argModel = ""
		c.argBuilt = false
	}
}

// invalidateSlashCatalog drops the cached catalog and arg data so the next
// slashItems/slashArgDataSnapshot call rebuilds them. Call from model switch,
// skill rescan, /reload-cmd, and any path that mutates
// commands/skills/host/extension actions.
func (m *chatTUI) invalidateSlashCatalog() {
	m.slashCache = nil
}
