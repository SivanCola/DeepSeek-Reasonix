package cli

import (
	"context"
	"fmt"
	"image/color"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"reasonix/internal/agent"
	"reasonix/internal/agent/goal"
	"reasonix/internal/ccswitch"
	"reasonix/internal/command"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/plugin"
	"reasonix/internal/provider"
)

// chatTUI is a bubbletea Model that runs a chat session in the terminal's
// normal buffer (no alt-screen). Finalized output — user bubbles, tool dispatch
// lines, usage lines, reasoning, and the rendered assistant answer — is
// committed to the native scrollback via tea.Println, so the wheel, scrollbar,
// and copy all work like any CLI. The bubbletea-managed region is only the
// bottom — input box, status line, an optional approval/plan banner, and the
// autocomplete menu — and it is kept a stable height (it changes only on
// discrete user actions, never per streamed token) so the renderer commits
// scrollback cleanly without stranding the input box's border lines. This
// mirrors how Claude Code uses Ink's <Static> to freeze finished output into
// scrollback while re-rendering just the active prompt.
type chatTUI struct {
	ctrl       *control.Controller
	label      string
	missing    string // missing-key warning surfaced once in the banner, "" when ready
	sessionTag string

	width  int
	height int

	input   textarea.Model
	spinner spinner.Model

	state    tuiState
	runStart time.Time
	elapsed  int

	// planMode mirrors the agent's read-only gate (Tab toggles it). The marker
	// rides in outgoing user messages so the cache-stable prompt prefix is left
	// untouched.
	planMode bool

	// turnAccumulator collects this turn's assistant free text so the plan-mode
	// path can tell whether the turn produced a substantive proposal.
	turnAccumulator *strings.Builder

	// pendingPlan is non-empty while a plan-mode proposal awaits Enter-approval.
	pendingPlan string

	// history is a resumed session's messages, committed to scrollback once on
	// the first WindowSizeMsg so a reopened chat shows its prior transcript.
	history []provider.Message

	// reasoning is kept as a drain buffer for older call paths; reasoning events
	// are intentionally not rendered in chat scrollback by default. pending
	// accumulates the in-progress answer (raw markdown), then commits it to
	// scrollback markdown-rendered when finalized — at a tool/usage boundary or
	// turn end — not previewed live, so the bottom region stays a stable height.
	// pendingCommit queues finalized lines so a single Update emits exactly one
	// ordered tea.Println.
	reasoning     *strings.Builder
	pending       *strings.Builder
	pendingCommit *[]string
	renderer      *mdRenderer
	eventCh       chan event.Event
	started       bool // banner + resumed history committed once

	// pendingApproval holds the tool-call approval currently shown in the banner
	// (nil when none). While set, the controller's run goroutine is blocked
	// awaiting ctrl.Approve and key input is captured to answer it.
	pendingApproval *event.Approval

	// host is the running MCP servers (nil when no plugins). The TUI reads
	// prompts (slash commands), resources (@-references), and server status
	// (/mcp) from it.
	host *plugin.Host

	// commands are custom slash commands loaded from .reasonix/commands; each renders
	// its template with the typed args and sends the result as a turn.
	commands []command.Command

	// completion is the live autocomplete menu (slash commands; @-refs later).
	completion completion

	// slashFreq tracks per-command usage so frequently-used commands surface first.
	slashFreq map[string]int

	// picker, when non-nil, shows the session picker for /resume.
	picker *sessionPicker

	// compaction progress
	compacting       bool
	compactProgress  float64
	compactTipIdx    int
	compactLastPhase string

	// lastUsage feeds the compact OpenCode-style status line. It is display-only:
	// it never touches the model session or cache prefix.
	lastUsage            *provider.Usage
	lastCacheDiagnostics *event.CacheDiagnostics

	// bell (Phase 4)
	enableBell bool
}

type sessionPicker struct {
	sessions  []agent.SessionInfo
	highlight int
}

type tuiState int

const (
	tuiIdle tuiState = iota
	tuiRunning
)

// agentEventMsg is one typed event from the agent's run loop.
type agentEventMsg event.Event

// elapsedTickMsg fires once a second while a turn runs, driving the "thinking
// Ns" counter in the status line.
type elapsedTickMsg struct{}

// promptResolvedMsg carries the result of fetching an MCP prompt (an async
// prompts/get). display is the command line echoed as the user bubble; sent is
// the rendered prompt text that becomes the model turn.
type promptResolvedMsg struct {
	display string
	sent    string
	err     error
}

// refsResolvedMsg carries the result of resolving the @references in a
// submitted line (async file reads / MCP resources/read).
type refsResolvedMsg struct {
	line  string
	block string
	errs  []string
}

// newChatTUI assembles the initial model. The controller has already been wired
// with an event sink that feeds eventCh; the TUI issues commands to it and
// renders the events it emits. Label, history, host, and commands are read from
// the controller, so a resumed session pre-populates scrollback.
func newChatTUI(ctrl *control.Controller, missing string, eventCh chan event.Event, termW int) chatTUI {
	ti := textarea.New()
	configureChatTextarea(&ti)
	// Plain Enter submits (the chatTUI handler intercepts it), so the textarea's
	// own InsertNewline binding moves to Alt+Enter / Ctrl+J.
	ti.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("173"))

	commitBuf := []string{}
	return chatTUI{
		ctrl:            ctrl,
		label:           ctrl.Label(),
		missing:         missing,
		input:           ti,
		spinner:         sp,
		reasoning:       &strings.Builder{},
		pending:         &strings.Builder{},
		pendingCommit:   &commitBuf,
		turnAccumulator: &strings.Builder{},
		renderer:        newMarkdownRenderer(termW),
		eventCh:         eventCh,
		history:         ctrl.History(),
		host:            ctrl.Host(),
		commands:        ctrl.Commands(),
		slashFreq:       map[string]int{},
		enableBell:      true,
	}
}

func configureChatTextarea(ti *textarea.Model) {
	ti.Prompt = "› "
	ti.CharLimit = 16384
	ti.SetHeight(1)
	ti.ShowLineNumbers = false
	ti.EndOfBufferCharacter = ' '
	// Use the real terminal cursor (not a styled virtual one) so View can place
	// it at the insertion point and IME candidate windows anchor to the input.
	ti.SetVirtualCursor(false)

	styles := ti.Styles()
	plain := lipgloss.NewStyle()
	prompt := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	for _, st := range []*textarea.StyleState{&styles.Focused, &styles.Blurred} {
		st.CursorLine = plain
		st.CursorLineNumber = plain
		st.EndOfBuffer = plain
		st.LineNumber = plain
		st.Prompt = prompt
	}
	ti.SetStyles(styles)
}

// prompts returns the MCP prompts discovered at startup (nil when no plugins).
func (m *chatTUI) prompts() []plugin.Prompt {
	if m.host == nil {
		return nil
	}
	return m.host.Prompts()
}

func (m chatTUI) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		waitForAgentEvent(m.eventCh),
	)
}

