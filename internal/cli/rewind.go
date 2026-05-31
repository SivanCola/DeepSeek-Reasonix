package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"reasonix/internal/checkpoint"
	"reasonix/internal/control"
)

// rewindPicker is the in-chat overlay for Esc-Esc / "/rewind". Stage 0 lists the
// session's turns (one checkpoint each); stage 1 picks what to restore for the
// chosen turn. It mirrors the chooser overlay: keys route through handleRewindKey
// and it renders via renderRewind while m.rewind is set.
type rewindPicker struct {
	metas []checkpoint.Meta
	sel   int // selected turn (index into metas)
	stage int // 0 = pick turn, 1 = pick scope
	scope int // index into rewindScopes (stage 1)
}

var rewindScopes = []struct {
	label string
	scope control.RewindScope
}{
	{"Code + conversation", control.RewindBoth},
	{"Conversation only", control.RewindConversation},
	{"Code only", control.RewindCode},
}

// openRewind populates the picker from the session's checkpoints, selecting the
// most recent turn. A no-op (with a notice) when there is nothing to rewind.
func (m *chatTUI) openRewind() {
	metas := m.ctrl.Checkpoints()
	if len(metas) == 0 {
		m.notice("nothing to rewind yet")
		return
	}
	m.rewind = &rewindPicker{metas: metas, sel: len(metas) - 1}
}

func (m chatTUI) handleRewindKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.rewind
	switch msg.String() {
	case "esc":
		if r.stage == 1 {
			r.stage = 0
		} else {
			m.rewind = nil
		}
	case "up", "k":
		if r.stage == 0 {
			if r.sel > 0 {
				r.sel--
			}
		} else if r.scope > 0 {
			r.scope--
		}
	case "down", "j":
		if r.stage == 0 {
			if r.sel < len(r.metas)-1 {
				r.sel++
			}
		} else if r.scope < len(rewindScopes)-1 {
			r.scope++
		}
	case "enter":
		if r.stage == 0 {
			r.stage = 1
		} else {
			return m.applyRewind()
		}
	case "b":
		if r.stage == 1 {
			r.scope = 0
			return m.applyRewind()
		}
	case "c":
		if r.stage == 1 {
			r.scope = 1
			return m.applyRewind()
		}
	case "d":
		if r.stage == 1 {
			r.scope = 2
			return m.applyRewind()
		}
	}
	return m, nil
}

func (m chatTUI) applyRewind() (tea.Model, tea.Cmd) {
	r := m.rewind
	meta := r.metas[r.sel]
	scope := rewindScopes[r.scope].scope
	m.rewind = nil
	if err := m.ctrl.Rewind(meta.Turn, scope); err != nil {
		m.notice("rewind: " + err.Error())
		return m, nil
	}
	// The controller emits a notice marking the rewind point; the committed
	// transcript stays in terminal scrollback (v2 has no managed viewport), so for a
	// conversation/both rewind we prefill the composer with that turn's prompt to
	// re-send or edit — Claude Code's behavior — while the model's context is
	// truncated underneath.
	if scope != control.RewindCode && strings.TrimSpace(meta.Prompt) != "" {
		m.input.SetValue(meta.Prompt)
		m.growInputToFit()
	}
	return m, nil
}

func (m chatTUI) renderRewind() string {
	r := m.rewind
	if r == nil {
		return ""
	}
	w := max(m.width, 10)
	var b strings.Builder
	if r.stage == 0 {
		b.WriteString(accent("⟲ Rewind") + dim(" — pick a turn") + "\n")
		for i, meta := range r.metas {
			b.WriteString(rowLine(i == r.sel, meta.Turn+1, "", turnLabel(meta, w), false) + "\n")
		}
		b.WriteString(dim("↑/↓ move · Enter choose · Esc close"))
		return choicePanelStyle.Width(w).Render(b.String())
	}
	meta := r.metas[r.sel]
	b.WriteString(accent("⟲ Restore to turn ") + fmt.Sprintf("%d ", meta.Turn+1) + dim(oneLine(meta.Prompt, 48)) + "\n")
	for i, s := range rewindScopes {
		b.WriteString(rowLine(i == r.scope, i+1, "", s.label, false) + "\n")
	}
	b.WriteString(dim("↑/↓ · Enter apply · b/c/d quick · Esc back"))
	return choicePanelStyle.Width(w).Render(b.String())
}

func turnLabel(meta checkpoint.Meta, w int) string {
	label := oneLine(meta.Prompt, max(20, w-30))
	if n := len(meta.Paths); n > 0 {
		s := ""
		if n != 1 {
			s = "s"
		}
		label += dim(fmt.Sprintf("  (%d file%s)", n, s))
	}
	return label
}

// oneLine flattens s to a single line and truncates it to display width n.
func oneLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return "(empty)"
	}
	return ansi.Truncate(s, n, "…")
}
