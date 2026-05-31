package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

func (m *chatTUI) showBranchTree() {
	branches, err := m.ctrl.Branches()
	if err != nil {
		m.notice("tree: " + err.Error())
		return
	}
	current := agent.BranchID(m.ctrl.SessionPath())
	m.commitLine(ansi.Hardwrap(control.FormatBranchTree(branches, current), max(m.width, 20), false))
}

func (m *chatTUI) runBranchCommand(input string) {
	args := strings.Fields(input)
	name := ""
	if len(args) > 1 {
		name = strings.TrimSpace(strings.TrimPrefix(input, args[0]))
	}

	// /branch 3 optional-name branches from displayed turn 3. Plain /branch
	// branches from the current tip.
	if len(args) > 1 {
		if n, err := strconv.Atoi(args[1]); err == nil {
			if n <= 0 {
				m.notice("usage: /branch [turn] [name]")
				return
			}
			name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(input, args[0])), args[1]))
			if _, err := m.ctrl.ForkNamed(n-1, name); err != nil {
				return
			}
			m.replayActiveBranch(fmt.Sprintf("branched from turn %d", n))
			return
		}
	}

	if _, err := m.ctrl.Branch(name); err != nil {
		return
	}
	m.showBranchTree()
}

func (m *chatTUI) runSwitchCommand(input string) {
	ref := strings.TrimSpace(strings.TrimPrefix(input, strings.Fields(input)[0]))
	if ref == "" {
		m.notice("usage: /switch <branch id|name>")
		return
	}
	if _, err := m.ctrl.SwitchBranch(ref); err != nil {
		return
	}
	m.replayActiveBranch("switched branch")
}

func (m *chatTUI) replayActiveBranch(title string) {
	m.finalizeStreamed()
	m.pending.Reset()
	m.reasoning.Reset()
	m.todoArgs = ""
	m.chooser = nil
	m.pendingApproval = nil
	m.bubblePending = false
	m.turnDiscarded = false

	m.commitLine("")
	if title != "" {
		m.commitLine(dim("  -- " + title + " --"))
	}
	m.commitLine(strings.TrimRight(renderTUIBanner(m.label, "", m.width), "\n"))
	for _, section := range replaySectionsFor(m.ctrl.History(), m.width, m.renderer) {
		m.commitLine(strings.TrimRight(section, "\n"))
	}
}
