package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// TestTaskToolReturnsSubAgentFinalAnswer runs a task against a mock provider
// that emits a single text turn, and verifies the tool returns exactly that
// text — sub-agent intermediate state isn't supposed to leak.
func TestTaskToolReturnsSubAgentFinalAnswer(t *testing.T) {
	sub := &mockProvider{name: "sub", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "found 3 callers of Foo"},
		{Type: provider.ChunkDone},
	}}
	parentReg := tool.NewRegistry()
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "test-sys-prompt", nil)

	out, err := task.Execute(context.Background(), []byte(`{"prompt":"find callers of Foo"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "found 3 callers of Foo") {
		t.Errorf("got %q, want sub-agent final answer", out)
	}

	// The sub-agent must have received the prompt as its user message and
	// the configured system prompt at the top — proving the session was
	// fresh, not the parent's.
	if sys := sub.lastReq.Messages[0]; sys.Role != provider.RoleSystem || sys.Content != "test-sys-prompt" {
		t.Errorf("first message = %+v, want system 'test-sys-prompt'", sys)
	}
	if got := lastUser(sub.lastReq); got != "find callers of Foo" {
		t.Errorf("sub-agent user = %q, want the prompt verbatim", got)
	}
}

// TestTaskToolFiltersTools verifies the whitelist behaviour: when the caller
// names a subset of tools, the sub-agent's registry contains exactly that set
// with subagent/skill meta-tools stripped to prevent recursive delegation.
func TestTaskToolFiltersTools(t *testing.T) {
	sub := &mockProvider{name: "sub", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}
	parentReg := tool.NewRegistry()
	parentReg.Add(fakeTool{name: "read_file", readOnly: true})
	parentReg.Add(fakeTool{name: "write_file", readOnly: false})
	parentReg.Add(fakeTool{name: "bash", readOnly: false})
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "sys", nil)
	parentReg.Add(task) // simulate the wiring in cli.setup
	parentReg.Add(NewAgentsTool(task))
	parentReg.Add(fakeTool{name: "run_skill", readOnly: false})
	parentReg.Add(fakeTool{name: "research", readOnly: false})

	args := []byte(`{"prompt":"x","tools":["read_file","task","agents","write_file","run_skill","research"]}`)
	if _, err := task.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// The sub-agent's tool schemas should reflect the whitelist minus meta-tools.
	got := map[string]bool{}
	for _, s := range sub.lastReq.Tools {
		got[s.Name] = true
	}
	if !got["read_file"] || !got["write_file"] || got["task"] || got["agents"] || got["run_skill"] || got["research"] || got["bash"] {
		t.Errorf("sub-agent tools = %v, want {read_file, write_file} (meta-tools stripped, bash not requested)", got)
	}
}

// TestTaskToolDefaultsToParentToolsWithoutMetaTools covers the no-whitelist
// path: the sub-agent inherits parent tools except subagent/skill meta-tools.
func TestTaskToolDefaultsToParentToolsWithoutMetaTools(t *testing.T) {
	sub := &mockProvider{name: "sub", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}
	parentReg := tool.NewRegistry()
	parentReg.Add(fakeTool{name: "read_file", readOnly: true})
	parentReg.Add(fakeTool{name: "grep", readOnly: true})
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "sys", nil)
	parentReg.Add(task)
	parentReg.Add(NewAgentsTool(task))
	parentReg.Add(fakeTool{name: "run_skill", readOnly: false})
	parentReg.Add(fakeTool{name: "explore", readOnly: false})
	parentReg.Add(fakeTool{name: "research", readOnly: false})
	parentReg.Add(fakeTool{name: "review", readOnly: false})
	parentReg.Add(fakeTool{name: "security_review", readOnly: false})
	parentReg.Add(fakeTool{name: "remember", readOnly: false})

	if _, err := task.Execute(context.Background(), []byte(`{"prompt":"x"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := map[string]bool{}
	for _, s := range sub.lastReq.Tools {
		got[s.Name] = true
	}
	if !got["read_file"] || !got["grep"] || !got["remember"] ||
		got["task"] || got["agents"] || got["run_skill"] || got["explore"] || got["research"] || got["review"] || got["security_review"] {
		t.Errorf("default sub-agent tools = %v, want normal tools inherited and meta-tools stripped", got)
	}
}

func TestAgentsToolRunsBatchAndNestsSyntheticAgentEvents(t *testing.T) {
	sub := &promptProvider{}
	parentReg := tool.NewRegistry()
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "sys", nil)
	agents := NewAgentsTool(task)
	parentReg.Add(task)
	parentReg.Add(agents)

	var got []event.Event
	sink := event.FuncSink(func(e event.Event) { got = append(got, e) })
	ctx := withCallContext(context.Background(), "root-call", sink, nil)
	out, err := agents.Execute(ctx, []byte(`{"agents":[
		{"id":"a","description":"first scan","prompt":"alpha"},
		{"id":"b","description":"second scan","prompt":"beta"}
	],"concurrency":2}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "| first scan | - | alpha | - | done | ready |") ||
		!strings.Contains(out, "| second scan | - | beta | - | done | ready |") {
		t.Fatalf("summary table missing answers:\n%s", out)
	}

	dispatches, results := 0, 0
	for _, e := range got {
		if e.Tool.Name != "agent" {
			continue
		}
		if e.Tool.ParentID != "root-call" {
			t.Fatalf("agent event parentID = %q, want root-call", e.Tool.ParentID)
		}
		switch e.Kind {
		case event.ToolDispatch:
			dispatches++
		case event.ToolResult:
			results++
		}
	}
	if dispatches != 2 || results != 2 {
		t.Fatalf("agent events dispatch/results = %d/%d, want 2/2", dispatches, results)
	}
}

func TestAgentsToolPersistsManagerRunLog(t *testing.T) {
	sub := &promptProvider{}
	parentReg := tool.NewRegistry()
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "sys", nil)
	agents := NewAgentsTool(task)
	logDir := t.TempDir()
	if err := os.Chmod(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agents.SetRunStore(logDir)
	parentReg.Add(task)
	parentReg.Add(agents)

	out, err := agents.Execute(context.Background(), []byte(`{"agents":[{"id":"scan","description":"scan code","prompt":"alpha"}]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Manager run log:") {
		t.Fatalf("summary should include manager run log path:\n%s", out)
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("run log files = %d, want 1", len(entries))
	}
	dirInfo, err := os.Stat(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("run log dir mode = %o, want 700", got)
	}
	logPath := filepath.Join(logDir, entries[0].Name())
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("run log mode = %o, want 600", got)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{`"event":"started"`, `"event":"agent_finished"`, `"event":"finished"`, `"status":"done"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("run log missing %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, "alpha") {
		t.Fatalf("run log should not persist full agent prompt:\n%s", text)
	}
	if !strings.Contains(text, `"prompt_redacted":true`) {
		t.Fatalf("run log should mark prompts redacted:\n%s", text)
	}
}

func TestAgentsToolRejectsParallelWriterTools(t *testing.T) {
	sub := &promptProvider{}
	parentReg := tool.NewRegistry()
	parentReg.Add(fakeTool{name: "read_file", readOnly: true})
	parentReg.Add(fakeTool{name: "write_file", readOnly: false})
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "sys", nil)
	agents := NewAgentsTool(task)
	parentReg.Add(task)
	parentReg.Add(agents)

	out, err := agents.Execute(context.Background(), []byte(`{"agents":[
		{"description":"first","prompt":"alpha","tools":["write_file"]},
		{"description":"second","prompt":"beta","tools":["read_file"]}
	],"concurrency":2}`))
	if err == nil || !strings.Contains(out, "use isolation worktree") {
		t.Fatalf("Execute error/output = %v\n%s\nwant writer-tool rejection", err, out)
	}
}

func TestAgentsToolCreatesWorktreeAndReportsBranch(t *testing.T) {
	repo := initGitRepo(t)
	base := "agent-worktrees"
	sub := &promptProvider{}
	parentReg := tool.NewRegistry()
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "sys", nil)
	agents := NewAgentsTool(task)
	cleanupCalls := 0
	agents.SetWorktreeTools(func(dir string, write bool, names []string) (*tool.Registry, func(), error) {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("worktree dir should exist before registry build: %v", err)
		}
		return tool.NewRegistry(), func() { cleanupCalls++ }, nil
	})
	parentReg.Add(task)
	parentReg.Add(agents)

	args := marshalAgentsArgs(t, map[string]any{
		"isolation":     "worktree",
		"mode":          "write",
		"worktree_base": base,
		"agents": []map[string]any{{
			"id":           "worker",
			"description":  "worktree worker",
			"prompt":       "do work",
			"branch":       "feature/worker",
			"worktree_dir": "worker",
		}},
	})
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	out, err := agents.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Execute should block write-mode agent with no commits:\n%s", out)
	}
	if !strings.Contains(out, "| worktree worker | feature/worker |") ||
		!strings.Contains(out, "| - | blocked | no commits produced") {
		t.Fatalf("worktree summary missing branch/no-commit state:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(repo), base, "worker")); err != nil {
		t.Fatalf("worktree was not created: %v", err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("worktree tool cleanup calls = %d, want 1", cleanupCalls)
	}
}

func TestAgentsToolBlocksReadOnlyWorktreeMutation(t *testing.T) {
	repo := initGitRepo(t)
	base := "agent-worktrees"
	sub := &promptProvider{}
	parentReg := tool.NewRegistry()
	task := NewTaskTool(sub, nil, parentReg, 20, 0, 0.0, "", "sys", nil)
	agents := NewAgentsTool(task)
	agents.SetWorktreeTools(func(dir string, write bool, names []string) (*tool.Registry, func(), error) {
		if write {
			t.Fatalf("read-only worktree agent should not request write tools")
		}
		if err := os.WriteFile(filepath.Join(dir, "unexpected.txt"), []byte("dirty\n"), 0o644); err != nil {
			return nil, nil, err
		}
		return tool.NewRegistry(), nil, nil
	})
	parentReg.Add(task)
	parentReg.Add(agents)

	args := marshalAgentsArgs(t, map[string]any{
		"isolation":     "worktree",
		"worktree_base": base,
		"agents": []map[string]any{{
			"id":           "reader",
			"description":  "read-only scan",
			"prompt":       "inspect only",
			"branch":       "feature/reader",
			"worktree_dir": "reader",
		}},
	})
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	out, err := agents.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Execute should block read-only worktree mutation:\n%s", out)
	}
	if !strings.Contains(out, "| read-only scan | feature/reader |") ||
		!strings.Contains(out, "| - | blocked | read_only agent modified the worktree") {
		t.Fatalf("worktree summary missing read-only mutation block:\n%s", out)
	}
	if strings.Contains(out, "Merge plan:") {
		t.Fatalf("blocked read-only mutation should not produce a merge plan:\n%s", out)
	}
}

func TestResolveWorktreeDirStaysUnderRepoParent(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveWorktreeDir(repo, "agent-worktrees", "worker")
	if err != nil {
		t.Fatalf("resolveWorktreeDir: %v", err)
	}
	want := filepath.Join(filepath.Dir(repo), "agent-worktrees", "worker")
	if got != want {
		t.Fatalf("resolveWorktreeDir = %q, want %q", got, want)
	}
	for _, tc := range []struct {
		name string
		base string
		dir  string
	}{
		{name: "absolute dir", dir: filepath.Join(t.TempDir(), "worker")},
		{name: "absolute base", base: t.TempDir(), dir: "worker"},
		{name: "escaping base", base: "../outside", dir: "worker"},
		{name: "escaping dir", base: "agent-worktrees", dir: "../outside"},
		{name: "root dir", base: "agent-worktrees", dir: "."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := resolveWorktreeDir(repo, tc.base, tc.dir); err == nil {
				t.Fatal("resolveWorktreeDir should reject unsafe path")
			}
		})
	}
	outside := t.TempDir()
	linkBase := filepath.Join(filepath.Dir(repo), "linked-base")
	if err := os.Symlink(outside, linkBase); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := resolveWorktreeDir(repo, filepath.Base(linkBase), "worker"); err == nil {
		t.Fatal("resolveWorktreeDir should reject symlinked bases outside the repository parent")
	}
}

func TestAgentsPayloadLayersRejectsCycles(t *testing.T) {
	p := agentsPayload{Agents: []agentSpec{
		{ID: "a", Prompt: "a", DependsOn: []string{"b"}},
		{ID: "b", Prompt: "b", DependsOn: []string{"a"}},
	}}
	if err := p.normalize(4); err != nil {
		t.Fatal(err)
	}
	if err := p.fillAgentDefaults("run"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.layers(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("layers error = %v, want cycle", err)
	}
}

func TestAgentsManagerActionsMergeAndCleanup(t *testing.T) {
	repo := initGitRepo(t)
	wt := filepath.Join(t.TempDir(), "worker")
	runGit(t, repo, "worktree", "add", "-b", "feature/worker", wt)
	if err := os.WriteFile(filepath.Join(wt, "worker.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt, "add", "worker.txt")
	runGit(t, wt, "-c", "user.name=Reasonix Test", "-c", "user.email=test@example.com", "commit", "-m", "worker: add file")
	commit := strings.TrimSpace(gitOutputOrFatal(t, wt, "rev-parse", "HEAD"))

	results := []result{{
		label: "worker", task: "write file", status: "done",
		branch: "feature/worker", worktree: wt, commits: []string{commit},
	}}
	actions := runManagerActions(repo, results, []int{0}, agentsPayload{
		MergeStrategy: "merge",
		Cleanup:       "merged_branches",
	})
	if actions.err != nil {
		t.Fatalf("manager actions: %v", actions.err)
	}
	if len(actions.merged) != 1 || actions.merged[0] != "feature/worker" {
		t.Fatalf("merged = %v, want feature/worker", actions.merged)
	}
	if len(actions.cleaned) != 1 || len(actions.deleted) != 1 {
		t.Fatalf("cleanup cleaned/deleted = %v/%v, want one each", actions.cleaned, actions.deleted)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, stat err=%v", err)
	}
	if out := gitOutputOrFatal(t, repo, "branch", "--list", "feature/worker"); strings.TrimSpace(out) != "" {
		t.Fatalf("branch should be deleted, got %q", out)
	}
	if b, err := os.ReadFile(filepath.Join(repo, "worker.txt")); err != nil || string(b) != "done\n" {
		t.Fatalf("merged file = %q err=%v", b, err)
	}
}

type promptProvider struct {
	mu sync.Mutex
}

func (p *promptProvider) Name() string { return "prompt" }

func (p *promptProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	answer := "answer for " + lastUser(req)
	p.mu.Unlock()
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: answer}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func marshalAgentsArgs(t *testing.T, v map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Reasonix Test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitOutputOrFatal(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
