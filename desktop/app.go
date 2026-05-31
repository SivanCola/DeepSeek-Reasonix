package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/memory"
	"reasonix/internal/plugin"
	"reasonix/internal/provider"
)

// eventChannel is the Wails runtime event name the frontend subscribes to for the
// agent's typed event stream. One channel carries every event kind; the payload's
// `kind` field discriminates — the desktop analogue of the serve transport's SSE
// `data:` frames.
const eventChannel = "agent:event"

// App is the Wails-bound application object: the desktop frontend's command
// surface. Its exported methods (Submit/Cancel/Approve/…) are generated into JS
// bindings and call straight through to one transport-agnostic control.Controller
// — the same controller the chat TUI and the HTTP/SSE server drive, assembled by
// the shared internal/boot. Events flow the other way: the controller emits to an
// eventSink that forwards each one to the webview via runtime.EventsEmit.
type App struct {
	ctx  context.Context
	sink *eventSink
	ctrl *control.Controller

	startupErr string
	label      string
	model      string // active provider name (for the bottom model switcher)

	mu          sync.Mutex
	tabs        map[string]*tabState
	activeTabID string
	nextTab     int
}

// NewApp constructs the bound object. The controller is built later, in startup,
// once the Wails context exists.
func NewApp() *App { return &App{sink: &eventSink{}, tabs: map[string]*tabState{}} }

type tabState struct {
	ID           string
	Controller   *control.Controller
	Label        string
	Model        string
	WorkspaceDir string
	Err          string
}

type TabInfo struct {
	ID           string `json:"id"`
	WorkspaceDir string `json:"workspaceDir"`
	Active       bool   `json:"active"`
}

// startup runs once the webview process is up, before the frontend can issue any
// bound call. It captures the Wails context (needed for EventsEmit), points the
// sink at it, then builds the controller with that sink — so the event bridge is
// live before the first command lands. RequireKey is false so a missing API key
// opens the window in a "set your key" state rather than failing to launch; a
// build error is surfaced through Meta instead of crashing the window.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.sink.ctx = ctx

	// A GUI launch starts in "/" (read-only); move into a real, writable working
	// folder (the remembered one, else home) before anything reads/writes config,
	// .env, memory, or skills relative to cwd.
	ensureWorkspace()
	cwd, _ := os.Getwd()

	// Resolve the active model to its canonical "provider/model" ref up front so
	// the switcher can mark it current.
	if cfg, err := config.LoadAt(cwd); err == nil {
		// Drive the Go-side catalogue (i18n.M) from the configured language so the
		// backend-provided slash UI — command descriptions, sub-command hints,
		// listing notices — comes through localized, matching the frontend.
		i18n.DetectLanguage(cfg.Language)
		a.model = cfg.DefaultModel
		if e, ok := cfg.ResolveModel(cfg.DefaultModel); ok {
			a.model = e.Name + "/" + e.Model
		}
	}

	tabID := a.newTabID()
	ctrl, err := boot.Build(ctx, boot.Options{Model: a.model, RequireKey: false, Sink: &eventSink{ctx: ctx, tabID: tabID}, CWD: cwd})
	if err != nil {
		a.startupErr = err.Error()
		return
	}
	a.ctrl = ctrl
	a.label = ctrl.Label()

	// Desktop is interactive: route "ask" gate decisions to the frontend as
	// approval_request events, answered via Approve.
	ctrl.EnableInteractiveApproval()

	// Land auto-save in a fresh session file (same as a fresh chat/serve start).
	if dir := ctrl.SessionDir(); dir != "" {
		ctrl.SetSessionPath(agent.NewSessionPath(dir, ctrl.Label()))
	}
	a.tabs[tabID] = &tabState{ID: tabID, Controller: ctrl, Label: a.label, Model: a.model, WorkspaceDir: cwd}
	a.activeTabID = tabID
}

// shutdown snapshots the conversation and stops plugin subprocesses on close.
func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, tab := range a.tabs {
		if tab.Controller != nil {
			_ = tab.Controller.Snapshot()
			tab.Controller.Close()
		}
	}
}

func (a *App) newTabID() string {
	a.nextTab++
	return fmt.Sprintf("tab-%d", a.nextTab)
}

func (a *App) syncActiveLocked(tab *tabState) {
	if tab == nil {
		a.activeTabID = ""
		a.ctrl = nil
		a.label = ""
		return
	}
	a.activeTabID = tab.ID
	a.ctrl = tab.Controller
	a.label = tab.Label
	a.model = tab.Model
	a.startupErr = tab.Err
}