func (m chatTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(msg.Width - 4)
		m.renderer = newMarkdownRenderer(msg.Width)
		// Commit the banner — and a resumed session's transcript — to scrollback
		// once, now that the width is known.
		if !m.started {
			m.started = true
			var b strings.Builder
			b.WriteString(renderTUIBanner(m.label, m.missing, msg.Width))
			if len(m.history) > 0 {
				r := newMarkdownRenderer(msg.Width)
				for _, sec := range replaySectionsFor(m.history, msg.Width, r) {
					b.WriteString(sec)
				}
				m.history = nil
			}
			m.commitLine(strings.TrimRight(b.String(), "\n"))
		}

	case tea.MouseMsg:
		switch mouse := msg.Mouse(); mouse.Button {
		case tea.MouseWheelUp, tea.MouseWheelDown:
			// In normal-buffer mode the terminal's native scrollback handles
			// wheel events for committed output; we let the event pass through.
		}

	case tea.KeyPressMsg:
		// A pending tool approval is modal: keystrokes answer it (y/a/n, Enter,
		// Esc) rather than reaching the input.
		if m.pendingApproval != nil {
			return m.handleApprovalKey(msg)
		}
		// Intercept Ctrl+V: if the clipboard holds an image, save to a temp
		// file and inject an @-reference so the image can be included in the
		// next message. Text paste falls through to the textarea normally.
		if msg.String() == "ctrl+v" && m.handleCtrlV() {
			return m, nil
		}
		// While the autocomplete menu is open it captures navigation/accept keys
		// (↑/↓ move, Tab/Enter accept, Esc close); everything else falls through
		// to the textarea and re-filters the menu at the end of Update.
		if m.completion.active {
			switch msg.String() {
			case "up":
				m.moveCompletion(-1)
				return m, nil
			case "down":
				m.moveCompletion(1)
				return m, nil
			case "pgup":
				m.moveCompletionPage(-1)
				return m, nil
			case "pgdown":
				m.moveCompletionPage(1)
				return m, nil
			case "tab", "enter":
				m.acceptCompletion()
				return m, nil
			case "esc":
				m.completion = completion{}
				return m, nil
			}
		}
		switch msg.String() {
		case "esc":
			// "Back out" of the most specific in-progress state: cancel a turn,
			// turn plan mode off, or clear typed-but-unsent input. Scrollback is
			// the terminal's now, so there's no viewport to dismiss.
			switch {
			case m.state == tuiRunning:
				m.ctrl.Cancel()
			case m.pendingPlan != "":
				m.pendingPlan = ""
			case m.planMode:
				m.planMode = false
				m.ctrl.SetPlanMode(false)
			default:
				m.input.Reset()
			}
			return m, nil
		case "ctrl+c":
			if m.state == tuiRunning {
				m.ctrl.Cancel()
				return m, nil
			}
			return m, tea.Quit
		case "ctrl+d":
			return m, tea.Quit
		case "tab":
			if m.state == tuiRunning {
				break
			}
			m.planMode = !m.planMode
			m.ctrl.SetPlanMode(m.planMode)
			return m, nil
		case "enter":
			if m.state == tuiRunning {
				return m, nil // ignore Enter while a turn is in flight
			}
			line := strings.TrimSpace(m.input.Value())

			// Empty Enter while a plan is pending approves it and auto-sends a
			// brief "proceed" message.
			if line == "" && m.pendingPlan != "" {
				m.pendingPlan = ""
				m.planMode = false
				m.ctrl.SetPlanMode(false)
				cmds = append(cmds, m.startTurn("Plan approved — proceed with the steps you laid out, executing as needed.", "(plan approved — executing)"))
				return m, finalize(m, cmds)
			}

			if line == "" {
				return m, nil
			}
			if line == "exit" || line == "quit" || line == ":q" {
				return m, tea.Quit
			}

			// Slash commands run locally without going through the model.
			if strings.HasPrefix(line, "/") {
				m.input.Reset()
				m.input.SetHeight(1)
				cmds = append(cmds, m.runSlashCommand(line))
				return m, finalize(m, cmds)
			}

			// A non-empty submission while a plan is pending counts as feedback.
			m.pendingPlan = ""
			m.input.Reset()
			m.input.SetHeight(1)

			// @references (local files / MCP resources) are resolved off the event
			// loop by the controller; the turn starts when they resolve
			// (refsResolvedMsg).
			if m.ctrl.HasRefs(line) {
				cmds = append(cmds, m.resolveRefs(line))
				return m, finalize(m, cmds)
			}

			cmds = append(cmds, m.startTurn(m.ctrl.Compose(line), line))
			return m, finalize(m, cmds)
		}

	case agentEventMsg:
		m.ingestEvent(event.Event(msg))
		cmds = append(cmds, waitForAgentEvent(m.eventCh))

	case promptResolvedMsg:
		switch {
		case msg.err != nil:
			m.commitLine(wrapForViewport(i18n.M.ErrorPrefix+" "+msg.err.Error(), m.width, lipgloss.Color("3")))
		case strings.TrimSpace(msg.sent) == "":
			m.notice(i18n.M.SlashPromptEmpty)
		default:
			cmds = append(cmds, m.startTurn(m.ctrl.Compose(msg.sent), msg.display))
		}

	case refsResolvedMsg:
		for _, e := range msg.errs {
			m.notice(e) // surface a fetch failure but still send the turn
		}
		sent := msg.line
		if msg.block != "" {
			sent = "Referenced context:\n\n" + msg.block + "\n\n" + msg.line
		}
		cmds = append(cmds, m.startTurn(m.ctrl.Compose(sent), msg.line))

	case elapsedTickMsg:
		if m.state == tuiRunning {
			m.elapsed = int(time.Since(m.runStart).Seconds())
			cmds = append(cmds, elapsedTick())
		}

	case spinner.TickMsg:
		if m.state == tuiRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	var ic tea.Cmd
	m.input, ic = m.input.Update(msg)
	cmds = append(cmds, ic)
	m.growInputToFit()
	// Re-filter the autocomplete menu against the freshly-edited input.
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.updateCompletion()
	}

	return m, finalize(m, cmds)
}

// finalize flushes any queued scrollback lines into a single ordered tea.Println
// (Batch doesn't preserve order across multiple Println cmds, so we coalesce per
// Update) and batches it with the turn's other commands.
func finalize(m chatTUI, cmds []tea.Cmd) tea.Cmd {
	if len(*m.pendingCommit) > 0 {
		out := strings.TrimRight(clampWidth(strings.Join(*m.pendingCommit, "\n"), m.width), "\n")
		*m.pendingCommit = (*m.pendingCommit)[:0]
		// Commit in screen-bounded chunks. v2's inline renderer commits scrollback
		// via insertAbove, which scrolls the screen and InsertLine()s by the
		// block's line count; a single block taller than the screen makes its
		// CursorUp clamp at the top and the inserts misalign — the whole frame
		// (input box, banner) corrupts. Splitting so each Println is at most a
		// screenful keeps insertAbove within bounds. Sequence preserves order
		// (Batch does not across multiple Printlns).
		var prints []tea.Cmd
		for _, chunk := range chunkLines(out, m.scrollChunkHeight()) {
			prints = append(prints, tea.Println(chunk))
		}
		cmds = append(cmds, tea.Sequence(prints...))
	}
	return tea.Batch(cmds...)
}

// scrollChunkHeight is the largest block (in lines) finalize prints at once so
// v2's insertAbove stays within the screen. It leaves room for the pinned
// bottom frame (input box + status). Falls back to a generous default before
// the first WindowSizeMsg sets the height.
func (m chatTUI) scrollChunkHeight() int {
	if m.height <= 0 {
		return 100
	}
	if n := m.height - 5; n > 1 {
		return n
	}
	return 1
}

// chunkLines splits s into blocks of at most n lines each, preserving order and
// line content. A single block is returned when it already fits.
func chunkLines(s string, n int) []string {
	if n < 1 {
		n = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(lines); i += n {
		end := i + n
		if end > len(lines) {
			end = len(lines)
		}
		out = append(out, strings.Join(lines[i:end], "\n"))
	}
	return out
}

// clampWidth hard-breaks any line wider than width so no scrollback line wraps
// in the terminal. bubbletea's inline renderer estimates how far to scroll for
// each printed block from each line's width (insertAbove: offset += width/w); an
// over-wide line that the terminal wraps throws that estimate off and drifts the
// pinned input box off-screen. Lines already within width are left byte-for-byte
// untouched (chunkByWidth preserves content and ANSI), so rendered tables and the
// wrapped answer — which the markdown renderer already fit to width — are safe;
// only stray long lines (tool-dispatch args, unwrapped code) get broken.
func clampWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		if visibleWidth(line) > width {
			b.WriteString(strings.Join(chunkByWidth(line, width), "\n"))
		} else {
			b.WriteString(line)
		}
	}
	return b.String()
}

// commitLine queues one finalized block for the next scrollback flush.
func (m *chatTUI) commitLine(s string) {
	*m.pendingCommit = append(*m.pendingCommit, s)
}

// commitReasoning discards any accumulated thinking stream. Reasoning remains
// available to the agent/provider for round-tripping, but the TUI transcript
// hides it by default.
func (m *chatTUI) commitReasoning() {
	if m.reasoning.Len() == 0 {
		return
	}
	m.reasoning.Reset()
}

// commitPending renders the accumulated answer as markdown and freezes it into
// scrollback. Joining commitReasoning then commitPending puts the answer on its
// own line, restoring the thinking→answer break the renderer strips.
func (m *chatTUI) commitPending() {
	if m.pending.Len() == 0 {
		return
	}
	raw := m.pending.String()
	rendered := m.renderer.Render(raw)
	if rendered == "" {
		rendered = raw
	}
	m.commitLine(strings.TrimRight(rendered, "\n"))
	m.pending.Reset()
}

// handleApprovalKey resolves a pending tool-call approval from a keystroke and
// re-arms the approval listener. y/Enter allows once, a allows for the rest of
// the session, n/Esc denies. Ctrl-C cancels the whole turn via the run context.
func (m chatTUI) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	answer := func(allow, session bool) (tea.Model, tea.Cmd) {
		m.ctrl.Approve(m.pendingApproval.ID, allow, session)
		m.pendingApproval = nil
		return m, nil // the next ApprovalRequest / event arrives on eventCh
	}
	switch msg.String() {
	case "ctrl+c":
		m.ctrl.Cancel() // cancels the run; the approver unblocks via ctx.Done()
		return answer(false, false)
	case "enter":
		return answer(true, false)
	case "esc":
		return answer(false, false)
	}
	switch strings.ToLower(msg.String()) {
	case "y", "1":
		return answer(true, false)
	case "a", "2":
		return answer(true, true)
	case "n", "3":
		return answer(false, false)
	}
	return m, nil // ignore anything else while awaiting a decision
}

var (
	// Input box: only top + bottom borders, no sides or interior padding.
	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, true, false).
			BorderForeground(lipgloss.Color("173"))

	// Approval banner: same frame as the input box, recoloured yellow.
	approvalBannerStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), true, false, true, false).
				BorderForeground(lipgloss.Color("220")).
				Foreground(lipgloss.Color("220")).
				Bold(true).
				PaddingLeft(1)

	// usageFooter colors are intentionally low-saturation: usage metadata should
	// read as a quiet annotation, not a status alert.
	usageFooterGray   = "244"
	usageFooterGreen  = "108"
	usageFooterYellow = "179"
	usageFooterOrange = "173"
)

