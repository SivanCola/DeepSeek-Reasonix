package cli

import (
	"fmt"
	"os"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/hook"
	"reasonix/internal/skill"
)

// runSkillSubcommand handles "/skill" (and "/skills"): list the discoverable
// skills, show one's body, scaffold a new one, or inspect the discovery paths.
// "/skill <name> [args]" with no recognised subcommand falls through to invoking
// the skill (handled by runSlashCommand's default branch), so this only owns the
// management verbs.
func (m *chatTUI) runSkillSubcommand(input string) {
	args := tokenizeArgs(input) // args[0] == "/skill"
	sub := ""
	if len(args) > 1 {
		sub = strings.ToLower(args[1])
	}
	switch sub {
	case "", "list", "ls":
		m.skillList()
	case "show", "cat":
		if len(args) < 3 {
			m.notice("usage: /skill show <name>")
			return
		}
		m.skillShow(args[2])
	case "new", "init":
		if len(args) < 3 {
			m.notice("usage: /skill new <name> [--global]")
			return
		}
		global := containsArg(args[3:], "--global")
		m.skillNew(args[2], global)
	case "paths":
		m.skillPaths()
	default:
		// /skill is management-only; a skill is invoked directly as /<name>.
		hint := ""
		if _, ok := m.ctrl.RunSkill("/" + args[1]); ok {
			hint = " (to run it, type /" + args[1] + ")"
		}
		m.notice("unknown /skill subcommand " + args[1] + hint + " — try: /skill, /skill show <name>, /skill new <name>, /skill paths")
	}
}

func (m *chatTUI) skillList() {
	skills := m.skills
	if len(skills) == 0 {
		m.notice("no skills found. Add SKILL.md / <name>.md under .reasonix/skills (project) or ~/.reasonix/skills (global); .agents/.agent/.claude skills dirs also work. Invoke with /<name> or run_skill.")
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", dim(fmt.Sprintf("  · skills (%d)", len(skills))))
	for _, s := range skills {
		tag := ""
		if s.RunAs == skill.RunSubagent {
			tag = " 🧬"
		}
		desc := s.Description
		if len([]rune(desc)) > 70 {
			desc = string([]rune(desc)[:69]) + "…"
		}
		fmt.Fprintf(&b, "  %-16s %-9s %s%s\n", "/"+s.Name, "("+string(s.Scope)+")", desc, tag)
	}
	b.WriteString(dim("  invoke: /<name> [args] · author: install_skill (model) or /skill new <name>"))
	m.notice(b.String())
}

func (m *chatTUI) skillShow(name string) {
	for _, s := range m.skills {
		if s.Name == name {
			var b strings.Builder
			fmt.Fprintf(&b, "▸ %s  (%s)\n", s.Name, s.Scope)
			if s.Description != "" {
				b.WriteString("  " + s.Description + "\n")
			}
			b.WriteString(dim("  " + s.Path + "\n\n"))
			b.WriteString(s.Body)
			m.notice(b.String())
			return
		}
	}
	m.notice("unknown skill: " + name)
}

func (m *chatTUI) skillNew(name string, global bool) {
	st := m.skillStore()
	scope := skill.ScopeProject
	if global || !st.HasProjectScope() {
		scope = skill.ScopeGlobal
	}
	path, err := st.Create(name, scope)
	if err != nil {
		m.notice("skill new: " + err.Error())
		return
	}
	m.notice(fmt.Sprintf("created skill %q at %s — edit it, then /new (or restart) to pick it up", name, path))
}

func (m *chatTUI) skillPaths() {
	st := m.skillStore()
	var b strings.Builder
	b.WriteString(dim("  · skill discovery paths (highest priority first)\n"))
	for _, r := range st.Roots() {
		fmt.Fprintf(&b, "  %2d. %-9s %-13s %s\n", r.Priority+1, r.Scope, r.Status, r.Dir)
	}
	b.WriteString(dim("  project > custom > global > builtin; add custom roots via [skills] paths in reasonix.toml"))
	m.notice(b.String())
}

// skillStore builds a Store reflecting this session's project root + configured
// custom paths, for the management verbs that need to write or enumerate roots.
func (m *chatTUI) skillStore() *skill.Store {
	cwd, _ := os.Getwd()
	var custom []string
	if cfg, err := config.Load(); err == nil {
		custom = cfg.SkillCustomPaths()
	}
	return skill.New(skill.Options{ProjectRoot: cwd, CustomPaths: custom})
}

// runHooksSubcommand handles "/hooks": list the active hooks and the project's
// trust state, or trust the current project so its hooks load next session.
func (m *chatTUI) runHooksSubcommand(input string) {
	args := tokenizeArgs(input) // args[0] == "/hooks"
	sub := ""
	if len(args) > 1 {
		sub = strings.ToLower(args[1])
	}
	cwd, _ := os.Getwd()
	switch sub {
	case "", "list", "ls":
		m.hooksList(cwd)
	case "trust":
		if err := hook.Trust(cwd, ""); err != nil {
			m.notice("hooks trust: " + err.Error())
			return
		}
		m.notice("trusted this project's hooks — they load on the next /new or restart")
	default:
		m.notice("unknown /hooks subcommand " + args[1] + " — try: /hooks, /hooks trust")
	}
}

func (m *chatTUI) hooksList(cwd string) {
	var b strings.Builder
	active := m.ctrl.HookRunner().Hooks()
	fmt.Fprintf(&b, "%s\n", dim(fmt.Sprintf("  · hooks (%d active this session)", len(active))))
	for _, h := range active {
		match := h.Match
		if h.Event == hook.PreToolUse || h.Event == hook.PostToolUse {
			if match == "" {
				match = "*"
			}
		} else {
			match = "-"
		}
		cmd := h.Command
		if len([]rune(cmd)) > 50 {
			cmd = string([]rune(cmd)[:49]) + "…"
		}
		fmt.Fprintf(&b, "  %-16s %-8s %-8s %s\n", h.Event, h.Scope, match, cmd)
	}
	trusted := hook.IsTrusted(cwd, "")
	if hook.ProjectDefinesHooks(cwd) && !trusted {
		b.WriteString(dim("  this project defines hooks but is NOT trusted — run /hooks trust to enable them (security: project hooks run shell commands)"))
	} else {
		fmt.Fprintf(&b, "%s", dim(fmt.Sprintf("  project trusted: %v · config: project .reasonix/settings.json + global ~/.reasonix/settings.json", trusted)))
	}
	m.notice(b.String())
}

// containsArg reports whether flag appears in args.
func containsArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