func (a *App) tabInfosLocked() []TabInfo {
	out := make([]TabInfo, 0, len(a.tabs))
	for _, tab := range a.tabs {
		out = append(out, TabInfo{ID: tab.ID, WorkspaceDir: tab.WorkspaceDir, Active: tab.ID == a.activeTabID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (a *App) Tabs() []TabInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tabInfosLocked()
}

func (a *App) OpenTab() (TabInfo, error) {
	a.mu.Lock()
	tabID := a.newTabID()
	model := a.model
	label := a.label
	cwd := a.activeWorkspaceDirLocked()
	tab := &tabState{ID: tabID, Label: label, Model: model, WorkspaceDir: cwd}
	a.tabs[tabID] = tab
	a.syncActiveLocked(tab)
	info := TabInfo{ID: tab.ID, WorkspaceDir: tab.WorkspaceDir, Active: true}
	a.mu.Unlock()

	go a.buildTab(tabID, model, cwd)
	return info, nil
}

func (a *App) activeWorkspaceDirLocked() string {
	if tab := a.tabs[a.activeTabID]; tab != nil && tab.WorkspaceDir != "" {
		return tab.WorkspaceDir
	}
	cwd, _ := os.Getwd()
	return cwd
}

func (a *App) buildTab(tabID, model, cwd string) {
	ctrl, err := boot.Build(a.ctx, boot.Options{Model: model, RequireKey: false, Sink: &eventSink{ctx: a.ctx, tabID: tabID}, CWD: cwd})
	a.mu.Lock()
	tab := a.tabs[tabID]
	if tab == nil {
		a.mu.Unlock()
		if ctrl != nil {
			ctrl.Close()
		}
		return
	}
	if err != nil {
		tab.Err = err.Error()
		if a.activeTabID == tabID {
			a.syncActiveLocked(tab)
		}
		a.mu.Unlock()
		a.emitTabReady(tabID, err.Error())
		return
	}
	ctrl.EnableInteractiveApproval()
	if dir := ctrl.SessionDir(); dir != "" {
		ctrl.SetSessionPath(agent.NewSessionPath(dir, ctrl.Label()))
	}
	tab.Controller = ctrl
	tab.Label = ctrl.Label()
	tab.Model = model
	tab.WorkspaceDir = cwd
	tab.Err = ""
	if a.activeTabID == tabID {
		a.syncActiveLocked(tab)
	}
	a.mu.Unlock()
	a.emitTabReady(tabID, "")
}

func (a *App) ActivateTab(id string) []TabInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	if tab := a.tabs[id]; tab != nil {
		a.syncActiveLocked(tab)
	}
	return a.tabInfosLocked()
}

func (a *App) CloseTab(id string) []TabInfo {
	a.mu.Lock()
	if len(a.tabs) <= 1 {
		out := a.tabInfosLocked()
		a.mu.Unlock()
		return out
	}
	tab := a.tabs[id]
	if tab == nil {
		out := a.tabInfosLocked()
		a.mu.Unlock()
		return out
	}
	delete(a.tabs, id)
	wasActive := a.activeTabID == id
	var next *tabState
	if wasActive {
		for _, candidate := range a.tabs {
			next = candidate
			break
		}
		a.syncActiveLocked(next)
	}
	out := a.tabInfosLocked()
	a.mu.Unlock()

	if tab.Controller != nil {
		_ = tab.Controller.Snapshot()
		tab.Controller.Close()
	}
	return out
}

// --- bound command surface (frontend → controller) ---
// Each method guards on a nil controller so a pre-startup or failed-build call is
// a no-op, never a panic.

// Submit runs raw user input as a turn; slash commands and @-references are
// resolved by the controller. Output arrives asynchronously on eventChannel.
func (a *App) Submit(input string) {
	if a.ctrl != nil {
		a.ctrl.Submit(input)
	}
}

// SavePastedImage persists an image pasted into the desktop composer and returns
// a workspace-relative @-reference path the frontend can insert into the prompt.
func (a *App) SavePastedImage(dataURL string) (string, error) {
	return control.SaveImageDataURL(dataURL)
}

func (a *App) AttachmentDataURL(path string) (string, error) {
	return control.ImageDataURL(path)
}

// Cancel aborts the in-flight turn.
func (a *App) Cancel() {
	if a.ctrl != nil {
		a.ctrl.Cancel()
	}
}

// Approve answers a pending approval_request by ID: allow runs the call, session
// also remembers the grant for the rest of the session.
func (a *App) Approve(id string, allow, session bool) {
	if a.ctrl != nil {
		a.ctrl.Approve(id, allow, session)
	}
}

// SetPlanMode toggles read-only plan mode.
func (a *App) SetPlanMode(on bool) {
	if a.ctrl != nil {
		a.ctrl.SetPlanMode(on)
	}
}

// QuestionAnswer is the frontend's reply to one question in an ask_request.
type QuestionAnswer struct {
	QuestionID string   `json:"questionId"`
	Selected   []string `json:"selected"`
}

// AnswerQuestion resolves a pending ask_request (the `ask` tool) by ID with the
// user's selections per question.
func (a *App) AnswerQuestion(id string, answers []QuestionAnswer) {
	if a.ctrl == nil {
		return
	}
	out := make([]event.AskAnswer, len(answers))
	for i, an := range answers {
		out[i] = event.AskAnswer{QuestionID: an.QuestionID, Selected: an.Selected}
	}
	a.ctrl.AnswerQuestion(id, out)
}

// Compact runs one compaction pass on demand.
func (a *App) Compact() error {
	if a.ctrl == nil {
		return nil
	}
	return a.ctrl.Compact(a.ctx)
}

// NewSession snapshots the current conversation and rotates to a fresh one.
func (a *App) NewSession() error {
	if a.ctrl == nil {
		return nil
	}
	return a.ctrl.NewSession()
}

// CheckpointMeta summarises one rewind point (a user turn) for the desktop.
type CheckpointMeta struct {
	Turn   int      `json:"turn"`
	Prompt string   `json:"prompt"`
	Files  []string `json:"files"` // paths changed during the turn
	Time   int64    `json:"time"`  // unix milliseconds
}

// Checkpoints lists the session's rewind points, oldest first, for the rewind UI.
func (a *App) Checkpoints() []CheckpointMeta {
	if a.ctrl == nil {
		return []CheckpointMeta{}
	}
	metas := a.ctrl.Checkpoints()
	out := make([]CheckpointMeta, 0, len(metas))
	for _, m := range metas {
		out = append(out, CheckpointMeta{Turn: m.Turn, Prompt: m.Prompt, Files: m.Paths, Time: m.Time.UnixMilli()})
	}
	return out
}

// Rewind restores the session to the start of turn. scope is "code",
// "conversation", or "both" (anything else is treated as "both"). The frontend
// re-reads History after this resolves.
func (a *App) Rewind(turn int, scope string) error {
	if a.ctrl == nil {
		return nil
	}
	s := control.RewindBoth
	switch scope {
	case "code":
		s = control.RewindCode
	case "conversation":
		s = control.RewindConversation
	}
	return a.ctrl.Rewind(turn, s)
}

// Fork branches the conversation at the start of turn into a new session
// (preserving the current one), keeping code intact, and switches to the branch.
// The frontend re-reads History after this resolves.
func (a *App) Fork(turn int) error {
	if a.ctrl == nil {
		return nil
	}
	_, err := a.ctrl.Fork(turn)
	return err
}

// SummarizeFrom / SummarizeUpTo compress the conversation from / up to the start
// of turn into one summary (Claude Code's "summarize from/up to here"), keeping
// code intact. The frontend re-reads History after this resolves.
func (a *App) SummarizeFrom(turn int) error {
	if a.ctrl == nil {
		return nil
	}
	return a.ctrl.SummarizeFrom(a.ctx, turn)
}

func (a *App) SummarizeUpTo(turn int) error {
	if a.ctrl == nil {
		return nil
	}
	return a.ctrl.SummarizeUpTo(a.ctx, turn)
}

// SessionMeta summarises one saved session for the history panel.
type SessionMeta struct {
	Path    string `json:"path"`
	Preview string `json:"preview"`         // first user message
	Title   string `json:"title,omitempty"` // user-chosen name, when set (overrides preview)
	Turns   int    `json:"turns"`
	ModTime int64  `json:"modTime"` // unix milliseconds, for the frontend to group/format
	Current bool   `json:"current"`
}

type PickFileFilter struct {
	Name       string   `json:"name"`
	Extensions []string `json:"extensions"`
}

// ListSessions returns the saved sessions newest-first for the history panel,
// marking the one the current conversation is writing to and attaching any
// user-chosen titles.
func (a *App) ListSessions() []SessionMeta {
	dir := config.SessionDir()
	infos, err := agent.ListSessions(dir)
	if err != nil {
		return []SessionMeta{}
	}
	titles := loadSessionTitles(dir)
	cur := ""
	if a.ctrl != nil {
		cur = a.ctrl.SessionPath()
	}
	out := make([]SessionMeta, 0, len(infos))
	for _, s := range infos {
		out = append(out, SessionMeta{
			Path:    s.Path,
			Preview: s.Preview,
			Title:   titles[filepath.Base(s.Path)],
			Turns:   s.Turns,
			ModTime: s.ModTime.UnixMilli(),
			Current: s.Path == cur,
		})
	}
	return out
}

// DeleteSession removes a saved session (and its title). It refuses the active
// session — that's the conversation on screen, and auto-save would recreate the
// file on the next turn; start a new session first to retire it.
func (a *App) DeleteSession(path string) error {
	if a.ctrl != nil && a.ctrl.SessionPath() == path {
		return errActiveSession
	}
	return deleteSessionFile(config.SessionDir(), path)
}

// RenameSession sets a custom display name for a session (empty clears it back to
// the preview). It only affects the history panel; the file on disk is unchanged.
func (a *App) RenameSession(path, title string) error {
	return setSessionTitle(config.SessionDir(), path, title)
}

// ResumeSession snapshots the current conversation, then loads the session at
// path and continues it — auto-save keeps appending to that file. The model and
// working folder are unchanged (same controller); only the transcript is swapped.
// Returns the resumed messages for the frontend to render.
func (a *App) ResumeSession(path string) ([]HistoryMessage, error) {
	if a.ctrl == nil {
		return []HistoryMessage{}, nil
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		return nil, err
	}
	_ = a.ctrl.Snapshot() // persist the current session before switching away
	a.ctrl.Resume(loaded, path)
	return a.History(), nil
}

// PickWorkspace opens a folder chooser and, on a pick, switches only the active
// tab to that project. Other tabs keep their controllers and workspace roots.
func (a *App) PickWorkspace() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	a.mu.Lock()
	cur := a.activeWorkspaceDirLocked()
	tabID := a.activeTabID
	old := a.tabs[tabID]
	a.mu.Unlock()
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Choose working folder",
		DefaultDirectory: cur,
	})
	if err != nil || dir == "" {
		return "", err // cancelled or error → no change
	}
	if dir == cur {
		return dir, nil
	}
	saveWorkspace(dir) // remember it so the next launch reopens here

	// Resolve the new folder's default model from its own config.
	model := ""
	if cfg, cerr := config.LoadAt(dir); cerr == nil {
		model = cfg.DefaultModel
		if e, ok := cfg.ResolveModel(cfg.DefaultModel); ok {
			model = e.Name + "/" + e.Model
		}
	}
	ctrl, err := boot.Build(a.ctx, boot.Options{Model: model, RequireKey: false, Sink: &eventSink{ctx: a.ctx, tabID: tabID}, CWD: dir})
	if err != nil {
		return "", err
	}
	if old != nil && old.Controller != nil {
		_ = old.Controller.Snapshot()
		old.Controller.Close()
	}
	a.mu.Lock()
	a.ctrl = ctrl
	a.model = model
	a.label = ctrl.Label()
	a.startupErr = ""
	ctrl.EnableInteractiveApproval()
	if d := ctrl.SessionDir(); d != "" {
		ctrl.SetSessionPath(agent.NewSessionPath(d, ctrl.Label()))
	}
	if tab := a.tabs[tabID]; tab != nil {
		tab.Controller = ctrl
		tab.Label = a.label
		tab.Model = a.model
		tab.WorkspaceDir = dir
		tab.Err = ""
	}
	a.mu.Unlock()
	return dir, nil
}

