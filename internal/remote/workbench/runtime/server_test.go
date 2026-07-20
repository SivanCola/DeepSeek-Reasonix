package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/checkpoint"
	"reasonix/internal/command"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/plugin"
	"reasonix/internal/provider"
	remotebroker "reasonix/internal/remote/broker"
	"reasonix/internal/remote/protocol"
	"reasonix/internal/rpcwire"
	"reasonix/internal/skill"
)

type fakeController struct {
	model   string
	history []provider.Message
}

type projectionController struct {
	*fakeController
	checkpoints []checkpoint.Meta
	todos       []evidence.TodoItem
	goal        string
	goalStatus  string
	rewoundTurn int
	rewound     control.RewindScope
}

type catalogController struct {
	*fakeController
	commands     []command.Command
	skills       []skill.Skill
	disabled     []skill.Skill
	configured   []string
	disconnected []string
}

func (*catalogController) Host() *plugin.Host { return nil }
func (c *catalogController) Commands() []command.Command {
	return append([]command.Command(nil), c.commands...)
}
func (c *catalogController) Skills() []skill.Skill {
	return append([]skill.Skill(nil), c.skills...)
}
func (c *catalogController) SlashSkills() []skill.Skill {
	return append([]skill.Skill(nil), c.skills...)
}
func (c *catalogController) DisabledSkills() []skill.Skill {
	return append([]skill.Skill(nil), c.disabled...)
}
func (c *catalogController) ConfiguredMCPNames() []string {
	return append([]string(nil), c.configured...)
}
func (c *catalogController) DisconnectedMCPNames() []string {
	return append([]string(nil), c.disconnected...)
}

func (c *projectionController) Checkpoints() []checkpoint.Meta {
	return append([]checkpoint.Meta(nil), c.checkpoints...)
}
func (c *projectionController) CheckpointHasBoundary(turn int) bool {
	return turn == 1
}
func (c *projectionController) Todos() []evidence.TodoItem {
	return append([]evidence.TodoItem(nil), c.todos...)
}
func (c *projectionController) SetGoal(goal string) {
	c.goal = strings.TrimSpace(goal)
	if c.goal == "" {
		c.goalStatus = ""
	} else {
		c.goalStatus = string(protocol.GoalRunning)
	}
}
func (c *projectionController) ResumeGoal() bool {
	if c.goal == "" {
		return false
	}
	c.goalStatus = string(protocol.GoalRunning)
	return true
}
func (c *projectionController) Goal() string       { return c.goal }
func (c *projectionController) GoalStatus() string { return c.goalStatus }
func (c *projectionController) Rewind(turn int, scope control.RewindScope) error {
	c.rewoundTurn, c.rewound = turn, scope
	return nil
}

type brokerStubProvider struct{ requests chan provider.Request }

func (p *brokerStubProvider) Name() string { return "desktop-stub" }
func (p *brokerStubProvider) Stream(ctx context.Context, request provider.Request) (<-chan provider.Chunk, error) {
	p.requests <- request
	chunks := make(chan provider.Chunk, 2)
	chunks <- provider.Chunk{Type: provider.ChunkText, Text: "hello from desktop"}
	chunks <- provider.Chunk{Type: provider.ChunkDone}
	close(chunks)
	return chunks, nil
}

func (p *brokerStubProvider) RequiresToolCallReasoning() bool      { return true }
func (p *brokerStubProvider) WarnOnMissingToolCallReasoning() bool { return false }

func (c *fakeController) ModelRef() string { return c.model }
func (c *fakeController) Label() string    { return c.model }
func (c *fakeController) History() []provider.Message {
	return append([]provider.Message(nil), c.history...)
}
func (c *fakeController) Turn() int     { return len(c.history) }
func (c *fakeController) Running() bool { return false }
func (c *fakeController) Submit(input string) {
	c.history = append(c.history, provider.Message{Role: provider.RoleUser, Content: input})
}
func (c *fakeController) Cancel()               {}
func (c *fakeController) Close()                {}
func (c *fakeController) SessionPath() string   { return "" }
func (c *fakeController) SetSessionPath(string) {}
func (c *fakeController) EnsureSessionPath()    {}
func (c *fakeController) AdoptHistory(h []provider.Message, _ string) {
	c.history = append([]provider.Message(nil), h...)
}

