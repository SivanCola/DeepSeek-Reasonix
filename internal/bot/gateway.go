package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/fileutil"
)

// GatewayConfig 是 BotGateway 的配置。
type GatewayConfig struct {
	Model              string
	MaxSteps           int
	WorkspaceRoot      string
	SessionDir         string
	SessionSearchDirs  []string
	SessionMappingPath string
	SessionMappings    []SessionMapping
	Channels           map[Platform]ChannelConfig
	Allowlist          AllowlistConfig
	Enabled            map[Platform]bool
	Debounce           time.Duration
}

// ChannelConfig overrides gateway defaults for one IM channel.
type ChannelConfig struct {
	Model         string
	WorkspaceRoot string
}

// AllowlistConfig 控制哪些用户/群可以使用 bot。
type AllowlistConfig struct {
	Enabled  bool
	AllowAll bool
	Users    map[Platform][]string
	Groups   map[Platform][]string
}

// SessionMapping binds a remote IM session key to a Reasonix session file.
type SessionMapping struct {
	RemoteKey     string    `json:"remote_key"`
	SessionPath   string    `json:"session_path"`
	SessionID     string    `json:"session_id,omitempty"`
	WorkspaceRoot string    `json:"workspace_root,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type sessionMappingFile struct {
	Version  int              `json:"version"`
	Mappings []SessionMapping `json:"mappings"`
}

// BotGateway 是 reasonix bot 消息网关，管理 Controller 生命周期、session 并发、
// 事件渲染和平台适配器。
type BotGateway struct {
	cfg      GatewayConfig
	adapters map[Platform]Adapter
	sessions *SessionManager

	mu             sync.Mutex
	controllers    map[string]*sessionState // session key -> active state
	mappings       map[string]SessionMapping
	allowlist      map[Platform]map[string]bool
	groupAllowlist map[Platform]map[string]bool

	logger *slog.Logger
}

type sessionState struct {
	ctrl        *control.Controller
	sink        *sessionEventSink
	cancel      context.CancelFunc
	pendingAsks map[string][]event.AskQuestion
	createdAt   time.Time
	lastActive  time.Time
}

type sessionEventSink struct {
	mu     sync.RWMutex
	target event.Sink
}

type pendingReactionAdapter interface {
	AddPendingReaction(ctx context.Context, messageID string) error
}

func (s *sessionEventSink) setTarget(target event.Sink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = target
}

func (s *sessionEventSink) Emit(e event.Event) {
	s.mu.RLock()
	target := s.target
	s.mu.RUnlock()
	if target != nil {
		target.Emit(e)
	}
}

// NewGateway 创建一个新的 BotGateway。
func NewGateway(cfg GatewayConfig, adapters map[Platform]Adapter, logger *slog.Logger) *BotGateway {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = 1500 * time.Millisecond
	}
	gw := &BotGateway{
		cfg:            cfg,
		adapters:       adapters,
		sessions:       NewSessionManager(cfg.Debounce),
		controllers:    make(map[string]*sessionState),
		mappings:       make(map[string]SessionMapping),
		allowlist:      make(map[Platform]map[string]bool),
		groupAllowlist: make(map[Platform]map[string]bool),
		logger:         logger.With("component", "bot_gateway"),
	}
	gw.loadSessionMappings()
	gw.buildAllowlist()
	return gw
}

func (gw *BotGateway) buildAllowlist() {
	for _, plat := range []Platform{PlatformQQ, PlatformFeishu, PlatformWeixin} {
		gw.allowlist[plat] = make(map[string]bool)
		if !gw.cfg.Allowlist.Enabled {
			continue
		}
		for _, uid := range gw.cfg.Allowlist.Users[plat] {
			gw.allowlist[plat][uid] = true
		}
		gw.groupAllowlist[plat] = make(map[string]bool)
		for _, gid := range gw.cfg.Allowlist.Groups[plat] {
			gw.groupAllowlist[plat][gid] = true
		}
	}
}

// Start 启动所有已启用的平台适配器并开始处理消息。
func (gw *BotGateway) Start(ctx context.Context) error {
	for plat, adapter := range gw.adapters {
		if !gw.cfg.Enabled[plat] {
			gw.logger.Info("platform disabled, skipping", "platform", plat)
			continue
		}
		gw.logger.Info("starting adapter", "platform", plat)
		if err := adapter.Start(ctx); err != nil {
			return fmt.Errorf("start adapter %s: %w", plat, err)
		}
	}

	// 合并所有适配器的消息通道
	for plat, adapter := range gw.adapters {
		if !gw.cfg.Enabled[plat] {
			continue
		}
		go gw.dispatchLoop(ctx, plat, adapter)
	}

	return nil
}

// Stop 停止所有适配器并关闭所有 session。
func (gw *BotGateway) Stop() {
	gw.mu.Lock()
	for key, state := range gw.controllers {
		if state.cancel != nil {
			state.cancel()
		}
		state.ctrl.Close()
		delete(gw.controllers, key)
	}
	gw.mu.Unlock()

	for _, adapter := range gw.adapters {
		if err := adapter.Stop(); err != nil {
			gw.logger.Warn("error stopping adapter", "err", err)
		}
	}
}

func (gw *BotGateway) dispatchLoop(ctx context.Context, plat Platform, adapter Adapter) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-adapter.Messages():
			if !ok {
				return
			}
			gw.handleMessage(ctx, plat, adapter, msg)
		}
	}
}

func (gw *BotGateway) handleMessage(ctx context.Context, plat Platform, adapter Adapter, msg InboundMessage) {
	msg.Platform = plat

	// allowlist 检查
	if !gw.checkAllowlist(plat, msg) {
		gw.logger.Info("user not in allowlist", "platform", plat, "user", hashID(msg.UserID))
		_ = gw.sendText(ctx, adapter, msg, "抱歉，您没有使用此 bot 的权限。")
		return
	}

	src := msg.Session()
	key := BuildSessionKey(src)

	// 斜杠命令处理
	if IsSlashBypass(msg.Text) {
		gw.handleSlashCommand(ctx, adapter, key, msg)
		return
	}

	gw.addPendingReaction(ctx, plat, adapter, msg)

	// session 并发控制
	acquired, merged := gw.sessions.TryAcquire(key, msg)
	if merged {
		gw.logger.Debug("message merged to pending queue", "session", key[:8])
		return
	}
	if !acquired {
		// 正在处理中且非 bypass 命令，已在 TryAcquire 中入队
		gw.logger.Debug("session busy, queued", "session", key[:8])
		return
	}

	gw.runTurn(ctx, adapter, key, msg)
}

func (gw *BotGateway) addPendingReaction(ctx context.Context, plat Platform, adapter Adapter, msg InboundMessage) {
	if strings.TrimSpace(msg.MessageID) == "" {
		return
	}
	reactor, ok := adapter.(pendingReactionAdapter)
	if !ok {
		return
	}
	if err := reactor.AddPendingReaction(ctx, msg.MessageID); err != nil {
		gw.logger.Warn("pending reaction failed", "platform", plat, "err", err)
	}
}

func (gw *BotGateway) checkAllowlist(plat Platform, msg InboundMessage) bool {
	if gw.cfg.Allowlist.AllowAll {
		return true
	}
	if !gw.cfg.Allowlist.Enabled {
		return false
	}
	if !gw.allowlist[plat][msg.UserID] {
		return false
	}
	groups := gw.groupAllowlist[plat]
	if chatUsesGroupAllowlist(msg.ChatType) && len(groups) > 0 && !groups[msg.ChatID] {
		return false
	}
	return true
}

func chatUsesGroupAllowlist(chatType ChatType) bool {
	switch chatType {
	case ChatGroup, ChatGuild, ChatThread:
		return true
	default:
		return false
	}
}

func (gw *BotGateway) handleSlashCommand(ctx context.Context, adapter Adapter, key string, msg InboundMessage) {
	switch {
	case strings.HasPrefix(msg.Text, "/stop"):
		gw.mu.Lock()
		state, ok := gw.controllers[key]
		gw.mu.Unlock()
		if ok && state.cancel != nil {
			state.cancel()
		}
		gw.sessions.ForceRelease(key)
		_ = gw.sendText(ctx, adapter, msg, "已停止当前任务。")

	case strings.HasPrefix(msg.Text, "/new") || strings.HasPrefix(msg.Text, "/reset"):
		gw.mu.Lock()
		state, ok := gw.controllers[key]
		gw.mu.Unlock()
		if ok {
			if state.cancel != nil {
				state.cancel()
			}
			if err := state.ctrl.NewSession(); err != nil {
				gw.logger.Warn("new session failed", "err", err)
				_ = gw.sendText(ctx, adapter, msg, "新会话创建失败："+err.Error())
				return
			}
			gw.ensureControllerSessionMapping(key, msg.Platform, state.ctrl)
		}
		gw.sessions.ForceRelease(key)
		_ = gw.sendText(ctx, adapter, msg, "已开始新会话。")

	case strings.HasPrefix(msg.Text, "/approve"):
		// 从消息中解析 approval ID
		parts := strings.Fields(msg.Text)
		if len(parts) < 2 {
			_ = gw.sendText(ctx, adapter, msg, "用法: /approve <id>")
			return
		}
		gw.mu.Lock()
		state, ok := gw.controllers[key]
		gw.mu.Unlock()
		if ok {
			state.ctrl.Approve(parts[1], true, false, false)
			_ = gw.sendText(ctx, adapter, msg, "已批准。")
		}

	case strings.HasPrefix(msg.Text, "/deny"):
		parts := strings.Fields(msg.Text)
		if len(parts) < 2 {
			_ = gw.sendText(ctx, adapter, msg, "用法: /deny <id>")
			return
		}
		gw.mu.Lock()
		state, ok := gw.controllers[key]
		gw.mu.Unlock()
		if ok {
			state.ctrl.Approve(parts[1], false, false, false)
			_ = gw.sendText(ctx, adapter, msg, "已拒绝。")
		}

	case strings.HasPrefix(msg.Text, "/answer"):
		parts := strings.Fields(msg.Text)
		if len(parts) < 3 {
			_ = gw.sendText(ctx, adapter, msg, "用法: /answer <id> <选项或 q1=选项;q2=选项>")
			return
		}
		askID := parts[1]
		rawAnswer := strings.TrimSpace(strings.Join(parts[2:], " "))
		gw.mu.Lock()
		state, ok := gw.controllers[key]
		var questions []event.AskQuestion
		if ok {
			questions = state.pendingAsks[askID]
			delete(state.pendingAsks, askID)
		}
		gw.mu.Unlock()
		if !ok || state.ctrl == nil {
			_ = gw.sendText(ctx, adapter, msg, "没有找到当前会话。")
			return
		}
		answers := parseAskAnswers(questions, rawAnswer)
		state.ctrl.AnswerQuestion(askID, answers)
		_ = gw.sendText(ctx, adapter, msg, "已提交回答。")

	case strings.HasPrefix(msg.Text, "/status"):
		active := gw.sessions.ActiveCount()
		gw.mu.Lock()
		sessions := len(gw.controllers)
		state, hasState := gw.controllers[key]
		gw.mu.Unlock()
		var goalInfo string
		if hasState && state.ctrl != nil {
			goal := state.ctrl.Goal()
			goalStatus := state.ctrl.GoalStatus()
			running := state.ctrl.Running()
			if goal != "" {
				goalInfo = fmt.Sprintf("\n目标: %s\n目标状态: %s", goal, goalStatus)
			} else {
				goalInfo = fmt.Sprintf("\n目标状态: %s", goalStatus)
			}
			if running {
				goalInfo += "\n运行状态: running"
			} else {
				goalInfo += "\n运行状态: idle"
			}
		}
		_ = gw.sendText(ctx, adapter, msg, fmt.Sprintf("活跃任务数: %d\n保留会话数: %d%s", active, sessions, goalInfo))

	case strings.HasPrefix(msg.Text, "/sessions"):
		_ = gw.sendText(ctx, adapter, msg, gw.renderSessionList(8))

	case strings.HasPrefix(msg.Text, "/attach"):
		parts := strings.Fields(msg.Text)
		if len(parts) < 2 {
			_ = gw.sendText(ctx, adapter, msg, "用法: /attach <session-id-or-path>")
			return
		}
		if err := gw.attachSession(ctx, key, msg, parts[1]); err != nil {
			_ = gw.sendText(ctx, adapter, msg, "绑定失败："+err.Error())
			return
		}
		mapping, _ := gw.sessionMapping(key)
		_ = gw.sendText(ctx, adapter, msg, fmt.Sprintf("已绑定会话：%s", shortSessionID(mapping.SessionPath)))

	case strings.HasPrefix(msg.Text, "/detach"):
		if ok := gw.clearSessionMapping(key); ok {
			gw.mu.Lock()
			if state, exists := gw.controllers[key]; exists {
				if state.cancel != nil {
					state.cancel()
				}
				if state.ctrl != nil {
					state.ctrl.Close()
				}
				delete(gw.controllers, key)
			}
			gw.mu.Unlock()
			gw.sessions.ForceRelease(key)
			_ = gw.sendText(ctx, adapter, msg, "已解除当前 IM 会话绑定。")
		} else {
			_ = gw.sendText(ctx, adapter, msg, "当前 IM 会话没有绑定 Reasonix session。")
		}

	case strings.HasPrefix(msg.Text, "/goal"):
		gw.handleGoalCommand(ctx, adapter, key, msg)

	case strings.HasPrefix(msg.Text, "/help"):
		help := "可用命令:\n" +
			"/stop - 停止当前任务\n" +
			"/new - 开始新会话\n" +
			"/reset - 重置会话\n" +
			"/goal <text> - 设置目标\n" +
			"/goal continue - 继续执行目标\n" +
			"/goal status - 查看目标\n" +
			"/goal clear - 清除目标\n" +
			"/approve <id> - 批准操作\n" +
			"/deny <id> - 拒绝操作\n" +
			"/answer <id> <选项> - 回答 ask 问题\n" +
			"/sessions - 列出可绑定的 Reasonix 会话\n" +
			"/attach <id> - 绑定并恢复已有 Reasonix 会话\n" +
			"/detach - 解除当前 IM 会话绑定\n" +
			"/status - 查看状态\n" +
			"/help - 显示帮助"
		_ = gw.sendText(ctx, adapter, msg, help)
	}
}

func (gw *BotGateway) runTurn(ctx context.Context, adapter Adapter, key string, msg InboundMessage) {
	defer func() {
		// 检查是否有等待队列中的消息
		next := gw.sessions.Release(key)
		if next != nil {
			gw.runTurn(ctx, adapter, key, *next)
			return
		}
	}()

	// 构建输入文本：群聊中在消息前加上发送者名
	input := msg.Text
	if msg.ChatType == ChatGroup {
		input = fmt.Sprintf("[%s] %s", msg.UserName, msg.Text)
	}

	// 获取或创建 Controller
	state := gw.getOrCreateSession(ctx, key, msg)
	if state == nil || state.ctrl == nil {
		_ = gw.sendText(ctx, adapter, msg, "内部错误：无法创建会话。")
		return
	}

	// 发送"正在输入"状态
	_ = adapter.SendTyping(ctx, msg.ChatID)

	// 创建事件渲染 sink
	sink := newRenderSink(ctx, adapter, msg.ChatID, msg.ChatType, msg.MessageID, gw.logger, func(ask event.Ask) {
		gw.mu.Lock()
		if state.pendingAsks == nil {
			state.pendingAsks = make(map[string][]event.AskQuestion)
		}
		state.pendingAsks[ask.ID] = ask.Questions
		gw.mu.Unlock()
	})
	state.sink.setTarget(sink)
	defer state.sink.setTarget(nil)

	// 创建带取消的 context
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	gw.mu.Lock()
	state.cancel = cancel
	state.lastActive = time.Now()
	gw.mu.Unlock()

	// 运行一轮对话
	sink.ctrl = state.ctrl
	err := state.ctrl.RunTurn(turnCtx, input)
	sink.Emit(event.Event{Kind: event.TurnDone, Err: err})
	if err != nil {
		gw.logger.Warn("turn error", "session", key[:8], "err", err)
	}
}

func (gw *BotGateway) getOrCreateSession(ctx context.Context, key string, msg InboundMessage) *sessionState {
	gw.mu.Lock()
	if state, ok := gw.controllers[key]; ok {
		state.lastActive = time.Now()
		gw.mu.Unlock()
		return state
	}
	gw.mu.Unlock()

	if mapping, ok := gw.sessionMapping(key); ok {
		if state := gw.resumeMappedSession(ctx, key, msg, mapping); state != nil {
			return state
		}
	}

	// 创建新 Controller
	sessionSink := &sessionEventSink{}
	ctrl, err := gw.buildController(ctx, msg.Platform, sessionSink)
	if err != nil {
		gw.logger.Error("build controller failed", "err", err)
		return nil
	}
	ctrl.EnableInteractiveApproval()
	gw.ensureControllerSessionMapping(key, msg.Platform, ctrl)

	gw.mu.Lock()
	gw.controllers[key] = &sessionState{
		ctrl:        ctrl,
		sink:        sessionSink,
		pendingAsks: make(map[string][]event.AskQuestion),
		createdAt:   time.Now(),
		lastActive:  time.Now(),
	}
	state := gw.controllers[key]
	gw.mu.Unlock()

	return state
}

func (gw *BotGateway) buildController(ctx context.Context, plat Platform, sink event.Sink) (*control.Controller, error) {
	model, workspaceRoot := gw.sessionOptionsForPlatform(plat)
	return boot.Build(ctx, boot.Options{
		Model:         model,
		MaxSteps:      gw.cfg.MaxSteps,
		RequireKey:    true,
		Sink:          sink,
		WorkspaceRoot: workspaceRoot,
		SessionDir:    gw.cfg.SessionDir,
	})
}

func (gw *BotGateway) resumeMappedSession(ctx context.Context, key string, msg InboundMessage, mapping SessionMapping) *sessionState {
	if strings.TrimSpace(mapping.SessionPath) == "" {
		return nil
	}
	loaded, err := agent.LoadSession(mapping.SessionPath)
	if err != nil {
		gw.logger.Warn("bot mapped session load failed", "session", shortSessionID(mapping.SessionPath), "err", err)
		return nil
	}
	sessionSink := &sessionEventSink{}
	ctrl, err := gw.buildController(ctx, msg.Platform, sessionSink)
	if err != nil {
		gw.logger.Warn("bot mapped controller build failed", "session", shortSessionID(mapping.SessionPath), "err", err)
		return nil
	}
	ctrl.Resume(loaded, mapping.SessionPath)
	ctrl.EnableInteractiveApproval()
	state := &sessionState{
		ctrl:        ctrl,
		sink:        sessionSink,
		pendingAsks: make(map[string][]event.AskQuestion),
		createdAt:   time.Now(),
		lastActive:  time.Now(),
	}
	gw.mu.Lock()
	gw.controllers[key] = state
	gw.mu.Unlock()
	gw.logger.Info("bot resumed mapped session", "session", shortSessionID(mapping.SessionPath))
	return state
}

func (gw *BotGateway) attachSession(ctx context.Context, key string, msg InboundMessage, ref string) error {
	path, err := gw.resolveSessionRef(ref)
	if err != nil {
		return err
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		return err
	}
	sessionSink := &sessionEventSink{}
	ctrl, err := gw.buildController(ctx, msg.Platform, sessionSink)
	if err != nil {
		return err
	}
	ctrl.Resume(loaded, path)
	ctrl.EnableInteractiveApproval()
	gw.mu.Lock()
	if existing, ok := gw.controllers[key]; ok {
		if existing.ctrl != nil && existing.ctrl.Running() {
			gw.mu.Unlock()
			ctrl.Close()
			return fmt.Errorf("当前会话正在运行，无法绑定")
		}
		if existing.cancel != nil {
			existing.cancel()
		}
		if existing.ctrl != nil {
			existing.ctrl.Close()
		}
	}
	gw.controllers[key] = &sessionState{
		ctrl:        ctrl,
		sink:        sessionSink,
		pendingAsks: make(map[string][]event.AskQuestion),
		createdAt:   time.Now(),
		lastActive:  time.Now(),
	}
	gw.mu.Unlock()
	if err := gw.setSessionMapping(key, path, gw.workspaceRootForPlatform(msg.Platform)); err != nil {
		return err
	}
	return nil
}

func (gw *BotGateway) sessionOptionsForPlatform(plat Platform) (model string, workspaceRoot string) {
	model = gw.cfg.Model
	workspaceRoot = gw.cfg.WorkspaceRoot
	if gw.cfg.Channels == nil {
		return model, workspaceRoot
	}
	channel, ok := gw.cfg.Channels[plat]
	if !ok {
		return model, workspaceRoot
	}
	if value := strings.TrimSpace(channel.Model); value != "" {
		model = value
	}
	if value := strings.TrimSpace(channel.WorkspaceRoot); value != "" {
		workspaceRoot = value
	}
	return model, workspaceRoot
}

func (gw *BotGateway) workspaceRootForPlatform(plat Platform) string {
	_, workspaceRoot := gw.sessionOptionsForPlatform(plat)
	return workspaceRoot
}

func (gw *BotGateway) ensureControllerSessionMapping(key string, plat Platform, ctrl *control.Controller) {
	if ctrl == nil {
		return
	}
	path := strings.TrimSpace(ctrl.SessionPath())
	if path == "" {
		dir := strings.TrimSpace(ctrl.SessionDir())
		if dir == "" {
			return
		}
		path = agent.NewSessionPath(dir, ctrl.Label())
		ctrl.SetSessionPath(path)
	}
	if err := gw.setSessionMapping(key, path, gw.workspaceRootForPlatform(plat)); err != nil {
		gw.logger.Warn("bot session mapping save failed", "err", err, "session", shortSessionID(path))
	}
}

func (gw *BotGateway) setSessionMapping(key, path, workspaceRoot string) error {
	key = strings.TrimSpace(key)
	path = strings.TrimSpace(path)
	if key == "" || path == "" {
		return nil
	}
	gw.mu.Lock()
	gw.mappings[key] = SessionMapping{
		RemoteKey:     key,
		SessionPath:   path,
		SessionID:     shortSessionID(path),
		WorkspaceRoot: workspaceRoot,
		UpdatedAt:     time.Now().UTC(),
	}
	gw.mu.Unlock()
	return gw.saveSessionMappings()
}

func (gw *BotGateway) loadSessionMappings() {
	for _, mapping := range gw.cfg.SessionMappings {
		if mapping.RemoteKey == "" || mapping.SessionPath == "" {
			continue
		}
		gw.mappings[mapping.RemoteKey] = mapping
	}
	path := strings.TrimSpace(gw.cfg.SessionMappingPath)
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			gw.logger.Warn("bot session mappings load failed", "err", err)
		}
		return
	}
	var file sessionMappingFile
	if err := json.Unmarshal(b, &file); err != nil {
		gw.logger.Warn("bot session mappings decode failed", "err", err)
		return
	}
	for _, mapping := range file.Mappings {
		if mapping.RemoteKey == "" || mapping.SessionPath == "" {
			continue
		}
		gw.mappings[mapping.RemoteKey] = mapping
	}
}

func (gw *BotGateway) saveSessionMappings() error {
	path := strings.TrimSpace(gw.cfg.SessionMappingPath)
	if path == "" {
		return nil
	}
	gw.mu.Lock()
	mappings := make([]SessionMapping, 0, len(gw.mappings))
	for _, mapping := range gw.mappings {
		if strings.TrimSpace(mapping.RemoteKey) == "" || strings.TrimSpace(mapping.SessionPath) == "" {
			continue
		}
		mappings = append(mappings, mapping)
	}
	gw.mu.Unlock()
	data, err := json.MarshalIndent(sessionMappingFile{Version: 1, Mappings: mappings}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bot-session-mappings.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return fileutil.ReplaceFile(tmpPath, path)
}

func (gw *BotGateway) sessionMapping(key string) (SessionMapping, bool) {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	mapping, ok := gw.mappings[key]
	return mapping, ok
}

func (gw *BotGateway) clearSessionMapping(key string) bool {
	gw.mu.Lock()
	_, ok := gw.mappings[key]
	if ok {
		delete(gw.mappings, key)
	}
	gw.mu.Unlock()
	if ok {
		if err := gw.saveSessionMappings(); err != nil {
			gw.logger.Warn("bot session mapping save failed", "err", err)
		}
	}
	return ok
}

func (gw *BotGateway) renderSessionList(limit int) string {
	sessions := gw.availableSessions()
	if len(sessions) == 0 {
		return "没有可绑定的 Reasonix session。"
	}
	if limit <= 0 || limit > len(sessions) {
		limit = len(sessions)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "可绑定会话（使用 /attach <id>）：")
	for _, session := range sessions[:limit] {
		id := shortSessionID(session.Path)
		preview := strings.TrimSpace(session.Preview)
		if preview == "" {
			preview = "(empty)"
		}
		fmt.Fprintf(&b, "\n%s  %s", id, truncateBotText(preview, 48))
	}
	if omitted := len(sessions) - limit; omitted > 0 {
		fmt.Fprintf(&b, "\n... 还有 %d 个", omitted)
	}
	return b.String()
}

func (gw *BotGateway) resolveSessionRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("session id required")
	}
	if filepath.IsAbs(ref) || strings.Contains(ref, string(filepath.Separator)) {
		path := ref
		if !filepath.IsAbs(path) {
			path = filepath.Clean(path)
		}
		if _, err := os.Stat(path); err != nil {
			return "", err
		}
		return path, nil
	}
	var matches []agent.SessionInfo
	for _, session := range gw.availableSessions() {
		id := shortSessionID(session.Path)
		base := strings.TrimSuffix(filepath.Base(session.Path), filepath.Ext(session.Path))
		if strings.HasPrefix(id, ref) || strings.HasPrefix(base, ref) {
			matches = append(matches, session)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("session %q not found", ref)
	case 1:
		return matches[0].Path, nil
	default:
		return "", fmt.Errorf("session %q is ambiguous", ref)
	}
}

func (gw *BotGateway) availableSessions() []agent.SessionInfo {
	var out []agent.SessionInfo
	seen := make(map[string]struct{})
	for _, dir := range gw.sessionSearchDirs() {
		sessions, err := agent.ListSessions(dir)
		if err != nil {
			gw.logger.Warn("bot list sessions failed", "dir", dir, "err", err)
			continue
		}
		for _, session := range sessions {
			if _, ok := seen[session.Path]; ok {
				continue
			}
			seen[session.Path] = struct{}{}
			out = append(out, session)
		}
	}
	return out
}

func (gw *BotGateway) sessionSearchDirs() []string {
	var dirs []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		for _, existing := range dirs {
			if existing == clean {
				return
			}
		}
		dirs = append(dirs, clean)
	}
	add(gw.cfg.SessionDir)
	for _, dir := range gw.cfg.SessionSearchDirs {
		add(dir)
	}
	return dirs
}

func shortSessionID(path string) string {
	id := agent.BranchID(path)
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func truncateBotText(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}

func (gw *BotGateway) sendText(ctx context.Context, adapter Adapter, msg InboundMessage, text string) error {
	_, err := adapter.Send(ctx, OutboundMessage{
		ChatID:       msg.ChatID,
		ChatType:     msg.ChatType,
		Text:         text,
		ReplyToMsgID: msg.MessageID,
	})
	return err
}

// handleGoalCommand dispatches /goal subcommands from the bot.
func (gw *BotGateway) handleGoalCommand(ctx context.Context, adapter Adapter, key string, msg InboundMessage) {
	args := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/goal"))

	gw.mu.Lock()
	state, ok := gw.controllers[key]
	gw.mu.Unlock()

	switch strings.ToLower(args) {
	case "", "status":
		if !ok || state.ctrl == nil {
			_ = gw.sendText(ctx, adapter, msg, "目标：无（没有活跃会话）")
			return
		}
		goal := state.ctrl.Goal()
		goalStatus := state.ctrl.GoalStatus()
		if goal == "" {
			_ = gw.sendText(ctx, adapter, msg, fmt.Sprintf("目标：无\n状态：%s", goalStatus))
		} else {
			_ = gw.sendText(ctx, adapter, msg, fmt.Sprintf("目标：%s\n状态：%s", goal, goalStatus))
		}

	case "clear", "off", "stop", "done":
		if !ok || state.ctrl == nil {
			_ = gw.sendText(ctx, adapter, msg, "没有活跃会话。")
			return
		}
		state.ctrl.ClearGoal()
		_ = gw.sendText(ctx, adapter, msg, "目标已清除。")

	case "continue", "resume":
		if !ok || state.ctrl == nil {
			_ = gw.sendText(ctx, adapter, msg, "没有活跃会话。")
			return
		}
		if state.ctrl.Running() {
			_ = gw.sendText(ctx, adapter, msg, "当前正在运行，无法重复启动。")
			return
		}
		goal := state.ctrl.Goal()
		goalStatus := state.ctrl.GoalStatus()
		if goal == "" && (goalStatus == "" || goalStatus == control.GoalStatusStopped) {
			_ = gw.sendText(ctx, adapter, msg, "没有活跃目标可以继续。")
			return
		}
		if goalStatus == control.GoalStatusComplete {
			_ = gw.sendText(ctx, adapter, msg, "目标已完成，请设置新目标。")
			return
		}
		if !gw.sessions.TryStart(key) {
			_ = gw.sendText(ctx, adapter, msg, "当前正在运行，无法重复启动。")
			return
		}
		_ = gw.sendText(ctx, adapter, msg, "继续执行目标…")
		go func() {
			defer func() {
				next := gw.sessions.Release(key)
				if next != nil {
					gw.runTurn(ctx, adapter, key, *next)
				}
			}()

			sink := newRenderSink(ctx, adapter, msg.ChatID, msg.ChatType, msg.MessageID, gw.logger, func(ask event.Ask) {
				gw.mu.Lock()
				if state.pendingAsks == nil {
					state.pendingAsks = make(map[string][]event.AskQuestion)
				}
				state.pendingAsks[ask.ID] = ask.Questions
				gw.mu.Unlock()
			})
			state.sink.setTarget(sink)
			defer state.sink.setTarget(nil)

			turnCtx, cancel := context.WithCancel(ctx)
			gw.mu.Lock()
			if s, ok2 := gw.controllers[key]; ok2 {
				s.cancel = cancel
				s.lastActive = time.Now()
			}
			gw.mu.Unlock()
			defer cancel()
			sink.ctrl = state.ctrl
			err := state.ctrl.ContinueGoal(turnCtx, "bot")
			sink.Emit(event.Event{Kind: event.TurnDone, Err: err})
			if err != nil {
				gw.logger.Warn("bot goal continue failed", "err", err)
			}
		}()

	default:
		// /goal <text> — set a new goal
		if !ok || state.ctrl == nil {
			_ = gw.sendText(ctx, adapter, msg, "没有活跃会话，请先发送一条消息创建会话。")
			return
		}
		state.ctrl.SetPlanMode(false)
		state.ctrl.SetGoal(args)
		_ = gw.sendText(ctx, adapter, msg, fmt.Sprintf("目标已设置 → %s", args))
	}
}

func parseAskAnswers(questions []event.AskQuestion, raw string) []event.AskAnswer {
	raw = strings.TrimSpace(raw)
	if len(questions) == 0 {
		return []event.AskAnswer{{Selected: []string{raw}}}
	}
	byID := make(map[string]*event.AskQuestion, len(questions))
	for i := range questions {
		q := &questions[i]
		byID[q.ID] = q
		byID[fmt.Sprintf("%d", i+1)] = q
	}
	answerMap := make(map[string][]string, len(questions))
	if strings.Contains(raw, "=") {
		for _, part := range strings.Split(raw, ";") {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			q := byID[strings.TrimSpace(k)]
			if q == nil {
				continue
			}
			answerMap[q.ID] = normalizeAskSelection(*q, strings.TrimSpace(v))
		}
	} else if len(questions) == 1 {
		answerMap[questions[0].ID] = normalizeAskSelection(questions[0], raw)
	}
	out := make([]event.AskAnswer, 0, len(questions))
	for _, q := range questions {
		out = append(out, event.AskAnswer{QuestionID: q.ID, Selected: answerMap[q.ID]})
	}
	return out
}

func normalizeAskSelection(q event.AskQuestion, raw string) []string {
	parts := []string{raw}
	if q.Multi && strings.Contains(raw, ",") {
		parts = strings.Split(raw, ",")
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx, err := strconv.Atoi(part); err == nil && idx >= 1 && idx <= len(q.Options) {
			out = append(out, q.Options[idx-1].Label)
			continue
		}
		out = append(out, part)
	}
	return out
}