func (a *App) PickFile(filters []PickFileFilter, defaultPath string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	a.mu.Lock()
	cur := a.activeWorkspaceDirLocked()
	a.mu.Unlock()
	if defaultPath = strings.TrimSpace(defaultPath); defaultPath != "" {
		if info, err := os.Stat(defaultPath); err == nil && info.IsDir() {
			cur = defaultPath
		} else if dir := filepath.Dir(defaultPath); dir != "." {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				cur = dir
			}
		}
	}
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Choose file",
		DefaultDirectory: cur,
		Filters:          toDialogFilters(filters),
	})
}

func toDialogFilters(filters []PickFileFilter) []runtime.FileFilter {
	out := make([]runtime.FileFilter, 0, len(filters))
	for _, f := range filters {
		patterns := make([]string, 0, len(f.Extensions))
		for _, ext := range f.Extensions {
			ext = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(ext, "*"), "."))
			if ext == "" {
				continue
			}
			patterns = append(patterns, "*."+ext)
		}
		if len(patterns) == 0 {
			continue
		}
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = "Files"
		}
		out = append(out, runtime.FileFilter{DisplayName: name, Pattern: strings.Join(patterns, ";")})
	}
	return out
}

func (a *App) OpenPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

func (a *App) OpenInEditor(command, path string, line int) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return a.OpenPath(path)
	}
	args := append([]string{}, fields[1:]...)
	target := path
	name := strings.ToLower(filepath.Base(fields[0]))
	if line > 0 && (name == "code" || name == "cursor" || name == "windsurf" || name == "subl") {
		args = append(args, "-g")
		target = path + ":" + strconv.Itoa(line)
	}
	args = append(args, target)
	return exec.Command(fields[0], args...).Start()
}

