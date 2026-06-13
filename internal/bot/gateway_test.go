package bot

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/provider"
)

// fakeAdapter 是一个内存中的假适配器，用于测试 BotGateway。
type fakeAdapter struct {
	mu       sync.Mutex
	platform Platform
	name     string
	msgCh    chan InboundMessage
	sent     []OutboundMessage
	started  bool
}

func newFakeAdapter(platform Platform, name string) *fakeAdapter {
	return &fakeAdapter{
		platform: platform,
		name:     name,
		msgCh:    make(chan InboundMessage, 16),
	}
}

func (f *fakeAdapter) Platform() Platform              { return f.platform }
func (f *fakeAdapter) Name() string                    { return f.name }
func (f *fakeAdapter) Messages() <-chan InboundMessage { return f.msgCh }

func (f *fakeAdapter) Start(ctx context.Context) error {
	f.mu.Lock()
	f.started = true
	f.mu.Unlock()
	return nil
}

func (f *fakeAdapter) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.msgCh != nil {
		close(f.msgCh)
		f.msgCh = nil
	}
	return nil
}

func (f *fakeAdapter) Send(ctx context.Context, msg OutboundMessage) (SendResult, error) {
	f.mu.Lock()
	f.sent = append(f.sent, msg)
	f.mu.Unlock()
	return SendResult{MessageID: "fake_msg_1"}, nil
}

func (f *fakeAdapter) SendTyping(ctx context.Context, chatID string) error { return nil }

func (f *fakeAdapter) sentMessages() []OutboundMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]OutboundMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

type fakeReactionAdapter struct {
	*fakeAdapter
	reactions []string
}

func (f *fakeReactionAdapter) AddPendingReaction(ctx context.Context, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactions = append(f.reactions, messageID)
	return nil
}

func TestFakeAdapterInterface(t *testing.T) {
	fa := newFakeAdapter(PlatformQQ, "fake-qq")

	if fa.Platform() != PlatformQQ {
		t.Error("wrong platform")
	}
	if fa.Name() != "fake-qq" {
		t.Error("wrong name")
	}

	ctx := context.Background()
	if err := fa.Start(ctx); err != nil {
		t.Fatal("start:", err)
	}
	if !fa.started {
		t.Error("should be started")
	}

	_, err := fa.Send(ctx, OutboundMessage{ChatID: "c1", Text: "hello"})
	if err != nil {
		t.Fatal("send:", err)
	}

	sent := fa.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent count = %d, want 1", len(sent))
	}
	if sent[0].Text != "hello" {
		t.Errorf("sent text = %q, want %q", sent[0].Text, "hello")
	}

	if err := fa.Stop(); err != nil {
		t.Fatal("stop:", err)
	}
}

