package cli

import (
	"os"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"reasonix/internal/i18n"
)

// compKind distinguishes the two completion menus.
type compKind int

const (
	compSlash compKind = iota // slash commands, when the line starts with "/"
	compAt                    // @-references (files / MCP resources)
)

// compItem is one menu row: label shown, insert applied on accept, hint dimmed.
// descend marks a directory entry — accepting it fills the input and re-opens
// the menu one level deeper instead of closing.
type compItem struct {
	label   string
	insert  string
	hint    string
	descend bool
}

// completion is the live autocomplete menu state. Empty value = inactive.
// replaceFrom is the byte offset in the input where the completed token starts
// (0 for a slash line, the '@' index for an @-reference).
type completion struct {
	active      bool
	kind        compKind
	items       []compItem
	sel         int
	replaceFrom int
}

const (
	// maxCompRows caps how many menu rows show at once; the list windows around
	// the selection when longer.
	maxCompRows = 8
	// maxCompItems caps how many entries a single directory contributes, so a
	// pathologically large directory can't blow up the menu — we read only one
	// level (os.ReadDir), never the whole tree.
	maxCompItems = 200
)

// slashItems is the full set of slash commands offered for completion.
func (m *chatTUI) slashItems() []compItem {
	items := []compItem{
		// Session management
		{label: "/compact", insert: "/compact ", hint: "compact context"},
		{label: "/new", insert: "/new ", hint: "fork a fresh session"},
		{label: "/clear", insert: "/clear ", hint: "clear context, keep config"},
		{label: "/branch", insert: "/branch ", hint: "fork session branch"},
		{label: "/tree", insert: "/tree ", hint: "show session tree"},
		{label: "/switch", insert: "/switch ", hint: "switch branch", descend: true},
		{label: "/resume", insert: "/resume ", hint: "switch to saved session"},
		// Tools & plugins
		{label: "/btw", insert: "/btw ", hint: "ask without saving to history"},
		{label: "/mcp", insert: "/mcp ", hint: "MCP servers", descend: true},
		{label: "/copy", insert: "/copy ", hint: "copy last response", descend: true},
		{label: "/goal", insert: "/goal ", hint: "set persistent goal", descend: true},
		// Diagnostics & config
		{label: "/doctor", insert: "/doctor ", hint: "diagnostics report", descend: true},
		{label: "/config", insert: "/config ", hint: "view configuration", descend: true},
		{label: "/init", insert: "/init ", hint: "analyze project"},
		{label: "/commands", insert: "/commands ", hint: "manage custom commands", descend: true},
		// Settings
		{label: "/effort", insert: "/effort ", hint: "thinking effort (auto|high|fast)", descend: true},
		{label: "/lang", insert: "/lang ", hint: "switch language (en|zh)", descend: true},
		{label: "/help", insert: "/help ", hint: "list commands"},
	}
	for _, c := range m.commands {
		desc := c.Description
		if desc == "" {
			desc = "custom command"
		}
		items = append(items, compItem{label: "/" + c.Name, insert: "/" + c.Name + " ", hint: desc})
	}
	for _, p := range m.prompts() {
		desc := p.Description
		if desc == "" {
			desc = "MCP prompt"
		}
		items = append(items, compItem{label: "/" + p.Name, insert: "/" + p.Name + " ", hint: desc})
	}
	return items
}

// updateCompletion recomputes the menu from the current input: a slash menu
// while the line is a single "/word" token, argument completions for known
// commands, or an @-reference menu while the token under the cursor is "@…".
func (m *chatTUI) updateCompletion() {
	val := m.input.Value()

	// Phase 1: slash command name completion (single /word, no spaces).
	// Uses fuzzy matching with frequency weighting so commonly-used commands surface first.
	if strings.HasPrefix(val, "/") && !strings.ContainsAny(val, " \t\n") {
		if items := filterByFuzzy(m.slashItems(), val, m.slashFreq); len(items) > 0 {
			m.setCompletion(compSlash, items, 0)
			return
		}
	}

	// Phase 2: argument completion for known commands ("/lang en|zh").
	if strings.HasPrefix(val, "/") {
		parts := strings.SplitN(val, " ", 2)
		if len(parts) == 2 {
			argPrefix := parts[1]
			if items := m.slashArgItems(parts[0]); len(items) > 0 {
				if filtered := filterByPrefix(items, argPrefix); len(filtered) > 0 {
					replaceFrom := len(parts[0]) + 1 // replace everything after "/cmd "
					m.setCompletion(compSlash, filtered, replaceFrom)
					return
				}
				// Show all options when arg prefix matches none.
				m.setCompletion(compSlash, items, len(parts[0])+1)
				return
			}
		}
	}

	if at, token, ok := activeAtToken(val); ok {
		if items := m.atItems(token); len(items) > 0 {
			m.setCompletion(compAt, items, at)
			return
		}
	}

	m.completion = completion{}
}