// HistoryMessage is one prior turn, for the frontend to repopulate its transcript
// after a reload.
type HistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// History returns the session's message log.
func (a *App) History() []HistoryMessage {
	if a.ctrl == nil {
		return nil
	}
	msgs := a.ctrl.History()
	out := make([]HistoryMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, HistoryMessage{Role: string(m.Role), Content: m.Content})
	}
	return out
}

// ContextInfo is the prompt-vs-window gauge payload. Both zero means no data yet.
type ContextInfo struct {
	Used   int `json:"used"`
	Window int `json:"window"`
}

// ContextUsage returns the latest context-window gauge numbers.
func (a *App) ContextUsage() ContextInfo {
	if a.ctrl == nil {
		return ContextInfo{}
	}
	used, window := a.ctrl.ContextSnapshot()
	return ContextInfo{Used: used, Window: window}
}

// BalanceInfo is the wallet-balance readout for the status bar. Available is true
// only when a balance was fetched; Display is the formatted amount (e.g. "¥110.00")
// and is "" when the active provider declares no balance_url — the frontend then
// omits the readout. Err carries a fetch failure for an optional tooltip.
type BalanceInfo struct {
	Available bool   `json:"available"`
	Display   string `json:"display"`
	Err       string `json:"err,omitempty"`
}

// Balance queries the active provider's wallet balance (a network call). It
// returns an empty (unavailable) readout when no provider balance_url is set, the
// controller is down, or the fetch fails — so the status bar simply shows nothing
// rather than an error.
func (a *App) Balance() BalanceInfo {
	if a.ctrl == nil {
		return BalanceInfo{}
	}
	b, err := a.ctrl.Balance(a.ctx)
	if err != nil {
		return BalanceInfo{Err: err.Error()}
	}
	if b == nil {
		return BalanceInfo{} // provider declares no balance endpoint
	}
	return BalanceInfo{Available: true, Display: b.Display()}
}

// JobView is one running background job (bash/task started with
// run_in_background) for the status-bar indicator.
type JobView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	StartedAt int64  `json:"startedAt"`
}

// Jobs returns the still-running background jobs for the status bar. It refreshes
// on demand (mount, turn end, and on each notice the frontend receives).
func (a *App) Jobs() []JobView {
	out := []JobView{}
	if a.ctrl == nil {
		return out
	}
	for _, v := range a.ctrl.Jobs() {
		out = append(out, JobView{ID: v.ID, Kind: v.Kind, Label: v.Label, Status: v.Status, StartedAt: v.StartedAt})
	}
	return out
}

// Meta describes the session for the frontend's header and status line.
type Meta struct {
	Label        string `json:"label"`
	Ready        bool   `json:"ready"`
	StartupErr   string `json:"startupErr,omitempty"`
	EventChannel string `json:"eventChannel"`
	Cwd          string `json:"cwd"`
	Bypass       bool   `json:"bypass"` // YOLO mode on (auto-approve every tool call)
	TabID        string `json:"tabId,omitempty"`
	Opening      bool   `json:"opening,omitempty"`
}

// Meta reports the model label, readiness, any startup error, the working
// directory (for the status line), and the runtime event channel the frontend
// subscribes to.
func (a *App) Meta() Meta {
	a.mu.Lock()
	cwd := a.activeWorkspaceDirLocked()
	opening := a.activeTabID != "" && a.ctrl == nil && a.startupErr == ""
	tabID := a.activeTabID
	label := a.label
	ready := a.ctrl != nil
	startupErr := a.startupErr
	bypass := a.ctrl != nil && a.ctrl.Bypass()
	a.mu.Unlock()
	return Meta{
		Label:        label,
		Ready:        ready,
		StartupErr:   startupErr,
		EventChannel: eventChannel,
		Cwd:          cwd,
		Bypass:       bypass,
		TabID:        tabID,
		Opening:      opening,
	}
}