func TestGatewayConstructAndStop(t *testing.T) {
	cfg := GatewayConfig{
		Model:         "test",
		MaxSteps:      10,
		WorkspaceRoot: ".",
		Enabled:       map[Platform]bool{PlatformQQ: true},
		Allowlist:     AllowlistConfig{Enabled: false},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(cfg, map[Platform]Adapter{
		PlatformQQ: newFakeAdapter(PlatformQQ, "fake-qq"),
	}, logger)

	// 网关不应该 panic
	if gw == nil {
		t.Fatal("gateway should not be nil")
	}
	gw.Stop()
}

func TestGatewayAllowlistCheck(t *testing.T) {
	cfg := GatewayConfig{
		Allowlist: AllowlistConfig{
			Enabled: true,
			Users: map[Platform][]string{
				PlatformQQ: {"allowed_user_1"},
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(cfg, nil, logger)

	if !gw.checkAllowlist(PlatformQQ, InboundMessage{Platform: PlatformQQ, ChatType: ChatDM, UserID: "allowed_user_1"}) {
		t.Error("allowed user should pass")
	}
	if gw.checkAllowlist(PlatformQQ, InboundMessage{Platform: PlatformQQ, ChatType: ChatDM, UserID: "unknown_user"}) {
		t.Error("unknown user should not pass")
	}
	// 不同平台
	if gw.checkAllowlist(PlatformFeishu, InboundMessage{Platform: PlatformFeishu, ChatType: ChatDM, UserID: "allowed_user_1"}) {
		t.Error("QQ allowlist should not apply to feishu")
	}
}

func TestGatewayAllowlistDoesNotApplyGroupsToDirectMessages(t *testing.T) {
	cfg := GatewayConfig{
		Allowlist: AllowlistConfig{
			Enabled: true,
			Users: map[Platform][]string{
				PlatformQQ: {"allowed_user"},
			},
			Groups: map[Platform][]string{
				PlatformQQ: {"allowed_group"},
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(cfg, nil, logger)

	if !gw.checkAllowlist(PlatformQQ, InboundMessage{Platform: PlatformQQ, ChatType: ChatDirect, ChatID: "guild-dm", UserID: "allowed_user"}) {
		t.Error("direct message should not be rejected by group allowlist")
	}
	if gw.checkAllowlist(PlatformQQ, InboundMessage{Platform: PlatformQQ, ChatType: ChatGroup, ChatID: "unknown_group", UserID: "allowed_user"}) {
		t.Error("unknown group should still be rejected by group allowlist")
	}
}

func TestGatewayAllowlistDisabledRejectsByDefault(t *testing.T) {
	cfg := GatewayConfig{
		Allowlist: AllowlistConfig{Enabled: false},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(cfg, nil, logger)

	if gw.checkAllowlist(PlatformQQ, InboundMessage{Platform: PlatformQQ, ChatType: ChatDM, UserID: "any_user"}) {
		t.Error("disabled allowlist should reject unless allow_all is explicit")
	}
}

func TestGatewayAllowAll(t *testing.T) {
	cfg := GatewayConfig{
		Allowlist: AllowlistConfig{AllowAll: true},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(cfg, nil, logger)

	if !gw.checkAllowlist(PlatformQQ, InboundMessage{Platform: PlatformQQ, ChatType: ChatDM, UserID: "any_user"}) {
		t.Error("allow_all should allow everyone")
	}
}

func TestGatewayAddsPendingReactionWhenAdapterSupportsIt(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(GatewayConfig{}, nil, logger)
	fa := &fakeReactionAdapter{fakeAdapter: newFakeAdapter(PlatformFeishu, "fake-feishu")}

	gw.addPendingReaction(context.Background(), PlatformFeishu, fa, InboundMessage{MessageID: "om_123"})

	if len(fa.reactions) != 1 || fa.reactions[0] != "om_123" {
		t.Fatalf("reactions = %#v, want [om_123]", fa.reactions)
	}
}

func TestGatewaySessionOptionsUseChannelOverride(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(GatewayConfig{
		Model:         "global-model",
		WorkspaceRoot: "/global",
		Channels: map[Platform]ChannelConfig{
			PlatformFeishu: {Model: "feishu-model", WorkspaceRoot: "/feishu"},
			PlatformWeixin: {WorkspaceRoot: "/weixin"},
		},
	}, nil, logger)

	model, root := gw.sessionOptionsForPlatform(PlatformFeishu)
	if model != "feishu-model" || root != "/feishu" {
		t.Fatalf("feishu options = %q,%q; want channel override", model, root)
	}

	model, root = gw.sessionOptionsForPlatform(PlatformWeixin)
	if model != "global-model" || root != "/weixin" {
		t.Fatalf("weixin options = %q,%q; want global model and channel root", model, root)
	}

	model, root = gw.sessionOptionsForPlatform(PlatformQQ)
	if model != "global-model" || root != "/global" {
		t.Fatalf("qq options = %q,%q; want global defaults", model, root)
	}
}

func TestGatewayRenderSessionListAndResolveRef(t *testing.T) {
	dir := t.TempDir()
	sessionPath := writeBotTestSession(t, dir, "hello from saved session")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(GatewayConfig{SessionDir: dir}, nil, logger)

	list := gw.renderSessionList(5)
	id := shortSessionID(sessionPath)
	if !strings.Contains(list, id) || !strings.Contains(list, "hello from saved session") {
		t.Fatalf("session list missing saved session: %s", list)
	}

	resolved, err := gw.resolveSessionRef(id[:6])
	if err != nil {
		t.Fatalf("resolveSessionRef: %v", err)
	}
	if resolved != sessionPath {
		t.Fatalf("resolved = %q, want %q", resolved, sessionPath)
	}
}

func TestGatewaySessionMappingPersists(t *testing.T) {
	dir := t.TempDir()
	mappingPath := filepath.Join(dir, "mappings.json")
	sessionPath := filepath.Join(dir, "saved.jsonl")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(GatewayConfig{SessionMappingPath: mappingPath}, nil, logger)
	gw.mappings["remote-1"] = SessionMapping{
		RemoteKey:   "remote-1",
		SessionPath: sessionPath,
		SessionID:   "saved",
	}
	if err := gw.saveSessionMappings(); err != nil {
		t.Fatalf("saveSessionMappings: %v", err)
	}

	gw2 := NewGateway(GatewayConfig{SessionMappingPath: mappingPath}, nil, logger)
	mapping, ok := gw2.sessionMapping("remote-1")
	if !ok || mapping.SessionPath != sessionPath || mapping.SessionID != "saved" {
		t.Fatalf("mapping not reloaded: ok=%v mapping=%+v", ok, mapping)
	}
	if !gw2.clearSessionMapping("remote-1") {
		t.Fatal("clearSessionMapping should return true")
	}
	gw3 := NewGateway(GatewayConfig{SessionMappingPath: mappingPath}, nil, logger)
	if _, ok := gw3.sessionMapping("remote-1"); ok {
		t.Fatal("mapping should be removed after clear")
	}
}

func TestGatewayEnsuresMappingForNewController(t *testing.T) {
	dir := t.TempDir()
	mappingPath := filepath.Join(dir, "mappings.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(GatewayConfig{
		SessionDir:         dir,
		SessionMappingPath: mappingPath,
		WorkspaceRoot:      "/workspace",
	}, nil, logger)
	ctrl := control.New(control.Options{SessionDir: dir, Label: "model/test"})

	gw.ensureControllerSessionMapping("remote-new", PlatformQQ, ctrl)

	if ctrl.SessionPath() == "" {
		t.Fatal("controller should receive a session path")
	}
	mapping, ok := gw.sessionMapping("remote-new")
	if !ok {
		t.Fatal("mapping should be recorded")
	}
	if mapping.SessionPath != ctrl.SessionPath() || mapping.WorkspaceRoot != "/workspace" {
		t.Fatalf("unexpected mapping: %+v ctrlPath=%q", mapping, ctrl.SessionPath())
	}

	gw2 := NewGateway(GatewayConfig{SessionMappingPath: mappingPath}, nil, logger)
	reloaded, ok := gw2.sessionMapping("remote-new")
	if !ok || reloaded.SessionPath != ctrl.SessionPath() {
		t.Fatalf("mapping not persisted: ok=%v mapping=%+v", ok, reloaded)
	}
}

func TestGatewayEnsuresMappingPreservesExistingControllerPath(t *testing.T) {
	dir := t.TempDir()
	existingPath := filepath.Join(dir, "existing.jsonl")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(GatewayConfig{SessionDir: dir}, nil, logger)
	ctrl := control.New(control.Options{SessionDir: dir, Label: "model/test"})
	ctrl.SetSessionPath(existingPath)

	gw.ensureControllerSessionMapping("remote-existing", PlatformQQ, ctrl)

	mapping, ok := gw.sessionMapping("remote-existing")
	if !ok || mapping.SessionPath != existingPath || ctrl.SessionPath() != existingPath {
		t.Fatalf("existing path should be preserved: ok=%v mapping=%+v ctrlPath=%q", ok, mapping, ctrl.SessionPath())
	}
}

func TestGatewayStatusIncludesRuntimeDetails(t *testing.T) {
	dir := t.TempDir()
	sessionPath := writeBotTestSession(t, dir, "hello")
	now := time.Date(2026, 6, 13, 9, 30, 0, 0, time.UTC)
	if err := agent.SaveRuntimeMeta(sessionPath, agent.RuntimeMeta{
		Goal: agent.RuntimeGoalMeta{
			Text:   "ship the daemon status panel",
			Status: control.GoalStatusRunning,
		},
		Run: agent.RuntimeRunMeta{Status: "waiting_event"},
		Wait: agent.RuntimeWaitMeta{
			Kind:            "event",
			Reason:          "waiting for CI",
			EventID:         "run-123",
			EventSource:     "github.workflow_run",
			EventStatus:     "completed",
			EventConclusion: "success",
			Subject:         "PR #42",
			Since:           now.Add(-10 * time.Minute),
		},
		Scheduler: agent.RuntimeSchedMeta{
			Enabled:          true,
			DailyAt:          "09:00",
			Interval:         time.Hour,
			LastWakeupAt:     now,
			LastWakeupReason: "webhook",
			NextWakeupAt:     now.Add(time.Hour),
		},
		Budget: agent.RuntimeBudgetMeta{
			DailyWakeupLimit: 5,
			DailyWakeups:     2,
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(GatewayConfig{SessionDir: dir}, nil, logger)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: sessionPath, Label: "test"})
	ctrl.SetGoal("ship the daemon status panel")
	gw.controllers["remote-status"] = &sessionState{ctrl: ctrl, createdAt: now, lastActive: now}
	fa := newFakeAdapter(PlatformQQ, "fake-qq")

	gw.handleSlashCommand(context.Background(), fa, "remote-status", InboundMessage{
		Platform:  PlatformQQ,
		ChatType:  ChatDM,
		ChatID:    "chat-1",
		UserID:    "user-1",
		Text:      "/status",
		MessageID: "msg-1",
	})

	sent := fa.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent count = %d, want 1", len(sent))
	}
	text := sent[0].Text
	for _, want := range []string{
		"会话: " + shortSessionID(sessionPath),
		"目标: ship the daemon status panel",
		"运行状态: waiting_event",
		"等待: event run-123 (PR #42)",
		"等待原因: waiting for CI",
		"事件条件: source=github.workflow_run status=completed conclusion=success",
		"调度: enabled daily=09:00 interval=1h0m0s",
		"上次唤醒:",
		"(webhook)",
		"下次唤醒:",
		"唤醒预算: 2/5",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("status missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, sessionPath) {
		t.Fatalf("status should not expose full session path:\n%s", text)
	}
}

func TestGatewayStatusUsesMappingWithoutActiveController(t *testing.T) {
	dir := t.TempDir()
	sessionPath := writeBotTestSession(t, dir, "hello")
	if err := agent.SaveRuntimeMeta(sessionPath, agent.RuntimeMeta{
		Goal: agent.RuntimeGoalMeta{
			Text:   "resume mapped work",
			Status: control.GoalStatusRunning,
		},
		Run: agent.RuntimeRunMeta{Status: "idle"},
		Wait: agent.RuntimeWaitMeta{
			Kind:       "time",
			Reason:     "nap before retry",
			Until:      time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC),
			FilePaths:  nil,
			ApprovalID: "",
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(GatewayConfig{SessionDir: dir}, nil, logger)
	if err := gw.setSessionMapping("remote-mapped", sessionPath, "/workspace"); err != nil {
		t.Fatalf("setSessionMapping: %v", err)
	}
	fa := newFakeAdapter(PlatformQQ, "fake-qq")

	gw.handleSlashCommand(context.Background(), fa, "remote-mapped", InboundMessage{
		Platform:  PlatformQQ,
		ChatType:  ChatDM,
		ChatID:    "chat-1",
		UserID:    "user-1",
		Text:      "/status",
		MessageID: "msg-1",
	})

	sent := fa.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent count = %d, want 1", len(sent))
	}
	text := sent[0].Text
	for _, want := range []string{
		"会话: " + shortSessionID(sessionPath),
		"目标: resume mapped work",
		"目标状态: running",
		"运行状态: idle",
		"等待: time",
		"等待原因: nap before retry",
		"等待到:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("status missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, sessionPath) {
		t.Fatalf("status should not expose full session path:\n%s", text)
	}
}

func TestGatewayRecordRuntimeWaitPersistsApprovalAndAsk(t *testing.T) {
	dir := t.TempDir()
	sessionPath := writeBotTestSession(t, dir, "hello")
	if err := agent.SaveRuntimeMeta(sessionPath, agent.RuntimeMeta{
		Goal:   agent.RuntimeGoalMeta{Text: "needs a decision", Status: control.GoalStatusRunning},
		Budget: agent.RuntimeBudgetMeta{DailyWakeupLimit: 3, DailyWakeups: 1},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(GatewayConfig{SessionDir: dir}, nil, logger)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: sessionPath, Label: "test"})
	ctrl.SetGoal("needs a decision")
	gw.controllers["remote-wait"] = &sessionState{ctrl: ctrl}

	gw.recordRuntimeWait("remote-wait", agent.RuntimeWaitMeta{
		Kind:       "approval",
		Reason:     "approval required",
		ApprovalID: "approval-1",
		Tool:       "bash",
		Subject:    "git push",
	})

	loaded, ok, err := agent.LoadRuntimeMeta(sessionPath)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta after approval wait: err=%v ok=%v", err, ok)
	}
	if loaded.Wait.Kind != "approval" || loaded.Wait.ApprovalID != "approval-1" ||
		loaded.Wait.Tool != "bash" || loaded.Wait.Subject != "git push" ||
		loaded.Run.Status != "waiting_approval" {
		t.Fatalf("approval wait not persisted: %+v", loaded)
	}
	if loaded.Budget.DailyWakeupLimit != 3 || loaded.Budget.DailyWakeups != 1 {
		t.Fatalf("budget should be preserved: %+v", loaded.Budget)
	}

	gw.recordRuntimeWait("remote-wait", agent.RuntimeWaitMeta{
		Kind:    "ask",
		Reason:  "user answer required",
		AskID:   "ask-1",
		Subject: "Which release channel?",
	})

	loaded, ok, err = agent.LoadRuntimeMeta(sessionPath)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta after ask wait: err=%v ok=%v", err, ok)
	}
	if loaded.Wait.Kind != "ask" || loaded.Wait.AskID != "ask-1" ||
		loaded.Wait.Subject != "Which release channel?" || loaded.Run.Status != "waiting_ask" {
		t.Fatalf("ask wait not persisted: %+v", loaded)
	}
}

func TestGatewayRecordsAndClearsRuntimeWait(t *testing.T) {
	dir := t.TempDir()
	sessionPath := writeBotTestSession(t, dir, "hello")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := NewGateway(GatewayConfig{SessionDir: dir}, nil, logger)
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: sessionPath, Label: "test"})
	gw.controllers["remote-wait"] = &sessionState{ctrl: ctrl}

	gw.recordRuntimeWait("remote-wait", agent.RuntimeWaitMeta{
		Kind:       "approval",
		Reason:     "approval required",
		ApprovalID: "approval-1",
		Tool:       "shell",
		Subject:    "go test ./...",
	})

	meta, ok, err := agent.LoadRuntimeMeta(sessionPath)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta after record: ok=%v err=%v", ok, err)
	}
	if meta.Run.Status != "waiting_approval" || meta.Wait.ApprovalID != "approval-1" || meta.Wait.Tool != "shell" {
		t.Fatalf("wait not recorded: %+v", meta)
	}

	gw.clearRuntimeWait("remote-wait", "approval", "approval-1")

	meta, ok, err = agent.LoadRuntimeMeta(sessionPath)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta after clear: ok=%v err=%v", ok, err)
	}
	if meta.Wait.Kind != "" || meta.Run.Status != "running" {
		t.Fatalf("wait not cleared: %+v", meta)
	}
	events, ok, err := agent.LoadRuntimeTimeline(sessionPath, 2)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeTimeline: ok=%v err=%v", ok, err)
	}
	if len(events) != 2 || events[0].Type != "wait_started" || events[1].Type != "wait_cleared" {
		t.Fatalf("unexpected timeline: %+v", events)
	}
}

func writeBotTestSession(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sessionPath := filepath.Join(dir, "saved-session.jsonl")
	sess := agent.NewSession("")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: content})
	if err := sess.Save(sessionPath); err != nil {
		t.Fatalf("Save session: %v", err)
	}
	return sessionPath
}