func TestSessionCatalogAndSlashArgsUseHostControllerCapabilities(t *testing.T) {
	ctrl := &catalogController{
		fakeController: &fakeController{model: "local/model"},
		commands: []command.Command{
			{Name: "review", Description: "Review this workspace"},
			{Name: "hidden", Hidden: true},
		},
		skills:     []skill.Skill{{Name: "explore", Description: "Explore the Host", Scope: skill.ScopeProject}},
		configured: []string{"connected", "offline"}, disconnected: []string{"offline"},
	}
	catalog := buildSessionCatalog(context.Background(), ctrl)
	if len(catalog.Commands) != 1 || catalog.Commands[0].Name != "review" {
		t.Fatalf("commands = %+v", catalog.Commands)
	}
	if len(catalog.Skills) != 1 || catalog.Skills[0].Name != "explore" || catalog.Skills[0].Scope != "project" {
		t.Fatalf("skills = %+v", catalog.Skills)
	}
	if len(catalog.MCPServers) != 2 || catalog.MCPServers[0].Available || catalog.MCPServers[1].Available {
		t.Fatalf("MCP servers = %+v", catalog.MCPServers)
	}

	server := New(Options{Workspace: t.TempDir(), Version: "test"})
	target := protocol.RuntimeTarget{WorkspaceID: server.workspaceID, SessionID: "session_catalog"}
	server.sessions[target.SessionID] = &session{id: target.SessionID, ctrl: ctrl, model: ctrl.model, runtimeEpoch: "runtime_catalog"}
	result, err := server.composerSlashArgs(protocol.ComposerSlashArgsParams{
		RuntimeQuery: protocol.RuntimeQuery{ExpectedHostEpoch: server.hostEpoch, Target: target, ExpectedRuntimeEpoch: "runtime_catalog"},
		Input:        "/skill disable ",
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range result.Items {
		found = found || item.Label == "explore"
	}
	if !found {
		t.Fatalf("slash args = %+v", result.Items)
	}
}

func TestHistoryPageCapsAndPaginatesByVisibleUserTurns(t *testing.T) {
	history := []provider.Message{{Role: provider.RoleSystem, Content: "system"}}
	for turn := 0; turn < 250; turn++ {
		history = append(history,
			provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("user-%d", turn)},
			provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("assistant-%d", turn)},
		)
	}
	sess := &session{ctrl: &fakeController{model: "model", history: history}}
	latest := historyPage(sess, "snapshot_test", 0, protocol.HistoryMaxTurns)
	if latest.StartTurn != 50 || latest.EndTurn != 250 || latest.ActualTurns != 200 || !latest.HasOlder || latest.NextCursor != "turn:50" {
		t.Fatalf("latest = %+v", latest)
	}
	if len(latest.Messages) != 400 || latest.Messages[0].Content == nil || *latest.Messages[0].Content != "user-50" {
		t.Fatalf("latest messages = %d first=%+v", len(latest.Messages), latest.Messages[0])
	}
	before, err := historyCursorTurn(latest.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	older := historyPage(sess, "snapshot_test", before, protocol.HistoryMaxTurns)
	if older.StartTurn != 0 || older.EndTurn != 50 || older.ActualTurns != 50 || older.HasOlder || older.NextCursor != "" {
		t.Fatalf("older = %+v", older)
	}
	if len(older.Messages) != 101 || older.Messages[0].Role != "system" {
		t.Fatalf("older messages = %d first=%+v", len(older.Messages), older.Messages[0])
	}
}

func TestRuntimeOperationKeepsDetachedServerBusy(t *testing.T) {
	server := New(Options{Workspace: t.TempDir(), Version: "test"})
	server.sessions["session_operation"] = &session{
		id: "session_operation", ctrl: &fakeController{model: "model"},
		currentOp: &protocol.OperationState{OperationID: "operation_test", Kind: protocol.OperationCompact},
	}
	server.mu.Lock()
	busy := server.hasBusyLocked()
	server.mu.Unlock()
	if !busy {
		t.Fatal("detached runtime considered an active operation idle")
	}
}

func TestRuntimeSessionCreateAndFileList(t *testing.T) {
	ws := t.TempDir()
	// Short absolute socket path — macOS rejects long unix paths.
	sock := filepath.Join(t.TempDir(), "r.sock")
	if len(sock) > 100 {
		sock = filepath.Join("/tmp", "rx-wb-"+t.Name()+".sock")
		t.Cleanup(func() { _ = os.Remove(sock) })
	}
	srv := New(Options{Workspace: ws, Version: "test", BuildController: func(_ context.Context, model string, _ *string, _ event.Sink) (SessionController, error) {
		return &fakeController{model: model}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(ctx, sock) }()

	// Wait for socket.
	var conn net.Conn
	var err error
	for i := 0; i < 50; i++ {
		select {
		case e := <-errCh:
			t.Fatalf("listen failed: %v", e)
		default:
		}
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	wire := rpcwire.NewConn(conn, conn, rpcwire.Options{
		Name: "test", StrictJSONRPC: true,
		MaxInboundBytes: protocol.FrameBytes, MaxOutboundBytes: protocol.FrameBytes,
	})
	go wire.Serve(ctx)

	buildID, err := protocol.NewBuildID("test", strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := wire.Request(ctx, string(protocol.MethodRemoteInitialize), protocol.InitializeParams{BuildID: buildID, ClientInstanceID: "desktop-test", Workspace: ws})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("empty initialize result")
	}

	var initialized protocol.InitializeResult
	if err := json.Unmarshal(raw, &initialized); err != nil {
		t.Fatal(err)
	}
	raw, err = wire.Request(ctx, string(protocol.MethodWorkspaceList), protocol.WorkspaceListParams{ExpectedHostEpoch: initialized.HostEpoch})
	if err != nil {
		t.Fatalf("workspace list: %v", err)
	}
	var workspaces protocol.WorkspaceListResult
	if err := json.Unmarshal(raw, &workspaces); err != nil || len(workspaces.Items) != 1 {
		t.Fatalf("workspace list = %s err=%v", raw, err)
	}
	model := "stub/model"
	raw, err = wire.Request(ctx, string(protocol.MethodSessionCreate), protocol.SessionCreateParams{
		HostMutation: protocol.HostMutation{RequestID: "request-1", ExpectedHostEpoch: initialized.HostEpoch},
		WorkspaceID:  workspaces.Items[0].WorkspaceID, AdditionalDirectoryRefs: []protocol.DirectoryRef{},
		Topic: protocol.TopicSelection{Kind: protocol.TopicNew}, Profile: protocol.ProfileSelection{Model: &model},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created protocol.SessionCreateResult
	if err := json.Unmarshal(raw, &created); err != nil || created.Target.SessionID == "" {
		t.Fatalf("create = %s err=%v", raw, err)
	}

	raw, err = wire.Request(ctx, string(protocol.MethodFileList), protocol.FileListParams{
		RuntimeQuery: protocol.RuntimeQuery{ExpectedHostEpoch: initialized.HostEpoch, Target: created.Target, ExpectedRuntimeEpoch: created.RuntimeEpoch},
		Path:         "",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listed protocol.FileListResult
	_ = json.Unmarshal(raw, &listed)
	if listed.Entries == nil {
		t.Fatalf("list = %s", raw)
	}
	srv.Close()
}

func TestRuntimeGraceDetach(t *testing.T) {
	srv := New(Options{Workspace: t.TempDir(), Version: "t"})
	srv.ForceDetachForTest()
	if srv.Attached() {
		t.Fatal("expected detached")
	}
}

func TestRuntimeWorkspaceGitQueriesAndSnapshotProjection(t *testing.T) {
	workspace := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", workspace}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init")
	runGit("config", "user.email", "remote-test@example.com")
	runGit("config", "user.name", "Remote Test")
	tracked := filepath.Join(workspace, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tracked.txt")
	runGit("commit", "-m", "initial")
	hash := runGit("rev-parse", "HEAD")
	if err := os.WriteFile(tracked, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "untracked.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctrl := &projectionController{
		fakeController: &fakeController{model: "local/stub"},
		checkpoints:    []checkpoint.Meta{{Turn: 1, Time: time.Now(), Prompt: "edit it", Paths: []string{tracked}}},
		todos:          []evidence.TodoItem{{Content: "verify", Status: "in_progress"}},
	}
	srv := New(Options{Workspace: workspace, Version: "test"})
	target := srv.installTestSession(ctrl)
	query := protocol.RuntimeQuery{ExpectedHostEpoch: srv.hostEpoch, Target: target, ExpectedRuntimeEpoch: "runtime_test"}
	mutation := protocol.SessionMutation{RequestID: "request-test", ExpectedHostEpoch: srv.hostEpoch, Target: target, ExpectedRuntimeEpoch: "runtime_test"}

	changes, err := srv.workspaceChanges(protocol.WorkspaceChangesParams{RuntimeQuery: query})
	if err != nil {
		t.Fatal(err)
	}
	if !changes.GitAvailable || changes.GitBranch == "" || len(changes.Files) != 2 {
		t.Fatalf("changes = %+v", changes)
	}
	var trackedChange *protocol.ChangedFile
	for i := range changes.Files {
		if changes.Files[i].Path == "tracked.txt" {
			trackedChange = &changes.Files[i]
		}
	}
	if trackedChange == nil || len(trackedChange.Sources) != 2 || trackedChange.LatestPrompt != "edit it" {
		t.Fatalf("tracked change = %+v", trackedChange)
	}

	history, err := srv.gitHistory(protocol.GitHistoryParams{RuntimeQuery: query})
	if err != nil || len(history.Commits) != 1 || history.Commits[0].Hash != hash {
		t.Fatalf("history = %+v err=%v", history, err)
	}
	if err := history.Validate(); err != nil {
		t.Fatalf("history validation: %v", err)
	}
	patch, err := srv.gitCommitDetail(protocol.GitCommitDetailParams{RuntimeQuery: query, Hash: hash, Path: "tracked.txt"})
	if err != nil || patch.Body == nil || !strings.Contains(*patch.Body, "before") {
		t.Fatalf("patch = %+v err=%v", patch, err)
	}
	files, err := srv.gitCommitDetail(protocol.GitCommitDetailParams{RuntimeQuery: query, Hash: hash})
	if err != nil || files.Files == nil || len(*files.Files) != 1 || (*files.Files)[0].Path != "tracked.txt" {
		t.Fatalf("files = %+v err=%v", files, err)
	}

	srv.mu.Lock()
	snapshot := srv.snapshotLocked(srv.sessions[target.SessionID], 20)
	srv.mu.Unlock()
	if len(snapshot.Checkpoints) != 1 || !snapshot.Checkpoints[0].CanCode || !snapshot.Checkpoints[0].CanConversation {
		t.Fatalf("checkpoints = %+v", snapshot.Checkpoints)
	}
	if len(snapshot.Todos) != 1 || snapshot.Todos[0].Status != protocol.TodoInProgress {
		t.Fatalf("todos = %+v", snapshot.Todos)
	}
	goal, err := srv.setGoal(protocol.SessionGoalSetParams{SessionMutation: mutation, Goal: "ship it"})
	if err != nil || goal.Goal != "ship it" || goal.Status != protocol.GoalRunning {
		t.Fatalf("goal = %+v err=%v", goal, err)
	}
	rewound, err := srv.rewindSession(protocol.SessionRewindParams{SessionMutation: mutation, CheckpointID: "checkpoint_1", Scope: protocol.RewindConversation})
	if err != nil || !rewound.ConversationRewritten || ctrl.rewoundTurn != 1 || ctrl.rewound != control.RewindConversation {
		t.Fatalf("rewind = %+v err=%v controller=(%d,%d)", rewound, err, ctrl.rewoundTurn, ctrl.rewound)
	}
	srv.mu.Lock()
	snapshot = srv.snapshotLocked(srv.sessions[target.SessionID], 20)
	srv.mu.Unlock()
	if snapshot.Meta.Goal == nil || *snapshot.Meta.Goal != "ship it" || snapshot.Meta.GoalStatus != protocol.GoalRunning {
		t.Fatalf("snapshot goal = %+v/%q", snapshot.Meta.Goal, snapshot.Meta.GoalStatus)
	}
}

func TestRuntimeFilePreviewDoesNotDecodeBinary(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "blob.bin"), []byte{'a', 0, 'b'}, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Workspace: workspace, Version: "test"})
	target := srv.installTestSession(&fakeController{model: "local/stub"})
	result, err := srv.filePreview(protocol.FilePreviewParams{
		RuntimeQuery: protocol.RuntimeQuery{ExpectedHostEpoch: srv.hostEpoch, Target: target, ExpectedRuntimeEpoch: "runtime_test"},
		Path:         "blob.bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != protocol.FileBinary || !result.Binary || result.Body != nil || result.ReturnedBytes != 0 {
		t.Fatalf("preview = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
}

func (s *Server) installTestSession(ctrl SessionController) protocol.RuntimeTarget {
	id := protocol.SessionID("session_test")
	s.mu.Lock()
	s.sessions[id] = &session{
		id: id, ctrl: ctrl, model: ctrl.ModelRef(), effort: "high",
		collaboration: protocol.CollaborationNormal, tokenMode: protocol.TokenFull,
		toolApproval: protocol.ToolApprovalAsk, topicID: "topic_test", title: "Test",
		runtimeEpoch: "runtime_test", createdAt: time.Now().UnixMilli(), updatedAt: time.Now().UnixMilli(),
	}
	s.mu.Unlock()
	return s.target(id)
}

func TestRuntimeControllerUsesDesktopBrokerWithoutHostKey(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("REASONIX_HOME", filepath.Join(home, ".reasonix"))
	t.Setenv("REASONIX_SAFE_MODE", "1")
	t.Setenv("DEEPSEEK_API_KEY", "")

	revision := strings.Repeat("b", 40)
	buildID, err := protocol.NewBuildID("test", revision)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Workspace: workspace, Version: "test", SourceRevision: revision})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	hostSide, desktopSide := net.Pipe()
	defer desktopSide.Close()
	go srv.serveConn(ctx, hostSide)

	desktopWire := rpcwire.NewConn(desktopSide, desktopSide, rpcwire.Options{
		Name: "desktop-e2e", StrictJSONRPC: true,
		MaxInboundBytes: protocol.FrameBytes, MaxOutboundBytes: protocol.FrameBytes,
	})
	stub := &brokerStubProvider{requests: make(chan provider.Request, 1)}
	desktopBroker, err := remotebroker.Attach(desktopWire, remotebroker.Options{
		Catalog: func(context.Context, map[string]struct{}) ([]protocol.BrokerProviderDescriptor, error) {
			return []protocol.BrokerProviderDescriptor{
				remotebroker.DescriptorFromProvider("local/stub", "Local stub", "stub", stub, []string{"high"}, "high", false),
			}, nil
		},
		Open: func(ctx context.Context, ref, _ string, request provider.Request) (<-chan provider.Chunk, error) {
			if ref != "local/stub" {
				return nil, fmt.Errorf("unexpected provider ref %q", ref)
			}
			return stub.Stream(ctx, request)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer desktopBroker.Close()
	events := make(chan protocol.SessionEvent, 32)
	desktopWire.HandleNotify(string(protocol.MethodSessionEvent), func(_ context.Context, raw json.RawMessage) {
		decoded, decodeErr := protocol.DecodeNotificationParams(protocol.MethodSessionEvent, raw)
		if decodeErr != nil {
			t.Errorf("session event: %v", decodeErr)
			return
		}
		events <- decoded.(protocol.SessionEvent)
	})
	go desktopWire.Serve(ctx)

	request := func(method protocol.Method, params any) any {
		raw, requestErr := desktopWire.Request(ctx, string(method), params)
		if requestErr != nil {
			t.Fatalf("%s: %v", method, requestErr)
		}
		decoded, decodeErr := protocol.DecodeResult(method, raw)
		if decodeErr != nil {
			t.Fatalf("%s result: %v", method, decodeErr)
		}
		return decoded
	}
	initialized := request(protocol.MethodRemoteInitialize, protocol.InitializeParams{BuildID: buildID, ClientInstanceID: "desktop-e2e", Workspace: workspace}).(protocol.InitializeResult)
	if err := protocol.CompareBuildID(buildID, initialized.BuildID); err != nil {
		t.Fatal(err)
	}
	if err := desktopBroker.Activate(); err != nil {
		t.Fatal(err)
	}
	workspaces := request(protocol.MethodWorkspaceList, protocol.WorkspaceListParams{ExpectedHostEpoch: initialized.HostEpoch}).(protocol.WorkspaceListResult)
	model := "local/stub"
	created := request(protocol.MethodSessionCreate, protocol.SessionCreateParams{
		HostMutation: protocol.HostMutation{RequestID: "request-create", ExpectedHostEpoch: initialized.HostEpoch},
		WorkspaceID:  workspaces.Items[0].WorkspaceID, AdditionalDirectoryRefs: []protocol.DirectoryRef{},
		Topic: protocol.TopicSelection{Kind: protocol.TopicNew}, Profile: protocol.ProfileSelection{Model: &model},
	}).(protocol.SessionCreateResult)
	subscribed := request(protocol.MethodSessionSubscribe, protocol.SessionSubscribeParams{
		ExpectedHostEpoch: initialized.HostEpoch, Target: created.Target, PageTurns: 20,
	}).(protocol.SessionSubscribeResult)
	request(protocol.MethodSessionSubmit, protocol.SessionSubmitParams{
		SessionMutation: protocol.SessionMutation{
			RequestID: "request-submit", ExpectedHostEpoch: initialized.HostEpoch,
			Target: created.Target, ExpectedRuntimeEpoch: created.RuntimeEpoch,
		},
		Input: "say hello", DisplayText: "say hello",
	})

	select {
	case providerRequest := <-stub.requests:
		if len(providerRequest.Messages) == 0 || providerRequest.Messages[len(providerRequest.Messages)-1].Content != "say hello" {
			t.Fatalf("broker provider request lost user prompt: %+v", providerRequest.Messages)
		}
	case <-ctx.Done():
		t.Fatal("Desktop provider was not called")
	}
	seenText, seenDone := false, false
	for !seenDone {
		select {
		case event := <-events:
			if event.SubscriptionID != subscribed.SubscriptionID {
				t.Fatalf("event subscription = %q", event.SubscriptionID)
			}
			seenText = seenText || (event.Event.Kind == "text" && strings.Contains(event.Event.Text, "hello from desktop"))
			seenDone = event.Event.Kind == "turn_done"
		case <-ctx.Done():
			t.Fatal("timed out waiting for Broker-backed turn events")
		}
	}
	if !seenText {
		t.Fatal("Broker-backed text event was not delivered")
	}
}