// SetBypass toggles YOLO mode for the session: auto-approve every tool call
// (writers and bash run without asking). Deny rules still apply. Runtime-only —
// not written to config, so it resets on relaunch.
func (a *App) SetBypass(on bool) {
	if a.ctrl != nil {
		a.ctrl.SetBypass(on)
	}
}

// CommandInfo describes one available slash command for the composer's "/" menu.
type CommandInfo struct {
	Name        string `json:"name"` // without the leading slash
	Description string `json:"description"`
	Hint        string `json:"hint,omitempty"` // argument hint, if any
	Kind        string `json:"kind"`           // "builtin" | "custom" | "mcp"
}

// Commands lists the slash commands available this session — built-in actions,
// custom commands (.reasonix/commands), and MCP prompts — for the composer's "/"
// autocomplete menu.
func (a *App) Commands() []CommandInfo {
	out := []CommandInfo{
		{Name: "new", Description: i18n.M.CmdNew, Kind: "builtin"},
		{Name: "compact", Description: i18n.M.CmdCompact, Kind: "builtin"},
		{Name: "model", Description: i18n.M.CmdModel, Kind: "builtin"},
		{Name: "memory", Description: i18n.M.CmdMemory, Kind: "builtin"},
		{Name: "mcp", Description: i18n.M.CmdMcp, Kind: "builtin"},
		{Name: "hooks", Description: i18n.M.CmdHooks, Kind: "builtin"},
		{Name: "skill", Description: i18n.M.CmdSkill, Kind: "builtin"},
	}
	if a.ctrl == nil {
		return out
	}
	// Skills are invocable as /<name> (the model runs inline ones; subagent ones
	// run isolated). Listing them here is what surfaces /init, /explore, … in the
	// composer's slash menu; selecting one submits "/<name>", which the controller
	// resolves via RunSkill.
	for _, s := range a.ctrl.Skills() {
		out = append(out, CommandInfo{Name: s.Name, Description: s.Description, Kind: "skill"})
	}
	for _, c := range a.ctrl.Commands() {
		out = append(out, CommandInfo{Name: c.Name, Description: c.Description, Hint: c.ArgHint, Kind: "custom"})
	}
	if h := a.ctrl.Host(); h != nil {
		for _, p := range h.Prompts() {
			out = append(out, CommandInfo{Name: p.Name, Description: p.Description, Kind: "mcp"})
		}
	}
	return out
}

// SlashArgItem is one sub-command / argument suggestion for the composer's slash
// menu (the part after the command word). Mirrors the CLI's arg completion via
// the shared control.SlashArgItems, so desktop and CLI offer the same hints.
type SlashArgItem struct {
	Label   string `json:"label"`
	Insert  string `json:"insert"`
	Hint    string `json:"hint"`
	Descend bool   `json:"descend"`
}

// SlashArgsResult carries the suggestions plus the byte offset in the input where
// the current token begins, so the composer replaces just that token.
type SlashArgsResult struct {
	Items []SlashArgItem `json:"items"`
	From  int            `json:"from"`
}

// SlashArgs completes the arguments of a management slash command (/mcp, /model,
// /skill, /hooks) for the composer — the same logic the chat TUI uses. Empty
// Items means the input has no structured arguments to complete.
func (a *App) SlashArgs(input string) SlashArgsResult {
	if a.ctrl == nil {
		return SlashArgsResult{}
	}
	data := control.ArgData{
		Skills:       a.ctrl.Skills(),
		CurrentModel: a.model,
	}
	for _, m := range a.Models() {
		data.ModelRefs = append(data.ModelRefs, m.Ref)
	}
	if h := a.ctrl.Host(); h != nil {
		data.ServerNames = h.ServerNames()
	}
	data.ConfiguredMCP = a.ctrl.ConfiguredMCPNames()
	data.DisconnectedMCP = a.ctrl.DisconnectedMCPNames()
	items, from := control.SlashArgItems(input, data)
	// Non-nil so it serializes as a JSON array, never null — the frontend filters
	// over it directly.
	out := SlashArgsResult{Items: []SlashArgItem{}, From: from}
	for _, it := range items {
		out.Items = append(out.Items, SlashArgItem{Label: it.Label, Insert: it.Insert, Hint: it.Hint, Descend: it.Descend})
	}
	return out
}

// MCPToolInfo describes one tool exposed by a connected MCP server.
type MCPToolInfo struct {
	Name           string `json:"name"`
	RegisteredName string `json:"registeredName"`
	Description    string `json:"description,omitempty"`
}

// ImportedMCPServer mirrors the settings UI's editable MCP server shape.
type ImportedMCPServer struct {
	Name             string            `json:"name"`
	Transport        string            `json:"transport"`
	Command          string            `json:"command,omitempty"`
	Args             []string          `json:"args,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	CWD              string            `json:"cwd,omitempty"`
	URL              string            `json:"url,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	Disabled         bool              `json:"disabled,omitempty"`
	RequestTimeoutMs int               `json:"requestTimeoutMs,omitempty"`
}

// MCPSpecInfo is the desktop settings/context-panel view of one MCP server.
type MCPSpecInfo struct {
	Raw          string             `json:"raw"`
	Name         string             `json:"name,omitempty"`
	Transport    string             `json:"transport"`
	Summary      string             `json:"summary"`
	Config       *ImportedMCPServer `json:"config,omitempty"`
	Status       string             `json:"status"`
	StatusHint   string             `json:"statusHint,omitempty"`
	StatusReason string             `json:"statusReason,omitempty"`
	ToolCount    int                `json:"toolCount,omitempty"`
	Tools        []MCPToolInfo      `json:"tools,omitempty"`
}