func usageFooterColor(u *provider.Usage, diag *event.CacheDiagnostics) string {
	if diag != nil && diag.PrefixChanged {
		return usageFooterOrange
	}
	if u == nil || u.PromptTokens == 0 {
		return usageFooterGray
	}
	cached := u.CacheHitTokens
	fresh := u.CacheMissTokens
	if fresh == 0 {
		if d := u.PromptTokens - cached; d > 0 {
			fresh = d
		}
	}
	if cached+fresh == 0 {
		return usageFooterGray
	}
	pct := cached * 100 / (cached + fresh)
	switch {
	case pct >= 80:
		return usageFooterGreen
	case pct >= 50:
		return usageFooterYellow
	default:
		return usageFooterOrange
	}
}

func usageFooterStyleFor(u *provider.Usage, diag *event.CacheDiagnostics) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(usageFooterColor(u, diag))).
		Faint(true)
}

func cachePercent(u *provider.Usage) (int, bool) {
	if u == nil || u.PromptTokens == 0 {
		return 0, false
	}
	cached := u.CacheHitTokens
	fresh := u.CacheMissTokens
	if fresh == 0 {
		if d := u.PromptTokens - cached; d > 0 {
			fresh = d
		}
	}
	if cached+fresh == 0 {
		return 0, false
	}
	return cached * 100 / (cached + fresh), true
}

func renderInputFrameTag(label string, width int) string {
	label = strings.TrimSpace(label)
	if label == "" || width < 12 {
		return ""
	}
	maxLabel := width - 4
	if visibleWidth(label) > maxLabel {
		label = truncateTo(label, maxLabel)
	}
	tag := lipgloss.NewStyle().
		Foreground(lipgloss.Color("16")).
		Background(lipgloss.Color("31")).
		Padding(0, 1).
		Render(label)
	pad := width - visibleWidth(tag)
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + tag
}

func renderInputBoxWithTag(box, tag string, width int) string {
	if tag == "" {
		return box
	}
	lines := strings.Split(box, "\n")
	if len(lines) == 0 {
		return box
	}
	styledTag := strings.TrimLeft(tag, " ")
	start := width - visibleWidth(styledTag)
	if start < 0 {
		start = 0
	}
	cleanLine := ansiSGR.ReplaceAllString(lines[0], "")
	r := []rune(cleanLine)
	if start < len(r) {
		lines[0] = string(r[:start]) + styledTag
	} else {
		lines[0] = padRight(cleanLine, start) + styledTag
	}
	return strings.Join(lines, "\n")
}

func (m chatTUI) View() tea.View {
	boxW := m.width
	if boxW < 10 {
		boxW = 10
	}
	inputStyle := inputBoxStyle
	if m.pendingApproval != nil {
		inputStyle = inputBoxStyle.Foreground(lipgloss.Color("240"))
	}
	box := inputStyle.Width(boxW).Render(m.input.View())
	if tag := m.inputFrameTag(); tag != "" {
		box = renderInputBoxWithTag(box, tag, boxW)
	}

	var parts []string
	rowsAboveBox := 0

	if m.pendingApproval != nil {
		overlay := m.renderApprovalOverlay()
		parts = append(parts, overlay)
		rowsAboveBox += strings.Count(overlay, "\n") + 1
	} else if banner := m.renderApprovalBanner(); banner != "" {
		parts = append(parts, banner)
		rowsAboveBox += strings.Count(banner, "\n") + 1
	}

	if menu := m.renderCompletion(); menu != "" {
		parts = append(parts, menu)
		rowsAboveBox += strings.Count(menu, "\n") + 1
	}
	if picker := m.renderSessionPicker(); picker != "" {
		parts = append(parts, picker)
		rowsAboveBox += strings.Count(picker, "\n") + 1
	}
	parts = append(parts, box)
	parts = append(parts, m.renderStatusBar())

	content := strings.Join(parts, "\n")
	topPad := m.bottomDockTopPadding(content)
	if topPad > 0 {
		content = strings.Repeat("\n", topPad) + content
	}
	v := tea.NewView(content)
	if cur := m.input.Cursor(); cur != nil {
		cur.Y += topPad + rowsAboveBox + 1
		v.Cursor = cur
	}
	return v
}

func (m chatTUI) bottomDockTopPadding(content string) int {
	if m.height <= 0 {
		return 0
	}
	rows := viewLineCount(strings.TrimRight(content, "\n"))
	if rows >= m.height {
		return 0
	}
	return m.height - rows
}

func viewLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// renderCompactProgress returns the compaction progress bar block (3 lines).
func (m chatTUI) renderCompactProgress() string {
	title := dim("\u2733 " + i18n.M.CompactProgressTitle)
	barW := 40
	filled := int(m.compactProgress * float64(barW))
	if filled < 0 {
		filled = 0
	}
	if filled > barW {
		filled = barW
	}
	filledBar := accent(strings.Repeat("\u25b0", filled))
	emptyBar := strings.Repeat("\u25b1", barW-filled)
	bar := fmt.Sprintf("  %s%s %d%%", filledBar, emptyBar, int(m.compactProgress*100))
	tip := ""
	tips := strings.Split(i18n.M.CompactTips, "\n")
	if m.compactTipIdx >= 0 && m.compactTipIdx < len(tips) {
		tip = dim("  \u23bf  " + tips[m.compactTipIdx])
	}
	return title + "\n" + bar + "\n" + tip
}

func (m *chatTUI) bell() {
	if m.enableBell {
		fmt.Print("\a")
	}
}

// renderStatusBar returns a single-line status bar for the bottom region.
//
//	[plan]  12K/128K ctx (9%) │ deepseek-v4 ● thinking 3s     (running)
//	[auto]  12K/128K ctx (9%) │ Tab=plan · Enter=send         (idle)
//	Compacting ▰▰▰▱▱▱ 52%                                     (compacting)
func (m chatTUI) renderStatusBar() string {
	w := m.width
	if w < 10 {
		w = 10
	}

	// Compacting state: single-line progress bar replaces the whole status.
	if m.compacting {
		barW := 20
		filled := int(m.compactProgress * float64(barW))
		if filled < 0 {
			filled = 0
		}
		if filled > barW {
			filled = barW
		}
		filledBar := accent(strings.Repeat("▰", filled))
		emptyBar := strings.Repeat("▱", barW-filled)
		return fmt.Sprintf("  Compacting %s%s %d%%", filledBar, emptyBar, int(m.compactProgress*100))
	}

	leftParts := []string{}
	if strings.TrimSpace(m.label) != "" {
		leftParts = append(leftParts, lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Render(m.label))
	}
	if pct, ok := cachePercent(m.lastUsage); ok {
		cacheStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(usageFooterColor(m.lastUsage, m.lastCacheDiagnostics)))
		leftParts = append(leftParts, cacheStyle.Render(fmt.Sprintf("hit %d%%", pct)))
	}
	brand := "DeepSeek-Reasonix"
	if m.planMode {
		brand += " (plan)"
	}
	leftParts = append(leftParts, lipgloss.NewStyle().Foreground(lipgloss.Color("37")).Render(brand))
	left := strings.Join(leftParts, dim(" | "))
	if ctx := m.contextTag(); ctx != "" {
		left += dim(" | ") + ctx
	}

	var right string
	switch {
	case m.pendingApproval != nil:
		right = yellow("[1] approve once  [2] allow session  [3] deny")
	case m.state == tuiRunning:
		right = dim(fmt.Sprintf(i18n.M.ChatStatusThinkingFmt, m.spinner.View(), m.elapsed))
	case m.pendingPlan != "":
		right = dim(i18n.M.ChatStatusPlanApproval)
	default:
		right = dim(i18n.M.ChatStatusIdleCompact)
	}

	sep := dim(" │ ")
	lw := visibleWidth(left)
	rw := visibleWidth(right) + visibleWidth(sep)
	innerW := w - 2 // 2-char left indent
	if lw+rw <= innerW {
		return "  " + left + strings.Repeat(" ", innerW-lw-rw) + sep + right
	}
	if lw <= innerW {
		return "  " + left
	}
	return left
}

func (m chatTUI) inputFrameTag() string {
	label := strings.TrimSpace(m.sessionTag)
	if label == "" || label == "root" {
		return ""
	}
	return renderInputFrameTag(label, m.width)
}

func (m *chatTUI) syncSessionTagFromTree() {
	if m.ctrl == nil {
		m.sessionTag = ""
		return
	}
	_, labels, current := m.ctrl.TreeInfo()
	label := strings.TrimSpace(labels[current])
	if label == "" || label == "root" {
		m.sessionTag = ""
		return
	}
	m.sessionTag = label
}

