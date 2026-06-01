package cli

// showMemory reports what memory is loaded and where it lives — the TUI analog
// of Claude Code's /memory. It surfaces the doc files and the auto-memory store
// path so the user can open and edit them directly, since the in-terminal UI
// doesn't shell out to an editor.
func (m *chatTUI) showMemory() {
	set := m.ctrl.Memory()
	if set == nil || set.Empty() {
		m.notice("memory: none — add with “#<note>” or create REASONIX.md in the project root")
		return
	}
	m.commitLine(renderMemory(m.width, set))
}