type MCPSpecsResult struct {
	Specs   []MCPSpecInfo `json:"specs"`
	Bridged bool          `json:"bridged"`
}

type MCPImportResult struct {
	Total     int `json:"total"`
	Added     int `json:"added"`
	Updated   int `json:"updated"`
	Connected int `json:"connected"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

func (a *App) MCPSpecs() MCPSpecsResult {
	a.mu.Lock()
	cwd := a.activeWorkspaceDirLocked()
	ctrl := a.ctrl
	a.mu.Unlock()
	cfg, err := config.LoadAt(cwd)
	if err != nil {
		return MCPSpecsResult{Specs: []MCPSpecInfo{}, Bridged: true}
	}
	live := map[string]plugin.ServerStatus{}
	failed := map[string]plugin.Failure{}
	if ctrl != nil && ctrl.Host() != nil {
		for _, s := range ctrl.Host().Servers() {
			live[s.Name] = s
		}
		for _, f := range ctrl.Host().Failures() {
			failed[f.Name] = f
		}
	}
	out := make([]MCPSpecInfo, 0, len(cfg.Plugins)+len(live)+len(failed))
	seen := map[string]bool{}
	for _, entry := range cfg.Plugins {
		spec := mcpSpecFromEntry(entry)
		if s, ok := live[entry.Name]; ok {
			applyLiveMCPStatus(&spec, s)
		} else if f, ok := failed[entry.Name]; ok {
			applyFailedMCPStatus(&spec, f)
		} else if !entry.ShouldAutoStart() {
			spec.Status = "disabled"
		}
		out = append(out, spec)
		seen[entry.Name] = true
	}
	for _, s := range live {
		if seen[s.Name] {
			continue
		}
		spec := MCPSpecInfo{Raw: s.Name, Name: s.Name, Transport: uiTransport(s.Transport), Summary: s.Transport, Status: "connected"}
		applyLiveMCPStatus(&spec, s)
		out = append(out, spec)
		seen[s.Name] = true
	}
	for _, f := range failed {
		if seen[f.Name] {
			continue
		}
		spec := MCPSpecInfo{Raw: f.Name, Name: f.Name, Transport: uiTransport(f.Transport), Summary: f.Transport}
		applyFailedMCPStatus(&spec, f)
		out = append(out, spec)
	}
	return MCPSpecsResult{Specs: out, Bridged: true}
}

func (a *App) ImportCcSwitchMCP() (MCPImportResult, error) {
	if a.ctrl == nil {
		return MCPImportResult{}, nil
	}
	total, added, updated, connected, failed, skipped, err := a.ctrl.ImportCCSwitchMCPServers()
	return MCPImportResult{Total: total, Added: added, Updated: updated, Connected: connected, Failed: failed, Skipped: skipped}, err
}

func (a *App) AddMCPServer(raw string) (int, error) {
	if a.ctrl == nil {
		return 0, nil
	}
	entry, err := parseRawMCPServer(raw)
	if err != nil {
		return 0, err
	}
	return a.ctrl.AddMCPServer(entry)
}

func (a *App) RemoveMCPServer(raw string) (bool, error) {
	if a.ctrl == nil {
		return false, nil
	}
	return a.ctrl.RemoveMCPServer(mcpNameFromRaw(raw))
}

func (a *App) UpdateMCPServer(raw string, server ImportedMCPServer) (int, error) {
	if a.ctrl == nil {
		return 0, nil
	}
	entry := pluginEntryFromImported(server)
	return a.ctrl.UpsertMCPServer(mcpNameFromRaw(raw), entry, !server.Disabled)
}

func (a *App) RetryMCPServer(raw string) (int, error) {
	if a.ctrl == nil {
		return 0, nil
	}
	return a.ctrl.RetryMCPServer(mcpNameFromRaw(raw))
}

func mcpSpecFromEntry(entry config.PluginEntry) MCPSpecInfo {
	server := importedFromPluginEntry(entry)
	return MCPSpecInfo{
		Raw:       entry.Name,
		Name:      entry.Name,
		Transport: server.Transport,
		Summary:   mcpSummary(entry),
		Config:    &server,
		Status:    "configured",
	}
}

func applyLiveMCPStatus(spec *MCPSpecInfo, s plugin.ServerStatus) {
	spec.Status = "connected"
	spec.Transport = uiTransport(s.Transport)
	spec.ToolCount = s.Tools
	spec.Tools = make([]MCPToolInfo, 0, len(s.ToolInfos))
	for _, t := range s.ToolInfos {
		spec.Tools = append(spec.Tools, MCPToolInfo{Name: t.Name, RegisteredName: t.RegisteredName, Description: t.Description})
	}
}

func applyFailedMCPStatus(spec *MCPSpecInfo, f plugin.Failure) {
	spec.Status = "failed"
	spec.Transport = uiTransport(f.Transport)
	spec.StatusReason = f.Error
	spec.StatusHint = mcpStatusHint(f.Error)
}

func importedFromPluginEntry(entry config.PluginEntry) ImportedMCPServer {
	transport := uiTransport(entry.Type)
	server := ImportedMCPServer{
		Name:             entry.Name,
		Transport:        transport,
		Command:          entry.Command,
		Args:             append([]string(nil), entry.Args...),
		Env:              cloneStringMap(entry.Env),
		CWD:              entry.CWD,
		URL:              entry.URL,
		Headers:          cloneStringMap(entry.Headers),
		Disabled:         !entry.ShouldAutoStart(),
		RequestTimeoutMs: entry.RequestTimeoutMs,
	}
	return server
}

func pluginEntryFromImported(server ImportedMCPServer) config.PluginEntry {
	autoStart := !server.Disabled
	return config.PluginEntry{
		Name:             strings.TrimSpace(server.Name),
		Type:             configTransport(server.Transport),
		Command:          strings.TrimSpace(server.Command),
		Args:             append([]string(nil), server.Args...),
		Env:              cloneStringMap(server.Env),
		CWD:              strings.TrimSpace(server.CWD),
		URL:              strings.TrimSpace(server.URL),
		Headers:          cloneStringMap(server.Headers),
		AutoStart:        &autoStart,
		RequestTimeoutMs: server.RequestTimeoutMs,
	}
}

func parseRawMCPServer(raw string) (config.PluginEntry, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return config.PluginEntry{}, fmt.Errorf("empty MCP spec")
	}
	name, body, ok := strings.Cut(raw, "=")
	if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(body) == "" {
		return config.PluginEntry{}, fmt.Errorf("MCP spec must be name=command or name=url")
	}
	name = strings.TrimSpace(name)
	body = strings.TrimSpace(body)
	if strings.HasPrefix(strings.ToLower(body), "streamable+") {
		return config.PluginEntry{Name: name, Type: "http", URL: body[len("streamable+"):], AutoStart: boolPtr(true)}, nil
	}
	if strings.HasPrefix(body, "http://") || strings.HasPrefix(body, "https://") {
		return config.PluginEntry{Name: name, Type: "sse", URL: body, AutoStart: boolPtr(true)}, nil
	}
	argv, err := splitShellLike(body)
	if err != nil {
		return config.PluginEntry{}, err
	}
	if len(argv) == 0 {
		return config.PluginEntry{}, fmt.Errorf("MCP stdio spec needs a command")
	}
	return config.PluginEntry{Name: name, Command: argv[0], Args: argv[1:], AutoStart: boolPtr(true)}, nil
}

func splitShellLike(s string) ([]string, error) {
	var out []string
	var b strings.Builder
	var quote rune
	escaping := false
	for _, r := range s {
		if escaping {
			b.WriteRune(r)
			escaping = false
			continue
		}
		if r == '\\' {
			escaping = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' {
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteRune(r)
	}
	if escaping {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in MCP spec")
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out, nil
}

func mcpNameFromRaw(raw string) string {
	raw = strings.TrimSpace(raw)
	if name, _, ok := strings.Cut(raw, "="); ok {
		return strings.TrimSpace(name)
	}
	return raw
}

func mcpSummary(entry config.PluginEntry) string {
	if entry.URL != "" {
		return entry.URL
	}
	parts := append([]string{entry.Command}, entry.Args...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func uiTransport(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "http", "streamable-http", "streamable_http":
		return "streamable-http"
	case "sse":
		return "sse"
	default:
		return "stdio"
	}
}

func configTransport(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "streamable-http", "streamable_http", "http":
		return "http"
	case "sse":
		return "sse"
	default:
		return ""
	}
}

func mcpStatusHint(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "token") || strings.Contains(lower, "api key") || strings.Contains(lower, "apikey"):
		return "missing-token"
	case strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden") || strings.Contains(lower, "401") || strings.Contains(lower, "403"):
		return "auth"
	case strings.Contains(lower, "executable file not found") || strings.Contains(lower, "no such file") || strings.Contains(lower, "command"):
		return "command"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "connection") || strings.Contains(lower, "network"):
		return "network"
	default:
		return "unknown"
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func boolPtr(v bool) *bool { return &v }

// ModelInfo is one (provider, model) the bottom switcher can pick. Ref ("provider/
// model") is what SetModel takes; Provider/Model are for display.
type ModelInfo struct {
	Ref      string `json:"ref"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Current  bool   `json:"current"`
}