// slashArgItems returns argument completions for a slash command, or nil.
func (m *chatTUI) slashArgItems(cmd string) []compItem {
	switch cmd {
	case "/lang", "/language":
		return []compItem{
			{label: "en", insert: "en", hint: "English"},
			{label: "zh", insert: "zh", hint: "中文"},
		}
	case "/goal":
		return []compItem{
			{label: "status", insert: "status", hint: "show current goal"},
			{label: "cancel", insert: "cancel", hint: "cancel current goal"},
		}
	case "/copy":
		return []compItem{
			{label: "last", insert: "last", hint: "copy last assistant response"},
			{label: "all", insert: "all", hint: "copy full transcript"},
		}
	case "/switch":
		return m.switchArgItems()
	case "/mcp":
		return []compItem{
			{label: "status", insert: "status", hint: "show connected MCP servers"},
			{label: "prompts", insert: "prompts", hint: "list available MCP prompts"},
			{label: "resources", insert: "resources", hint: "list available MCP resources"},
			{label: "tools", insert: "tools", hint: "list available MCP tools"},
			{label: "import from cc-switch", insert: "import from cc-switch", hint: "import MCP servers from cc-switch"},
		}
	case "/commands":
		return []compItem{
			{label: "list", insert: "list", hint: "list custom commands"},
			{label: "create", insert: "create ", hint: "create a new command"},
			{label: "delete", insert: "delete ", hint: "delete a command"},
		}
	case "/config":
		return []compItem{
			{label: "show", insert: "show", hint: "show current configuration"},
		}
	case "/effort":
		return []compItem{
			{label: "auto", insert: "auto", hint: "model decides thinking depth (default)"},
			{label: "high", insert: "high", hint: "maximum thinking for complex tasks"},
			{label: "fast", insert: "fast", hint: "no thinking — direct answers"},
		}
	case "/doctor":
		return []compItem{
			{label: "all", insert: "all", hint: "run all diagnostics"},
			{label: "key", insert: "key", hint: "check API keys only"},
			{label: "network", insert: "network", hint: "check network only"},
			{label: "cache", insert: "cache", hint: "show cache hit-rate report"},
		}
	}
	// Auto-generate from custom command argument-hint.
	name := strings.TrimPrefix(cmd, "/")
	for _, c := range m.commands {
		if c.Name == name && c.ArgHint != "" {
			return []compItem{{label: c.ArgHint, insert: "", hint: c.Description}}
		}
	}
	return nil
}

// switchArgItems returns tree node completions for /switch.
func (m *chatTUI) switchArgItems() []compItem {
	nodes, labels, _ := m.ctrl.TreeInfo()
	if nodes == nil {
		return nil
	}
	var items []compItem
	for _, id := range nodes {
		label := labels[id]
		if label == "" {
			label = id
		}
		items = append(items, compItem{label: id, insert: id, hint: label})
	}
	return items
}

// setCompletion installs items, preserving the selection index only while the
// same menu kind stays open.
func (m *chatTUI) setCompletion(kind compKind, items []compItem, replaceFrom int) {
	sel := 0
	if m.completion.active && m.completion.kind == kind && m.completion.sel < len(items) {
		sel = m.completion.sel
	}
	m.completion = completion{active: true, kind: kind, items: items, sel: sel, replaceFrom: replaceFrom}
}

// fuzzyItem pairs a compItem with its match score for ranking.
type fuzzyItem struct {
	item  compItem
	score int
}

// filterByFuzzy returns items that match prefix via a multi-strategy fuzzy
// algorithm. Exact prefix matches come first; within the same match quality,
// frequently-used commands (freq) rank higher. For prefixes of 4+ chars,
// subsequence matching kicks in as a fallback.
func filterByFuzzy(items []compItem, prefix string, freq map[string]int) []compItem {
	lower := strings.ToLower(prefix)
	if lower == "" {
		return items
	}

	// freqBonus returns a per-command boost based on usage count. The
	// multiplier is tuned so frequently-used commands surface above
	// equally-matched peers but never beat a better-typed prefix.
	freqBonus := func(label string) int { return freq[label] * 3 }

	var prefixMatches []fuzzyItem
	var subseqMatches []fuzzyItem

	for _, it := range items {
		label := strings.ToLower(it.label)
		if strings.HasPrefix(label, lower) {
			score := -len(label) + freqBonus(it.label)
			prefixMatches = append(prefixMatches, fuzzyItem{it, score})
			continue
		}
		if len(lower) >= 4 {
			if s := subseqScore(label, lower); s > 0 {
				subseqMatches = append(subseqMatches, fuzzyItem{it, s + freqBonus(it.label)})
			}
		}
	}

	sortFuzzyDesc(prefixMatches)
	sortFuzzyDesc(subseqMatches)

	out := make([]compItem, 0, len(prefixMatches)+len(subseqMatches))
	for _, fi := range prefixMatches {
		out = append(out, fi.item)
	}
	for _, fi := range subseqMatches {
		out = append(out, fi.item)
	}
	return out
}