// renderApprovalOverlay renders a prominent approval dialog that replaces the
// normal banner when a tool call awaits the user's decision.
func (m chatTUI) renderApprovalOverlay() string {
	w := m.width
	if w < 40 {
		w = 40
	}
	a := m.pendingApproval
	subj := strings.TrimSpace(a.Subject)
	if subj != "" {
		subj = " " + subj
	}
	title := yellow("⏸  Tool Approval Required")
	body := fmt.Sprintf("Allow %s%s?", bold(a.Tool), dim(subj))
	hint := dim("  [1] approve once  ·  [2] allow this session  ·  [3] deny  ·  Ctrl-C cancel turn")
	innerW := w - 8
	if innerW < 30 {
		innerW = 30
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(dim(strings.Repeat("─", w)) + "\n")
	b.WriteString("  " + title + "\n\n")
	b.WriteString("  " + body + "\n\n")
	b.WriteString(hint + "\n")
	b.WriteString(dim(strings.Repeat("─", w)))
	return b.String()
}

// contextTag renders the prompt-vs-context-window gauge for the status line.
func (m chatTUI) contextTag() string {
	if m.ctrl == nil {
		return ""
	}
	used, window := m.ctrl.ContextSnapshot()
	return formatContextTag(used, window)
}

func formatContextTag(used, window int) string {
	if used == 0 || window == 0 {
		return ""
	}
	pct := used * 100 / window
	body := fmt.Sprintf("ctx %s / %s (%d%%)", shortTokens(used), shortTokens(window), pct)
	switch {
	case pct >= 85:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(body)
	case pct >= 60:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render(body)
	default:
		return dim(body)
	}
}

// shortTokens prints token counts compactly: 142_000 → "142K", 1_000_000 → "1M".
func shortTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// renderApprovalBanner is the slim notice shown above the input while a tool
// call (or a plan) awaits the user's decision.
func (m chatTUI) renderApprovalBanner() string {
	w := m.width
	if w < 10 {
		w = 10
	}
	if m.pendingApproval != nil {
		subj := strings.TrimSpace(m.pendingApproval.Subject)
		if subj != "" {
			subj = " " + truncateSubject(subj, w)
		}
		text := fmt.Sprintf(i18n.M.ToolApprovalPromptFmt, m.pendingApproval.Tool, subj)
		return approvalBannerStyle.Width(w).Render("⏸ " + text)
	}
	if m.pendingPlan == "" {
		return ""
	}
	return approvalBannerStyle.Width(w).Render("⏸ " + i18n.M.PlanApprovalPrompt)
}

// truncateSubject trims a tool subject so the approval banner fits one line.
func truncateSubject(s string, width int) string {
	max := width - 28
	if max < 16 {
		max = 16
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// growInputToFit resizes the textarea to the number of lines its value spans,
// capped at maxInputRows so a long paste doesn't crowd the screen.
const maxInputRows = 5

func (m *chatTUI) growInputToFit() {
	lines := strings.Count(m.input.Value(), "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > maxInputRows {
		lines = maxInputRows
	}
	if lines != m.input.Height() {
		m.input.SetHeight(lines)
	}
}

// startTurn commits the user bubble to scrollback, resets the turn accumulator,
// and kicks off runner.Run. `sent` goes to the model (may carry a plan-mode
// marker); `displayed` is what the transcript shows.
func (m *chatTUI) startTurn(sent, displayed string) tea.Cmd {
	// Flush any half-streamed leftover before the new turn (defensive).
	m.commitReasoning()
	m.commitPending()
	m.turnAccumulator.Reset()
	m.commitLine("") // blank line separating turns
	m.commitLine(renderUserBubble(displayed, m.width, m.planMode))

	m.state = tuiRunning
	m.runStart = time.Now()
	m.elapsed = 0
	// The controller owns the run goroutine, its context, and cancellation; it
	// streams events to eventCh and emits TurnDone when the turn settles.
	m.ctrl.Send(sent)
	return tea.Batch(m.spinner.Tick, elapsedTick())
}

// ingestEvent routes one typed event from the agent. Reasoning events are
// consumed but hidden from scrollback by default; visible answer text
// accumulates in its live buffer. Every other event first finalizes the visible
// answer streamed so far, then commits its own line — preserving order.
// Switching on the event Kind replaces the old prefix-sniffing of a flattened
// byte stream: the structure is now explicit.
func (m *chatTUI) ingestEvent(e event.Event) {
	switch e.Kind {
	case event.Reasoning:
		// The status bar already tells the user the model is thinking; the raw
		// reasoning stream should not become part of the visible transcript.

	case event.Text:
		m.commitReasoning() // reasoning ends as the answer begins
		m.pending.WriteString(e.Text)
		m.turnAccumulator.WriteString(e.Text)

	case event.Message:
		m.commitReasoning()
		m.commitPending()

	case event.ToolDispatch:
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("  -> %s %s", e.Tool.Name, compactArgs(e.Tool.Args)))

	case event.ToolResult:
		if e.Tool.Err != "" {
			m.finalizeStreamed()
			m.commitLine(fmt.Sprintf("  ⊘ %s %s", e.Tool.Name, e.Tool.Err))
		} else if e.Tool.Output != "" && isUnifiedDiff(e.Tool.Output) {
			m.finalizeStreamed()
			m.commitLine(colorizeDiff(e.Tool.Output))
		}

	case event.Usage:
		m.lastUsage = e.Usage
		m.lastCacheDiagnostics = e.CacheDiagnostics
		if line := agent.FormatUsageLine(e.Usage, e.Pricing, e.CacheDiagnostics, false); line != "" {
			m.finalizeStreamed()
			m.commitLine(usageFooterStyleFor(e.Usage, e.CacheDiagnostics).Render(line))
		}

	case event.Notice:
		glyph := "·"
		if e.Level == event.LevelWarn {
			glyph = "!"
		}
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("  %s %s", glyph, e.Text))

	case event.Phase:
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("[%s]", e.Text))

	case event.ApprovalRequest:
		a := e.Approval
		m.pendingApproval = &a
		m.bell()

	case event.TurnDone:
		m.commitReasoning()
		m.commitPending()
		m.state = tuiIdle
		_ = m.ctrl.Snapshot()
		if e.Err != nil && e.Err.Error() != "" && !strings.Contains(e.Err.Error(), "context canceled") {
			m.commitLine(wrapForViewport(i18n.M.ErrorPrefix+" "+e.Err.Error(), m.width, lipgloss.Color("3")))
		}
		if e.Err == nil && m.planMode && strings.TrimSpace(m.turnAccumulator.String()) != "" {
			m.pendingPlan = "pending"
		}
		if m.elapsed > 10 {
			m.bell()
		}
	}
}

// finalizeStreamed freezes any in-progress reasoning + answer into scrollback so
// a following event line lands after them, preserving chronological order.
func (m *chatTUI) finalizeStreamed() {
	m.commitReasoning()
	m.commitPending()
}

func waitForAgentEvent(ch chan event.Event) tea.Cmd {
	return func() tea.Msg { return agentEventMsg(<-ch) }
}

func elapsedTick() tea.Cmd {
	return tea.Tick(time.Second, func(_ time.Time) tea.Msg { return elapsedTickMsg{} })
}

// runSlashCommand handles "/<cmd> <args>" input. Local commands queue their
// output to scrollback; MCP prompt / custom commands resolve to a model turn.
func (m *chatTUI) runSlashCommand(input string) tea.Cmd {
	cmd := strings.TrimSpace(strings.SplitN(input, " ", 2)[0])

	if strings.HasPrefix(cmd, "/mcp__") {
		m.slashFreq[cmd]++
		return m.runMCPPrompt(input)
	}

	if cmd != "" {
		m.slashFreq[cmd]++
	}

	switch cmd {
	case "/compact":
		go func() {
			if err := m.ctrl.Compact(context.Background()); err != nil {
			}
			_ = m.ctrl.Snapshot()
		}()
		return nil
	case "/new":
		if err := m.ctrl.NewSession(); err != nil {
			m.notice(fmt.Sprintf("%s: %v", i18n.M.SlashNewFailed, err))
			return nil
		}
		m.pending.Reset()
		m.reasoning.Reset()
		m.turnAccumulator.Reset()
		m.pendingPlan = ""
		m.sessionTag = ""
		m.commitLine("")
		m.commitLine(strings.TrimRight(renderTUIBanner(m.label, "", m.width), "\n"))
		m.notice(i18n.M.SlashNewDone)
	case "/clear":
		m.runClear()
	case "/branch":
		m.runBranch(input)
	case "/tree":
		m.showTree()
	case "/switch":
		m.runSwitch(input)
	case "/resume":
		m.runResume()
		return nil
	case "/btw":
		return m.runBtw(input)
	case "/mcp":
		m.runMCP(input)
	case "/copy":
		m.runCopy(input)
	case "/goal":
		m.runGoal(input)
	case "/cache-report", "/cache":
		m.runDoctor("/doctor cache")
	case "/doctor":
		m.runDoctor(input)
	case "/config":
		m.runConfig()
	case "/init":
		m.runInit()
	case "/commands":
		m.runCommands(input)
	case "/effort":
		m.runEffort(input)
	case "/lang", "/language":
		m.runLang(input)
	case "/help":
		m.showHelp()
	default:
		if sent, ok := m.ctrl.CustomCommand(input); ok {
			return m.startTurn(m.ctrl.Compose(sent), input)
		}
		m.slashFreq[cmd]--
		m.notice(fmt.Sprintf("%s: %s", i18n.M.SlashUnknown, cmd))
	}
	return nil
}