// Models flattens the configured providers into their (provider, model) pairs —
// the switcher's options — marking the active one. A vendor with a `models` list
// yields one entry per model, all sharing the same endpoint/key.
func (a *App) Models() []ModelInfo {
	a.mu.Lock()
	cwd := a.activeWorkspaceDirLocked()
	a.mu.Unlock()
	cfg, err := config.LoadAt(cwd)
	if err != nil {
		return nil
	}
	var out []ModelInfo
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		for _, m := range p.ModelList() {
			ref := p.Name + "/" + m
			out = append(out, ModelInfo{Ref: ref, Provider: p.Name, Model: m, Current: ref == a.model})
		}
	}
	return out
}

// SetModel switches the active model and carries the current conversation into the
// new model's session, so the chat continues seamlessly and subsequent turns use
// the new model. (Switching models necessarily resets the prompt cache; that's the
// cost of the switch.) No-op if name is already active or the controller is down.
func (a *App) SetModel(name string) error {
	if a.ctx == nil || name == "" || name == a.model {
		return nil
	}

	var carried []provider.Message
	a.mu.Lock()
	tabID := a.activeTabID
	cwd := a.activeWorkspaceDirLocked()
	a.mu.Unlock()
	if a.ctrl != nil {
		_ = a.ctrl.Snapshot()
		carried = a.ctrl.History()
		a.ctrl.Close()
	}

	ctrl, err := boot.Build(a.ctx, boot.Options{Model: name, RequireKey: false, Sink: &eventSink{ctx: a.ctx, tabID: tabID}, CWD: cwd})
	if err != nil {
		return err
	}
	a.ctrl = ctrl
	a.model = name
	a.label = ctrl.Label()
	ctrl.EnableInteractiveApproval()

	path := ""
	if dir := ctrl.SessionDir(); dir != "" {
		path = agent.NewSessionPath(dir, ctrl.Label())
	}
	// Carry the prior conversation (full provider.Message log, incl. the system
	// prompt) into the new session so history is preserved across the switch.
	if len(carried) > 0 {
		ctrl.Resume(&agent.Session{Messages: carried}, path)
	} else if path != "" {
		ctrl.SetSessionPath(path)
	}
	a.mu.Lock()
	if tab := a.tabs[tabID]; tab != nil {
		tab.Controller = ctrl
		tab.Label = a.label
		tab.Model = a.model
		tab.Err = ""
	}
	a.mu.Unlock()
	return nil
}