// freqMultiplier converts usage count to sort weight. Each use adds this many
// relative points — enough to float a favourite above unused peers of the
// same label length but not enough to beat a more-specific prefix match.
const freqMultiplier = 3

// subseqScore rates how well prefix matches label as a character subsequence.
// Returns 0 when not all prefix chars appear in order. The score rewards:
// contiguous runs, early first match, and tight spacing between chars.
func subseqScore(label, prefix string) int {
	runes := []rune(label)
	prev := -1
	score := 0
	consecutive := 0

	for _, pc := range prefix {
		found := -1
		for j := prev + 1; j < len(runes); j++ {
			if runes[j] == pc {
				found = j
				break
			}
		}
		// Also try from the start (allow restart — handles transpositions).
		if found < 0 {
			for j := 0; j <= prev && j < len(runes); j++ {
				if runes[j] == pc {
					found = j
					break
				}
			}
		}
		if found < 0 {
			return 0 // can't match this prefix char at all
		}

		if found == prev+1 {
			consecutive++
		} else {
			consecutive = 1
		}
		score += consecutive*10 + (len(runes) - found)
		prev = found
	}
	return score
}

// sortFuzzyDesc sorts items by score descending (higher score = better match).
func sortFuzzyDesc(items []fuzzyItem) {
	sort.Slice(items, func(i, j int) bool { return items[i].score > items[j].score })
}

// filterByPrefix keeps items whose label starts with prefix (case-insensitive).
// Kept for @-completion where fuzzy matching is undesirable (exact file paths).
func filterByPrefix(items []compItem, prefix string) []compItem {
	lp := strings.ToLower(prefix)
	var out []compItem
	for _, it := range items {
		if strings.HasPrefix(strings.ToLower(it.label), lp) {
			out = append(out, it)
		}
	}
	return out
}

// activeAtToken finds the @-reference token ending at the cursor (assumed at the
// input's end). The '@' must start the line or follow whitespace, so emails
// like "a@b" don't trigger it. Returns the '@' offset and the text after it.
func activeAtToken(val string) (int, string, bool) {
	for i := len(val) - 1; i >= 0; i-- {
		switch val[i] {
		case ' ', '\t', '\n':
			return 0, "", false // hit whitespace before an '@' → no active token
		case '@':
			if i == 0 || val[i-1] == ' ' || val[i-1] == '\t' || val[i-1] == '\n' {
				return i, val[i+1:], true
			}
			return 0, "", false
		}
	}
	return 0, "", false
}

// atItems builds the @-reference menu for a token. A "server:uri" token whose
// server is connected lists that server's MCP resources; otherwise the token is
// a path and we list one directory level (never a recursive walk), plus — at the
// top level — any matching MCP resources.
func (m *chatTUI) atItems(token string) []compItem {
	if i := strings.Index(token, ":"); i > 0 && m.isMCPServer(token[:i]) {
		return m.resourceItems(token[:i], token[i+1:])
	}
	return m.fileItems(token)
}

// fileItems lists one directory level for a path token. dir is the part up to
// the last '/', frag the part after; entries of dir starting with frag are
// offered (directories descend, files complete). Hidden entries are skipped
// unless frag starts with '.'. Top-level tokens also surface MCP resources.
func (m *chatTUI) fileItems(token string) []compItem {
	dir, frag := splitPathToken(token)
	readDir := dir
	if readDir == "" {
		readDir = "."
	}
	entries, err := os.ReadDir(readDir)
	if err != nil {
		entries = nil
	}
	// Directories first, then files; ReadDir is already name-sorted.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].IsDir() && !entries[j].IsDir()
	})

	showHidden := strings.HasPrefix(frag, ".")
	var items []compItem
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, frag) {
			continue
		}
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			items = append(items, compItem{label: name + "/", insert: "@" + dir + name + "/", hint: "dir", descend: true})
		} else {
			items = append(items, compItem{label: name, insert: "@" + dir + name})
		}
		if len(items) >= maxCompItems {
			break
		}
	}

	// At the top level (still naming the first segment) MCP resources share the
	// '@' namespace, so offer the matching ones too.
	if !strings.Contains(token, "/") {
		items = append(items, m.resourceItems("", token)...)
	}
	return items
}