// runClear resets session context, keeping the same session file.
func (m *chatTUI) runClear() {
	if m.state == tuiRunning {
		m.notice(i18n.M.SlashClearRunning)
		return
	}
	if err := m.ctrl.ClearSession(); err != nil {
		m.notice(fmt.Sprintf("%s: %v", i18n.M.SlashNewFailed, err))
		return
	}
	m.pending.Reset()
	m.reasoning.Reset()
	m.turnAccumulator.Reset()
	m.pendingPlan = ""
	m.sessionTag = ""
	m.commitLine("")
	m.commitLine(strings.TrimRight(renderTUIBanner(m.label, "", m.width), "\n"))
	m.notice(i18n.M.SlashClearDone)
}

// showHelp prints a structured command listing.
func (m *chatTUI) showHelp() {
	var b strings.Builder
	b.WriteString(dim("  \u00b7 Commands") + "\n\n")
	b.WriteString("  " + bold("Session") + "\n")
	for _, s := range []struct{ cmd, desc string }{
		{"/compact", "compact context"},
		{"/new", "fork a fresh session"},
		{"/clear", "clear context, keep config"},
		{"/branch <name>", "create a named branch"},
		{"/tree", "show session tree"},
		{"/switch <id>", "switch branches"},
		{"/resume", "resume a saved session"},
	} {
		b.WriteString("    " + accent(s.cmd) + "  " + dim(s.desc) + "\n")
	}
	b.WriteString("\n  " + bold("Diagnostics & Config") + "\n")
	for _, s := range []struct{ cmd, desc string }{
		{"/doctor <all|key|network|cache>", "diagnostics + cache report"},
		{"/config", "view configuration"},
		{"/init", "analyze project"},
	} {
		b.WriteString("    " + accent(s.cmd) + "  " + dim(s.desc) + "\n")
	}
	b.WriteString("\n  " + bold("Tools & Plugins") + "\n")
	for _, s := range []struct{ cmd, desc string }{
		{"/btw <msg>", "ask without saving to history"},
		{"/mcp", "MCP servers"},
		{"/copy", "copy response"},
		{"/goal", "set goal"},
		{"/commands", "manage custom commands"},
	} {
		b.WriteString("    " + accent(s.cmd) + "  " + dim(s.desc) + "\n")
	}
	b.WriteString("\n  " + bold("Settings") + "\n")
	for _, s := range []struct{ cmd, desc string }{
		{"/effort auto|high|fast", "thinking depth"},
		{"/lang en|zh", "switch language"},
	} {
		b.WriteString("    " + accent(s.cmd) + "  " + dim(s.desc) + "\n")
	}
	if len(m.commands) > 0 {
		b.WriteString("\n  " + bold("Custom") + "\n")
		for _, c := range m.commands {
			desc := c.Description
			if c.ArgHint != "" {
				desc += " " + dim("("+c.ArgHint+")")
			}
			b.WriteString("    " + accent("/"+c.Name) + "  " + dim(desc) + "\n")
		}
	}
	if prompts := m.prompts(); len(prompts) > 0 {
		b.WriteString("\n  " + bold("MCP Prompts") + "\n")
		for _, p := range prompts {
			desc := p.Description
			if desc == "" {
				desc = "prompt from " + p.Server
			}
			b.WriteString("    " + accent("/"+p.Name) + "  " + dim(desc) + "\n")
		}
	}
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

// runDoctor runs health checks: API keys, network, config.
func (m *chatTUI) runDoctor(input string) {
	arg := strings.TrimSpace(strings.TrimPrefix(input, "/doctor"))
	if arg == "cache" {
		m.showCacheReport()
		return
	}
	keyOnly := arg == "key"
	netOnly := arg == "network"
	var b strings.Builder
	b.WriteString(dim("  \u00b7 "+i18n.M.SlashDoctorHeader) + "\n")
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Default()
	}
	passed, total := 0, 0
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		total++
		if k := p.APIKey(); k != "" {
			passed++
			b.WriteString(fmt.Sprintf(i18n.M.SlashDoctorKeyOK+"\n", p.Name, maskKey(k)))
		} else {
			b.WriteString(fmt.Sprintf(i18n.M.SlashDoctorKeyMissing+"\n", p.Name, p.APIKeyEnv))
		}
	}
	if !keyOnly {
		if e, ok := cfg.Provider(cfg.DefaultModel); ok && e.BaseURL != "" {
			total++
			client := &http.Client{Timeout: 5 * time.Second}
			if resp, err := client.Get(e.BaseURL + "/models"); err == nil {
				resp.Body.Close()
				passed++
				b.WriteString(fmt.Sprintf(i18n.M.SlashDoctorNetOK+"\n", e.BaseURL))
			} else {
				b.WriteString(fmt.Sprintf(i18n.M.SlashDoctorNetFail+"\n", e.BaseURL, err))
			}
		}
	}
	if !keyOnly && !netOnly {
		total++
		if src := config.SourcePath(); src != "" {
			passed++
			b.WriteString(fmt.Sprintf(i18n.M.SlashDoctorConfigOK+"\n", src))
		} else {
			b.WriteString(i18n.M.SlashDoctorConfigMiss + "\n")
		}
	}
	b.WriteString(fmt.Sprintf(i18n.M.SlashDoctorSummary+"\n", passed, total))
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