// DirEntry is one entry in the "@" file-reference menu.
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
}

// atSkip are entries the "@" menu hides as noise.
var atSkip = map[string]bool{".git": true, "node_modules": true, ".DS_Store": true}

// ListDir lists one directory level (directories first, then files, each
// alphabetical) for the "@" file-reference menu. rel resolves against the process
// cwd; "" lists the cwd. The menu navigates one level at a time, never
// recursively — bounded for huge trees.
func (a *App) ListDir(rel string) []DirEntry {
	a.mu.Lock()
	base := a.activeWorkspaceDirLocked()
	a.mu.Unlock()
	dir := base
	if rel != "" {
		if filepath.IsAbs(rel) {
			dir = filepath.Clean(rel)
		} else {
			dir = filepath.Join(base, rel)
		}
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var dirs, files []DirEntry
	for _, e := range es {
		name := e.Name()
		if atSkip[name] {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, DirEntry{Name: name, IsDir: true})
		} else {
			files = append(files, DirEntry{Name: name, IsDir: false})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	return append(dirs, files...)
}

// --- memory panel (frontend ⇄ controller) ---

// MemoryDoc is one loaded doc-memory file for the panel: path, scope, and body.
type MemoryDoc struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
	Body  string `json:"body"`
}

// MemoryFact is one saved auto-memory, surfaced read-only in the panel.
type MemoryFact struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Body        string `json:"body"`
}

// MemoryScope is one writable quick-add target (scope id + the file it writes to).
type MemoryScope struct {
	Scope string `json:"scope"`
	Path  string `json:"path"`
}

// MemoryView is the whole memory panel payload: hierarchical docs, saved facts,
// and the writable scopes for the quick-add selector.
type MemoryView struct {
	Docs      []MemoryDoc   `json:"docs"`
	Facts     []MemoryFact  `json:"facts"`
	Scopes    []MemoryScope `json:"scopes"`
	StoreDir  string        `json:"storeDir"`
	Available bool          `json:"available"`
}

// writableScopes are the quick-add targets the panel offers, broad → specific.
var writableScopes = []memory.Scope{memory.ScopeUser, memory.ScopeProject, memory.ScopeLocal}

// Memory returns the loaded memory for the panel: the REASONIX.md hierarchy, the
// saved auto-memories, and the writable scopes. Read-only; mutations go through
// Remember / SaveDoc.
func (a *App) Memory() MemoryView {
	// Always return non-nil slices: a nil Go slice marshals to JSON `null`, which
	// would crash the panel's `view.facts.length` / `.map`.
	view := MemoryView{Docs: []MemoryDoc{}, Facts: []MemoryFact{}, Scopes: []MemoryScope{}}
	if a.ctrl == nil {
		return view
	}
	set := a.ctrl.Memory()
	if set == nil {
		return view
	}
	view.StoreDir = set.Store.Dir
	view.Available = true
	for _, d := range set.Docs {
		view.Docs = append(view.Docs, MemoryDoc{Path: d.Path, Scope: string(d.Scope), Body: d.Body})
	}
	for _, f := range set.Store.List() {
		view.Facts = append(view.Facts, MemoryFact{
			Name: f.Name, Description: f.Description, Type: string(f.Type), Body: f.Body,
		})
	}
	for _, sc := range writableScopes {
		if p := set.DocPath(sc); p != "" { // user scope yields "" when no config dir
			view.Scopes = append(view.Scopes, MemoryScope{Scope: string(sc), Path: p})
		}
	}
	return view
}

// Remember quick-adds a one-line note to the doc-memory file for scope — the
// panel's explicit "remember" action, equivalent to typing "#<note>". An unknown
// scope falls back to project. Returns the file written.
func (a *App) Remember(scope, note string) (string, error) {
	if a.ctrl == nil {
		return "", nil
	}
	return a.ctrl.QuickAdd(parseScope(scope), note)
}

// SaveDoc overwrites a memory doc with the panel editor's contents. The controller
// validates path against the recognized memory files. Returns the file written.
func (a *App) SaveDoc(path, body string) (string, error) {
	if a.ctrl == nil {
		return "", nil
	}
	return a.ctrl.SaveDoc(path, body)
}

// parseScope maps a frontend scope id to a memory.Scope, defaulting to project.
func parseScope(s string) memory.Scope {
	switch memory.Scope(s) {
	case memory.ScopeUser:
		return memory.ScopeUser
	case memory.ScopeLocal:
		return memory.ScopeLocal
	default:
		return memory.ScopeProject
	}
}

// eventSink is the controller's event.Sink in desktop mode: it forwards every
// agent event to the webview as one runtime event, JSON-shaped by toWire. It is a
// type distinct from App so App's bound method set stays the clean command surface
// — Emit must not be exposed to JS. Emit runs on the agent goroutine;
// runtime.EventsEmit is goroutine-safe, and the ctx guard covers the brief window
// before startup assigns it.
type eventSink struct {
	ctx   context.Context
	tabID string
}

func (s *eventSink) Emit(e event.Event) {
	if s.ctx == nil {
		return
	}
	w := toWire(e)
	w.TabID = s.tabID
	runtime.EventsEmit(s.ctx, eventChannel, w)
}

func (a *App) emitTabReady(tabID, errText string) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, eventChannel, wireEvent{Kind: "tab_ready", TabID: tabID, Text: errText})
}