// splitPathToken splits a path token into (dir, frag): dir keeps its trailing
// slash ("internal/" ), frag is the segment being typed.
func splitPathToken(token string) (dir, frag string) {
	if i := strings.LastIndex(token, "/"); i >= 0 {
		return token[:i+1], token[i+1:]
	}
	return "", token
}

// isMCPServer reports whether name is a connected MCP server.
func (m *chatTUI) isMCPServer(name string) bool {
	if m.host == nil {
		return false
	}
	for _, s := range m.host.ServerNames() {
		if s == name {
			return true
		}
	}
	return false
}

// resourceItems lists MCP resources as @server:uri completions. When server is
// "" (top level) it matches by the whole "server:uri" prefix; otherwise it lists
// the named server's resources filtered by the uri prefix.
func (m *chatTUI) resourceItems(server, frag string) []compItem {
	if m.host == nil {
		return nil
	}
	var items []compItem
	for _, r := range m.host.Resources() {
		ref := r.Server + ":" + r.URI
		switch {
		case server == "":
			if !strings.HasPrefix(ref, frag) {
				continue
			}
		case r.Server == server:
			if !strings.HasPrefix(r.URI, frag) {
				continue
			}
		default:
			continue
		}
		label := r.Name
		if label == "" {
			label = "resource"
		}
		items = append(items, compItem{label: "@" + ref, insert: "@" + ref, hint: label})
	}
	return items
}

// moveCompletion advances the selection by delta, wrapping around.
func (m *chatTUI) moveCompletion(delta int) {
	n := len(m.completion.items)
	if n == 0 {
		return
	}
	m.completion.sel = ((m.completion.sel+delta)%n + n) % n
}

// moveCompletionPage advances the selection by delta half-pages.
func (m *chatTUI) moveCompletionPage(delta int) {
	n := len(m.completion.items)
	if n == 0 {
		return
	}
	step := maxCompRows / 2
	if step < 1 {
		step = 1
	}
	m.completion.sel = ((m.completion.sel+delta*step)%n + n) % n
}

// acceptCompletion applies the selected item to the input. A directory descends
// (the input is filled and the menu re-opens one level deeper); anything else
// completes and closes the menu.
func (m *chatTUI) acceptCompletion() {
	if m.completion.sel >= len(m.completion.items) {
		m.completion = completion{}
		return
	}
	it := m.completion.items[m.completion.sel]
	val := m.input.Value()
	rf := m.completion.replaceFrom
	if rf > len(val) {
		rf = len(val)
	}
	m.input.SetValue(val[:rf] + it.insert)
	m.input.CursorEnd()
	if it.descend {
		m.updateCompletion() // re-list the directory we just descended into
		return
	}
	m.completion = completion{}
}

var compSelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("173")).Bold(true)

// compBg is the completion menu background, theme-adaptive via COLORFGBG.
var compBg = lipgloss.NewStyle().Background(lipgloss.Color(CompMenuBg()))

// compBgDim is the background used when an approval banner is active.
var compBgDim = lipgloss.NewStyle().Background(lipgloss.Color(CompMenuBg())).Faint(true)

// renderCompletion draws the menu above the input box: matching items, windowed
// around the selection, the current row highlighted, hints dimmed. When
// pendingApproval is non-nil the menu uses a dimmer background so the approval
// banner dominates.
func (m chatTUI) renderCompletion() string {
	if !m.completion.active || len(m.completion.items) == 0 {
		return ""
	}
	items := m.completion.items
	start := 0
	if len(items) > maxCompRows {
		start = m.completion.sel - maxCompRows/2
		if start < 0 {
			start = 0
		}
		if start > len(items)-maxCompRows {
			start = len(items) - maxCompRows
		}
	}
	end := start + maxCompRows
	if end > len(items) {
		end = len(items)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		it := items[i]
		if i == m.completion.sel {
			b.WriteString(accent("› ") + compSelStyle.Render(it.label))
		} else {
			b.WriteString("  " + it.label)
		}
		if it.hint != "" {
			b.WriteString("  " + dim(it.hint))
		}
		b.WriteByte('\n')
	}
	// A key-hint footer so users discover Tab — many won't know it accepts a
	// completion, let alone descends into a folder.
	hint := i18n.M.CompHintSlash
	if m.completion.kind == compAt {
		hint = i18n.M.CompHintFile
	}
	b.WriteString(dim(hint))
	bg := compBg
	if m.pendingApproval != nil {
		bg = compBgDim
	}
	return bg.Width(m.width - 4).Render(b.String())
}