// runConfig displays current configuration.
func (m *chatTUI) runConfig() {
	cfg, err := config.Load()
	if err != nil {
		m.notice(fmt.Sprintf("config: %v", err))
		return
	}
	var b strings.Builder
	b.WriteString(dim("  \u00b7 "+i18n.M.SlashConfigHeader) + "\n")
	b.WriteString(fmt.Sprintf(i18n.M.SlashConfigDefaultModel+"\n", cfg.DefaultModel))
	if e, ok := cfg.Provider(cfg.DefaultModel); ok {
		b.WriteString(fmt.Sprintf("    model: %s (%s)", e.Model, e.BaseURL))
		if k := e.APIKey(); k != "" {
			b.WriteString("  [key ready]")
		} else {
			b.WriteString("  [" + dim("no key") + "]")
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf(i18n.M.SlashConfigMaxSteps+"\n", cfg.Agent.MaxSteps))
	b.WriteString(fmt.Sprintf(i18n.M.SlashConfigCompact+"\n", cfg.Agent.CompactRatio*100, cfg.Agent.RecentKeep))
	lang := i18n.CurrentLanguage()
	if cfg.Language != "" {
		lang = cfg.Language
	}
	b.WriteString(fmt.Sprintf(i18n.M.SlashConfigLang+"\n", lang))
	if len(m.commands) > 0 {
		b.WriteString(fmt.Sprintf("    custom commands: %d", len(m.commands)))
		for _, c := range m.commands {
			b.WriteString("\n      /" + c.Name)
			if c.Description != "" {
				b.WriteString("  " + dim(c.Description))
			}
		}
		b.WriteString("\n")
	}
	if m.host != nil {
		if servers := m.host.Servers(); len(servers) > 0 {
			b.WriteString(fmt.Sprintf("    MCP servers: %d", len(servers)))
			for _, s := range servers {
				b.WriteString(fmt.Sprintf("\n      %s (%s) \u2014 %d tools", s.Name, s.Transport, s.Tools))
			}
			b.WriteString("\n")
		}
	}
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

// runInit scans the project directory.
func (m *chatTUI) runInit() {
	var b strings.Builder
	b.WriteString(dim("  \u00b7 "+i18n.M.SlashInitTitle) + "\n")
	entries, err := os.ReadDir(".")
	if err != nil {
		m.notice(i18n.M.SlashInitNoProject)
		return
	}
	foundAny := false
	extCount := map[string]int{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext != "" {
			extCount[ext]++
		}
	}
	if len(extCount) > 0 {
		foundAny = true
		langMap := map[string]string{
			".go": "Go", ".rs": "Rust", ".py": "Python", ".js": "JavaScript",
			".ts": "TypeScript", ".tsx": "TypeScript/React", ".vue": "Vue",
			".rb": "Ruby", ".java": "Java", ".kt": "Kotlin", ".swift": "Swift",
			".c": "C", ".cpp": "C++", ".css": "CSS", ".html": "HTML",
			".toml": "TOML", ".yaml": "YAML", ".json": "JSON", ".md": "Markdown",
			".sh": "Shell", ".tf": "Terraform",
		}
		topLang, topCount := "", 0
		for ext, count := range extCount {
			if count > topCount {
				topCount, topLang = count, ext
			}
		}
		langName := langMap[topLang]
		if langName == "" {
			langName = topLang
		}
		b.WriteString(fmt.Sprintf(i18n.M.SlashInitLangHint+"\n", langName))
	}
	frameworkHints := map[string]string{
		"go.mod": "Go Modules", "Cargo.toml": "Rust/Cargo", "package.json": "Node.js",
		"requirements.txt": "Python/pip", "pyproject.toml": "Python/poetry",
		"Gemfile": "Ruby", "Dockerfile": "Docker", "Makefile": "Make",
		"reasonix.toml": "Reasonix (configured)",
	}
	for _, e := range entries {
		if hint, ok := frameworkHints[e.Name()]; ok {
			foundAny = true
			b.WriteString(fmt.Sprintf(i18n.M.SlashInitFrameHint+"\n", hint))
		}
	}
	if !foundAny {
		m.notice(i18n.M.SlashInitNoProject)
		return
	}
	if _, err := os.Stat("CLAUDE.md"); os.IsNotExist(err) {
		b.WriteString(i18n.M.SlashInitFileHint + "\n")
	}
	b.WriteString(i18n.M.SlashInitDone)
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

// runCommands manages custom slash commands.
func (m *chatTUI) runCommands(input string) {
	rest := strings.TrimSpace(strings.TrimPrefix(input, "/commands"))
	parts := strings.Fields(rest)
	subcmd := ""
	if len(parts) > 0 {
		subcmd = parts[0]
	}
	cmdsDir := filepath.Join(".reasonix", "commands")
	switch subcmd {
	case "list", "":
		if len(m.commands) == 0 {
			m.notice(i18n.M.SlashCommandsEmpty)
			return
		}
		var b strings.Builder
		b.WriteString(dim("  \u00b7 "+i18n.M.SlashCommandsTitle) + "\n")
		for _, c := range m.commands {
			b.WriteString(fmt.Sprintf("    /%s", c.Name))
			if c.Description != "" {
				b.WriteString("  " + dim(c.Description))
			}
			if c.ArgHint != "" {
				b.WriteString("  " + dim("args: "+c.ArgHint))
			}
			b.WriteString("\n")
		}
		b.WriteString(dim("  " + i18n.M.SlashCommandsCreate))
		m.commitLine(strings.TrimRight(b.String(), "\n"))
	case "create":
		if len(parts) < 3 {
			m.notice(i18n.M.SlashCommandsCreate)
			return
		}
		name, desc := parts[1], strings.Join(parts[2:], " ")
		if err := os.MkdirAll(cmdsDir, 0o755); err != nil {
			m.notice(fmt.Sprintf("mkdir: %v", err))
			return
		}
		path := filepath.Join(cmdsDir, name+".md")
		if err := os.WriteFile(path, []byte(fmt.Sprintf("---\ndescription: %s\n---\n# %s\n\n$ARGUMENTS", desc, name)), 0o644); err != nil {
			m.notice(fmt.Sprintf("write: %v", err))
			return
		}
		m.notice(fmt.Sprintf(i18n.M.SlashCommandsCreated, name))
	case "delete":
		if len(parts) < 2 {
			m.notice(i18n.M.SlashCommandsDelete)
			return
		}
		name := parts[1]
		path := filepath.Join(cmdsDir, name+".md")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			m.notice(fmt.Sprintf(i18n.M.SlashCommandsNotFound, name))
			return
		}
		if err := os.Remove(path); err != nil {
			m.notice(fmt.Sprintf("remove: %v", err))
			return
		}
		m.notice(fmt.Sprintf(i18n.M.SlashCommandsDeleted, name))
	default:
		m.notice(i18n.M.SlashCommandsCreate)
	}
}

// runEffort sets thinking depth.
func (m *chatTUI) runEffort(input string) {
	val := strings.TrimSpace(strings.TrimPrefix(input, "/effort"))
	switch val {
	case "auto", "high", "fast":
		m.ctrl.SetEffort(val)
		m.notice(fmt.Sprintf(i18n.M.SlashEffortSetFmt, val))
	default:
		m.notice(i18n.M.SlashEffortUsage)
	}
}

// runBtw sends an ephemeral message.
func (m *chatTUI) runBtw(input string) tea.Cmd {
	rest := strings.TrimSpace(strings.TrimPrefix(input, "/btw"))
	if rest == "" {
		m.notice(i18n.M.SlashBtwUsage)
		return nil
	}
	m.commitReasoning()
	m.commitPending()
	m.turnAccumulator.Reset()
	m.commitLine("")
	m.commitLine(renderUserBubble("[btw] "+rest, m.width, m.planMode))
	m.state = tuiRunning
	m.runStart = time.Now()
	m.elapsed = 0
	sent := m.ctrl.Compose(rest)
	m.ctrl.SendEphemeral(sent)
	return tea.Batch(m.spinner.Tick, elapsedTick())
}

// runMCP dispatches MCP subcommands.
func (m *chatTUI) runMCP(input string) {
	arg := strings.TrimSpace(strings.TrimPrefix(input, "/mcp"))
	switch arg {
	case "prompts":
		m.showMCPPrompts()
	case "resources":
		m.showMCPResources()
	case "tools":
		m.showMCPTools()
	case "import from cc-switch":
		m.runMCPImport()
	default:
		m.showMCPStatus()
	}
}

// showMCPStatus lists connected servers.
func (m *chatTUI) showMCPStatus() {
	if m.host == nil || len(m.host.Servers()) == 0 {
		m.notice(i18n.M.SlashMCPNone)
		return
	}
	servers := m.host.Servers()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", dim(fmt.Sprintf("  \u00b7 MCP servers (%d)", len(servers))))
	for _, s := range servers {
		fmt.Fprintf(&b, "    %s %s %s\n", accent("\u2713"), bold(s.Name),
			dim(fmt.Sprintf("(%s) \u2014 %d tools \u00b7 %d prompts \u00b7 %d resources", s.Transport, s.Tools, s.Prompts, s.Resources)))
	}
	for _, p := range m.host.Prompts() {
		fmt.Fprintf(&b, "      %s  %s\n", "/"+p.Name, dim(p.Description))
	}
	for _, r := range m.host.Resources() {
		label := r.Name
		if label == "" {
			label = r.Description
		}
		fmt.Fprintf(&b, "      %s  %s\n", "@"+r.Server+":"+r.URI, dim(label))
	}
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

// showMCPPrompts lists prompts.
func (m *chatTUI) showMCPPrompts() {
	prompts := m.prompts()
	if len(prompts) == 0 {
		m.notice("no MCP prompts")
		return
	}
	var b strings.Builder
	b.WriteString(dim("  \u00b7 MCP Prompts") + "\n")
	for _, p := range prompts {
		b.WriteString(fmt.Sprintf("    /%s  %s\n", p.Name, dim(p.Description)))
	}
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

// showMCPResources lists resources.
func (m *chatTUI) showMCPResources() {
	if m.host == nil {
		m.notice("no MCP servers")
		return
	}
	resources := m.host.Resources()
	if len(resources) == 0 {
		m.notice("no resources")
		return
	}
	var b strings.Builder
	b.WriteString(dim("  \u00b7 MCP Resources") + "\n")
	for _, r := range resources {
		label := r.Name
		if label == "" {
			label = r.Description
		}
		b.WriteString(fmt.Sprintf("    @%s:%s  %s\n", r.Server, r.URI, dim(label)))
	}
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

// showMCPTools lists tools.
func (m *chatTUI) showMCPTools() {
	if m.host == nil {
		m.notice("no MCP servers")
		return
	}
	servers := m.host.Servers()
	if len(servers) == 0 {
		m.notice("no servers")
		return
	}
	var b strings.Builder
	b.WriteString(dim("  \u00b7 MCP Tools") + "\n")
	for _, s := range servers {
		b.WriteString(fmt.Sprintf("    %s (%s) \u2014 %d tools\n", s.Name, s.Transport, s.Tools))
	}
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

// runMCPImport reads cc-switch's SQLite database and imports MCP server configs
// into reasonix.toml. Already-present entries are skipped; new ones are added.
func (m *chatTUI) runMCPImport() {
	dbPath, err := ccswitch.DBPath()
	if err != nil {
		m.notice(fmt.Sprintf("cc-switch: %v", err))
		return
	}
	entries, err := ccswitch.Import(dbPath)
	if err != nil {
		m.notice(fmt.Sprintf("cc-switch: %v", err))
		return
	}
	if len(entries) == 0 {
		m.notice(i18n.M.SlashMCPImportEmpty)
		return
	}
	cfg, err := config.Load()
	if err != nil {
		m.notice(fmt.Sprintf("%s %v", i18n.M.ErrorPrefix, err))
		return
	}
	existing := map[string]bool{}
	for _, p := range cfg.Plugins {
		existing[p.Name] = true
	}
	var added, skipped []string
	var addedEntries []config.PluginEntry
	for _, e := range entries {
		if existing[e.Name] {
			skipped = append(skipped, e.Name)
			continue
		}
		if err := cfg.UpsertPlugin(e); err != nil {
			m.notice(fmt.Sprintf("%s %s: %v", i18n.M.ErrorPrefix, e.Name, err))
			continue
		}
		added = append(added, e.Name)
		addedEntries = append(addedEntries, e)
	}
	if len(added) == 0 && len(skipped) == 0 {
		m.notice(i18n.M.SlashMCPImportEmpty)
		return
	}
	if err := cfg.Save(); err != nil {
		m.notice(fmt.Sprintf("%s: %v", i18n.M.WriteConfigErr, err))
		return
	}
	// Hot-reload: connect the newly-added servers without restarting the session.
	live := m.ctrl.HotReloadMCPServers(context.Background(), pluginSpecs(addedEntries))
	liveSet := map[string]bool{}
	for _, n := range live {
		liveSet[n] = true
	}
	var b strings.Builder
	b.WriteString(dim(fmt.Sprintf("  \u00b7 "+i18n.M.SlashMCPImportDone, len(added))))
	for _, n := range added {
		marker := accent("+ ")
		if liveSet[n] {
			marker = accent("\u2713 ")
		} else {
			marker = accent("+ ") + dim("(restart needed)")
		}
		b.WriteString("\n    " + marker + n)
	}
	if len(skipped) > 0 {
		b.WriteString("\n" + dim(fmt.Sprintf("  \u00b7 "+i18n.M.SlashMCPImportSkipped, len(skipped))))
		for _, n := range skipped {
			b.WriteString("\n    " + dim("- "+n+" (exists)"))
		}
	}
	m.commitLine(b.String())
}

// maskKey returns a safe-to-display version of an API key.
func maskKey(k string) string {
	if len(k) <= 8 {
		return k[:1] + "..." + k[len(k)-1:]
	}
	return k[:4] + "..." + k[len(k)-4:]
}

// commandNames renders the custom command list for /help, "" when there are none.

// runBranch creates a named branch from current session.
func (m *chatTUI) runBranch(input string) {
	label := strings.TrimSpace(strings.TrimPrefix(input, "/branch"))
	if label == "" {
		m.notice("/branch <name>")
		return
	}
	if err := m.ctrl.Branch(label); err != nil {
		m.notice(fmt.Sprintf("branch: %v", err))
		return
	}
	m.pending.Reset()
	m.reasoning.Reset()
	m.turnAccumulator.Reset()
	m.pendingPlan = ""
	m.sessionTag = label
	m.commitLine("")
	m.commitLine(strings.TrimRight(renderTUIBanner(m.label, "", m.width), "\n"))
	m.notice(fmt.Sprintf(i18n.M.SlashBranchDone, label))
}

// showTree displays the session tree.
func (m *chatTUI) showTree() {
	nodes, labels, current := m.ctrl.TreeInfo()
	if nodes == nil {
		m.notice(i18n.M.SlashTreeDisabled)
		return
	}
	var b strings.Builder
	b.WriteString(dim("  · "+i18n.M.SlashTreeTitle) + "\n")
	for _, id := range nodes {
		marker := "  "
		if id == current {
			marker = accent("▶ ")
		}
		lbl := labels[id]
		if lbl == "" {
			lbl = id
		}
		b.WriteString(fmt.Sprintf("    %s%s %s\n", marker, dim(id), lbl))
	}
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

// runSwitch switches to a tree node.
func (m *chatTUI) runSwitch(input string) {
	id := strings.TrimSpace(strings.TrimPrefix(input, "/switch"))
	if id == "" {
		m.notice(i18n.M.SlashSwitchUsage)
		return
	}
	if err := m.ctrl.SwitchTo(id); err != nil {
		m.notice(fmt.Sprintf("switch: %v", err))
		return
	}
	m.pending.Reset()
	m.reasoning.Reset()
	m.turnAccumulator.Reset()
	m.pendingPlan = ""
	m.syncSessionTagFromTree()
	m.commitLine("")
	m.commitLine(strings.TrimRight(renderTUIBanner(m.label, "", m.width), "\n"))
	m.notice(fmt.Sprintf(i18n.M.SlashSwitchDone, id))
}

// runResume enters the session picker.
func (m *chatTUI) runResume() {
	dir := config.SessionDir()
	if dir == "" {
		m.notice(i18n.M.ResumeNoDir)
		return
	}
	sessions, err := agent.ListSessions(dir)
	if err != nil {
		m.notice(fmt.Sprintf("%s: %v", i18n.M.ResumeFailed, err))
		return
	}
	if len(sessions) == 0 {
		m.notice(i18n.M.ResumeEmpty)
		return
	}
	if len(sessions) > 10 {
		sessions = sessions[:10]
	}
	m.picker = &sessionPicker{sessions: sessions}
}

// renderSessionPicker draws the session list.
func (m *chatTUI) renderSessionPicker() string {
	if m.picker == nil || len(m.picker.sessions) == 0 {
		return ""
	}
	w := m.width
	if w < 40 {
		w = 40
	}
	var b strings.Builder
	b.WriteString(dim("  "+i18n.M.ResumeListTitle) + "\n")
	for i, s := range m.picker.sessions {
		ts := s.ModTime.Format("01/02 15:04")
		preview := s.Preview
		if preview == "" {
			preview = "(empty)"
		}
		line := fmt.Sprintf("  %d. %s · %d turns · %s", i+1, ts, s.Turns, preview)
		if i == m.picker.highlight {
			line = "▶ " + line[2:]
			b.WriteString(compSelStyle.Render(truncateTo(line, w-2)))
		} else {
			b.WriteString(dim(truncateTo(line, w-2)))
		}
		b.WriteString("\n")
	}
	b.WriteString(dim("  " + i18n.M.ResumePickerHint))
	return b.String()
}

// doResume switches to the selected session.
func (m *chatTUI) doResume(si agent.SessionInfo) {
	if err := m.ctrl.ResumeSession(si.Path); err != nil {
		m.notice(fmt.Sprintf("%s: %v", i18n.M.ResumeFailed, err))
		m.picker = nil
		return
	}
	m.pending.Reset()
	m.reasoning.Reset()
	m.turnAccumulator.Reset()
	m.pendingPlan = ""
	m.syncSessionTagFromTree()
	m.commitLine("")
	m.commitLine(strings.TrimRight(renderTUIBanner(m.label, "", m.width), "\n"))
	history := m.ctrl.History()
	r := newMarkdownRenderer(m.width)
	for _, sec := range replaySectionsFor(history, m.width, r) {
		if sec = strings.TrimRight(sec, "\n"); sec != "" {
			m.commitLine(sec)
		}
	}
	m.notice(fmt.Sprintf(i18n.M.ResumeSwitched, si.Preview))
	m.picker = nil
}

// truncateTo trims s to at most width runes, appending … when truncated.
func truncateTo(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
}

// maskKey returns a safe-to-display API key.

func (m *chatTUI) commandNames() string {
	if len(m.commands) == 0 {
		return ""
	}
	names := make([]string, len(m.commands))
	for i, c := range m.commands {
		names[i] = "/" + c.Name
	}
	return strings.Join(names, " · ")
}

// showMCPStatus queues the connected MCP servers, their counts, and the prompt
// commands / resource refs they expose — the discovery surface for /mcp.

// notice queues a dim informational line to scrollback.
func (m *chatTUI) notice(note string) {
	m.commitLine(dim("  · " + note))
}

// runCopy handles /copy: copies the last assistant response to clipboard.
func (m *chatTUI) runCopy(input string) {
	history := m.ctrl.History()
	kind := strings.TrimSpace(strings.TrimPrefix(input, "/copy"))

	var text string
	switch kind {
	case "", "last":
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == provider.RoleAssistant {
				text = history[i].Content
				break
			}
		}
		if text == "" {
			m.notice(i18n.M.SlashCopyNoFile)
			return
		}
	case "all":
		var b strings.Builder
		for _, msg := range history {
			switch msg.Role {
			case provider.RoleUser:
				b.WriteString("## User\n\n" + msg.Content + "\n\n")
			case provider.RoleAssistant:
				if msg.Content != "" {
					b.WriteString("## Assistant\n\n" + msg.Content + "\n\n")
				}
			}
		}
		text = b.String()
		if text == "" {
			m.notice(i18n.M.SlashCopyNoFile)
			return
		}
	default:
		m.notice(fmt.Sprintf("%s: /copy [last|all]", i18n.M.SlashUnknown))
		return
	}

	if tmp, err := copyToClipboard(text); err != nil {
		m.notice(fmt.Sprintf(i18n.M.SlashCopyFailed, err))
	} else if tmp != "" {
		m.notice(fmt.Sprintf(i18n.M.SlashCopyWritten, tmp))
	} else {
		m.notice(i18n.M.SlashCopyDone)
	}
}

// copyToClipboard writes text to the system clipboard. Returns the temp file
// path when no clipboard tool is available (non-nil path means fallback file).
func copyToClipboard(text string) (tempPath string, err error) {
	if _, e := exec.LookPath("pbcopy"); e == nil {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return "", cmd.Run()
	}
	if _, e := exec.LookPath("xclip"); e == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		return "", cmd.Run()
	}
	if _, e := exec.LookPath("wl-copy"); e == nil {
		cmd := exec.Command("wl-copy")
		cmd.Stdin = strings.NewReader(text)
		return "", cmd.Run()
	}
	f, err := os.CreateTemp("", "reasonix-copy-*.txt")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}

// runGoal handles /goal slash commands.
func (m *chatTUI) runGoal(input string) {
	rest := strings.TrimSpace(strings.TrimPrefix(input, "/goal"))
	ws, _ := os.Getwd()

	switch {
	case rest == "" || rest == "status":
		s, err := goal.Load(goal.Dir(ws))
		if err != nil || s == nil {
			m.notice(i18n.M.GoalNoGoal)
			return
		}
		lines := strings.Split(fmt.Sprintf(i18n.M.GoalStatusFmt, s.Goal, s.Status, s.Attempts), "\n")
		for _, line := range lines {
			m.notice(line)
		}
	case rest == "cancel":
		goal.Delete(goal.Dir(ws))
		m.notice(i18n.M.GoalCancelled)
	default:
		s := &goal.State{Goal: rest, Status: goal.StatusActive, Attempts: 1}
		s.Save(goal.Dir(ws))
		m.notice(fmt.Sprintf(i18n.M.GoalStartedFmt, rest))
	}
}

// showCacheReport prints cache diagnostic history.
func (m *chatTUI) showCacheReport() {
	entries := m.ctrl.CacheReport()
	if len(entries) == 0 {
		m.notice("no cache diagnostics recorded yet")
		return
	}
	var b strings.Builder
	b.WriteString(dim("  · "+i18n.M.CacheReportTitle) + "\n")
	stableTurns := 0
	for i, d := range entries {
		if d.PrefixChanged {
			b.WriteString(fmt.Sprintf("    "+i18n.M.CacheReportTurnFmt, i+1, d.CacheHitTokens, d.CacheMissTokens))
			if len(d.PrefixChangeReasons) > 0 {
				b.WriteString(fmt.Sprintf(" · "+i18n.M.CacheReportChurn, d.PrefixChangeReasons))
			}
			b.WriteByte('\n')
		} else {
			stableTurns++
		}
	}
	if stableTurns > 0 {
		b.WriteString(dim(fmt.Sprintf("    "+i18n.M.CacheReportStable+" (%d turns)", stableTurns)))
	}
	m.commitLine(strings.TrimRight(b.String(), "\n"))
}

// runLang handles /lang and /language for runtime language switching.
func (m *chatTUI) runLang(input string) {
	tag := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(input, "/lang"), "/language"))
	if tag == "" {
		m.notice(fmt.Sprintf("current language: %s — /lang en | zh", i18n.CurrentLanguage()))
		return
	}
	resolved := i18n.SwitchLanguage(tag)
	m.notice(fmt.Sprintf("language: %s", resolved))

	// Persist to config.
	cfg, err := config.Load()
	if err != nil {
		return
	}
	cfg.Language = resolved
	src := config.SourcePath()
	if src != "" {
		_ = cfg.WriteFile(src)
	}
}

// handleCtrlV intercepts Ctrl+V paste: if the clipboard holds image data,
// it saves a temp PNG and injects an @-reference into the input. Returns
// true when it consumed the event; false falls through to normal text paste.
func (m *chatTUI) handleCtrlV() bool {
	path, err := clipboardImagePath()
	if err != nil || path == "" {
		return false
	}
	cur := m.input.Value()
	if cur != "" && !strings.HasSuffix(cur, " ") {
		cur += " "
	}
	cur += "@" + path
	m.input.SetValue(cur)
	m.input.CursorEnd()
	m.completion = completion{}
	m.notice(fmt.Sprintf(i18n.M.SlashImgSaved, path))
	if m.host != nil {
		m.notice(i18n.M.SlashImgMCPHint)
	}
	return true
}

// handleCtrlV checks whether the clipboard holds an image. When it does
// the image is saved to a temp file and an @-reference is injected into
// the input; true means the paste was consumed. Text-only paste returns
// false so the textarea handles it normally.

// resolveRefs resolves a line's @references off the event loop via the
// controller, delivering a refsResolvedMsg with the tagged context block.
func (m *chatTUI) resolveRefs(line string) tea.Cmd {
	return func() tea.Msg {
		block, errs := m.ctrl.ResolveRefs(context.Background(), line)
		return refsResolvedMsg{line: line, block: block, errs: errs}
	}
}

// runMCPPrompt resolves a /mcp__server__prompt command off the event loop via
// the controller, delivering a promptResolvedMsg with the rendered prompt.
func (m *chatTUI) runMCPPrompt(input string) tea.Cmd {
	return func() tea.Msg {
		sent, found, err := m.ctrl.MCPPrompt(context.Background(), input)
		if !found {
			name := strings.TrimPrefix(strings.Fields(input)[0], "/")
			return promptResolvedMsg{display: input, err: fmt.Errorf("%s: /%s", i18n.M.SlashUnknown, name)}
		}
		return promptResolvedMsg{display: input, sent: sent, err: err}
	}
}

// replaySectionsFor turns a loaded session into scrollback blocks: user bubbles
// and assistant markdown. Tool messages are dropped — needed in session state
// but noise in the visible transcript on resume.
func replaySectionsFor(history []provider.Message, width int, renderer *mdRenderer) []string {
	var out []string
	for _, m := range history {
		switch m.Role {
		case provider.RoleUser:
			content := strings.TrimPrefix(m.Content, control.PlanModeMarker+"\n\n")
			out = append(out, renderUserBubble(content, width, false)+"\n\n")
		case provider.RoleAssistant:
			body := strings.TrimSpace(m.Content)
			if body == "" {
				continue
			}
			rendered := renderer.Render(body)
			if rendered == "" {
				rendered = body
			}
			out = append(out, rendered+"\n")
		}
	}
	return out
}

// renderTUIBanner is the welcome card printed once at the top of a new session.
func renderTUIBanner(label, missing string, width int) string {
	if width < 70 {
		width = 70
	}
	contentW := width - 4
	leftW := contentW / 3
	if leftW < 28 {
		leftW = 28
	}
	if leftW > 42 {
		leftW = 42
	}
	rightW := contentW - leftW - 3
	if rightW < 28 {
		rightW = 28
		leftW = contentW - rightW - 3
	}

	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		cwd = "."
	}
	if len(cwd) > leftW {
		cwd = truncateTo(cwd, leftW)
	}
	modelLine := strings.TrimSpace(label)
	if modelLine == "" {
		modelLine = "model ready"
	}

	left := []string{
		"",
		centerCells(bold("Welcome back!"), leftW),
		"",
		centerCells(accent("REASONIX")+" "+dim("×")+" "+lipgloss.NewStyle().Foreground(lipgloss.Color("37")).Bold(true).Render("DeepSeek"), leftW),
		centerCells(dim("DeepSeek-native coding agent"), leftW),
		centerCells(dim("cache-first · flash-first"), leftW),
		"",
		centerCells(dim(modelLine+" · API Usage Billing"), leftW),
		centerCells(dim(cwd), leftW),
	}
	right := []string{
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("173")).Bold(true).Render("Tips for getting started"),
		"Run " + accent("/init") + " to inspect the project and prepare context.",
		"Use " + accent("/help") + " for commands, " + accent("/mcp") + " for plugins.",
		dim(strings.Repeat("─", rightW)),
		lipgloss.NewStyle().Foreground(lipgloss.Color("173")).Bold(true).Render("What's new"),
		"Cache diagnostics are available from " + accent("/cache") + ".",
		"Session branches are available with " + accent("/branch") + " and " + accent("/switch") + ".",
		"Use " + accent("/config") + " to inspect model, language, and compaction settings.",
	}
	if missing != "" {
		right = append(right, "", yellow("! "+truncateTo(missing, rightW-2)))
	}

	var b strings.Builder
	top := "╭" + strings.Repeat("─", leftW+2) + "┬" + strings.Repeat("─", rightW+2) + "╮"
	mid := "├" + strings.Repeat("─", leftW+2) + "┼" + strings.Repeat("─", rightW+2) + "┤"
	bot := "╰" + strings.Repeat("─", leftW+2) + "┴" + strings.Repeat("─", rightW+2) + "╯"
	b.WriteString(accent(top) + "\n")
	rows := len(left)
	if len(right) > rows {
		rows = len(right)
	}
	for i := 0; i < rows; i++ {
		if i == 4 {
			b.WriteString(accent(mid) + "\n")
		}
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		b.WriteString(accent("│") + " " + padRight(l, leftW) + " " + accent("│") + " " + padRight(r, rightW) + " " + accent("│") + "\n")
	}
	b.WriteString(accent(bot) + "\n")
	b.WriteString(dim("  "+i18n.M.ChatTip) + "\n")
	return b.String()
}

func centerCells(s string, width int) string {
	pad := width - visibleWidth(s)
	if pad <= 0 {
		return s
	}
	left := pad / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", pad-left)
}

// wrapForViewport hard-wraps text to fit width columns and colours every line.
func wrapForViewport(text string, width int, fg color.Color) string {
	if width <= 0 {
		width = 80
	}
	return lipgloss.NewStyle().
		Foreground(fg).
		Width(width).
		Render(text)
}

// renderUserBubble styles the just-submitted line with a filled dim background.
func renderUserBubble(line string, width int, planMode bool) string {
	prefix := "› "
	if planMode {
		prefix = "› [plan] "
	}
	if !colorEnabled {
		return "│ " + prefix + line
	}
	w := width - 4
	if w < 10 {
		w = 10
	}
	bubble := lipgloss.NewStyle().
		Background(lipgloss.Color(UserBubbleBg())).
		Width(w).
		Padding(0, 1)
	return bubble.Render(prefix + line)
}

// eventSink is the event.Sink the agent emits to in TUI mode. Each event
// becomes an agentEventMsg. The channel is generously buffered so streaming
// bursts don't back-pressure the agent goroutine.
type eventSink struct {
	ch chan<- event.Event
}

func (s *eventSink) Emit(e event.Event) { s.ch <- e }

// compactArgs trims and caps a tool's raw JSON arguments for the dispatch line,
// matching the agent's headless rendering so the chat timeline reads the same.
func compactArgs(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120]) + "..."
	}
	return s
}
