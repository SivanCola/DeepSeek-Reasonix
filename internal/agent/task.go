package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// DefaultTaskSystemPrompt steers a sub-agent toward focused, terse delivery —
// it doesn't see the parent's conversation so it must self-contain.
const DefaultTaskSystemPrompt = `You are a sub-agent invoked by a parent coding agent to carry out one focused task.
Use the provided tools to investigate or act. Return a single final answer that is concise
and self-contained — the parent will see only that answer, not your tool calls or reasoning.
If you need to ask for clarification, fail with a precise question instead of guessing.`

var subagentMetaTools = []string{
	"task",
	"agents",
	"run_skill",
	"install_skill",
	"explore",
	"research",
	"review",
	"security_review",
}

// SubagentMetaTools returns the tool names that spawned agents should not inherit
// from the parent registry unless a future call site deliberately opts into a
// different boundary. They can spawn or author more agent work, so excluding them
// preserves one layer of delegation without adding a spawn-count cap.
func SubagentMetaTools() []string {
	out := make([]string, len(subagentMetaTools))
	copy(out, subagentMetaTools)
	return out
}

// TaskTool spawns a sub-agent in its own session for a focused sub-task. The
// sub-agent runs with a filtered tool whitelist and the same step budget shape
// as the parent (see Execute); its tool calls are forwarded to the parent's
// event stream nested under this call, while only its final assistant message is
// returned to the parent model. Use cases: keep noisy tool sequences (multi-file
// exploration, repeated grep / read_file) out of the parent's context budget, or
// parallel research across independent areas (the parallel-dispatch path picks
// these up only when readOnly, which task is not).
type TaskTool struct {
	prov          provider.Provider
	pricing       *provider.Pricing
	parentReg     *tool.Registry
	maxSteps      int
	contextWindow int
	temperature   float64
	archiveDir    string
	sysPrompt     string
	gate          Gate
}

// NewTaskTool wires a task tool to the parent agent's environment so its
// sub-agents can use the same provider and tools. sysPrompt is the system
// prompt every sub-agent starts with; pass "" for DefaultTaskSystemPrompt. gate
// is the permission gate sub-agents inherit — pass the headless variant so
// deny rules still bite while autonomous sub-agents are never blocked on an
// interactive prompt (there is no UI to answer one).
func NewTaskTool(prov provider.Provider, pricing *provider.Pricing, parentReg *tool.Registry,
	maxSteps, contextWindow int, temperature float64, archiveDir, sysPrompt string, gate Gate) *TaskTool {
	if sysPrompt == "" {
		sysPrompt = DefaultTaskSystemPrompt
	}
	return &TaskTool{
		prov:          prov,
		pricing:       pricing,
		parentReg:     parentReg,
		maxSteps:      maxSteps,
		contextWindow: contextWindow,
		temperature:   temperature,
		archiveDir:    archiveDir,
		sysPrompt:     sysPrompt,
		gate:          gate,
	}
}

func (t *TaskTool) Name() string { return "task" }

func (t *TaskTool) Description() string {
	return "Spawn a sub-agent for a focused sub-task. The sub-agent runs in its own session with the same provider and a filtered tool list (defaults to every parent tool except subagent/skill meta-tools, so delegation stays one layer deep). Only its final answer is returned. Use this to (a) keep long exploration sequences out of the parent's context budget, or (b) delegate self-contained work like 'find every place that calls X and summarise the patterns'."
}

func (t *TaskTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "prompt":{"type":"string","description":"What the sub-agent should accomplish. Be specific about the deliverable — the sub-agent does not see this conversation."},
  "description":{"type":"string","description":"Short label for the sub-task (3-7 words). Surfaced in the dispatch line so the user sees what's running."},
  "tools":{"type":"array","items":{"type":"string"},"description":"Optional tool whitelist. Subagent/skill meta-tools are still excluded so delegation stays one layer deep."},
  "max_steps":{"type":"integer","description":"Optional cap on tool-call rounds. Defaults to half the parent's cap (min 5).","minimum":1},
  "run_in_background":{"type":"boolean","description":"Run the sub-agent asynchronously: returns a job id immediately and keeps working across turns. Collect its final answer with wait, and you'll be notified when it finishes. Use for long, independent sub-tasks you don't need to block on right now."}
},
"required":["prompt"]
}`)
}

// ReadOnly is false: a sub-agent can invoke any whitelisted tool, including
// writers. Conservative classification keeps the parallel-dispatch path from
// running two sub-agents at once and letting their writes race.
func (t *TaskTool) ReadOnly() bool { return false }

func (t *TaskTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Prompt          string   `json:"prompt"`
		Description     string   `json:"description"`
		Tools           []string `json:"tools"`
		MaxSteps        int      `json:"max_steps"`
		RunInBackground bool     `json:"run_in_background"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}

	maxSteps := p.MaxSteps
	if maxSteps <= 0 {
		// No explicit cap from the caller: mirror the parent. A finite parent caps
		// the sub-agent at half its budget (min 5) so a delegated sub-task stays
		// shorter than the whole turn; an unbounded parent yields an unbounded
		// sub-agent. The sub-agent shares the parent's ctx, so cancelling the turn
		// stops it, and it compacts its own context — the same bounds the parent has.
		if t.maxSteps > 0 {
			maxSteps = t.maxSteps / 2
			if maxSteps < 5 {
				maxSteps = 5
			}
		}
	}

	subReg := t.buildSubReg(p.Tools)

	// Background: register a job that runs the sub-agent under the manager's
	// session context (so it survives this turn) and return immediately. The
	// sub-agent's tool activity still streams, nested under this call, because the
	// nested sink captures the parent ID + stream now (not from the job ctx).
	if p.RunInBackground {
		jm, ok := jobs.FromContext(ctx)
		if !ok {
			return "", fmt.Errorf("background execution is not available in this context")
		}
		parentID, parent, _, _ := CallContext(ctx)
		nested := subSinkFor(parentID, parent)
		label := p.Description
		if label == "" {
			label = "task"
		}
		job := jm.Start("task", label, func(jobCtx context.Context, _ io.Writer) (string, error) {
			return t.runSub(jobCtx, p.Prompt, subReg, nested, maxSteps)
		})
		return fmt.Sprintf("Started background task %q (%s). It runs across turns; collect its final answer with wait (or wait will return it once done), and you'll be notified when it finishes.", job.ID, label), nil
	}

	// Foreground: run synchronously, nesting events under this call.
	return t.runSub(ctx, p.Prompt, subReg, subSink(ctx), maxSteps)
}

// AgentsTool runs several focused sub-agents as one coordinated batch. It gives
// the model a stable, explicit way to fan out independent investigations without
// relying on provider-side parallel tool dispatch, while keeping every child in
// its own session so noisy exploration does not enter the parent's context.
type AgentsTool struct {
	task           *TaskTool
	defaultMaxRuns int
	worktreeTools  func(dir string, write bool, names []string) (*tool.Registry, func(), error)
	runStoreDir    string
}

// NewAgentsTool builds the batch sub-agent tool on top of the existing task
// machinery so both paths share model, tools, budget, permission, and cache
// behavior.
func NewAgentsTool(task *TaskTool) *AgentsTool {
	return &AgentsTool{task: task, defaultMaxRuns: 4}
}

// SetWorktreeTools installs the per-worktree registry builder used by
// isolation:"worktree". The agent package does not import built-in tools
// directly; boot supplies a workspace-bound builder so child agents can run in
// an isolated cwd/root without leaking that composition detail into the loop.
func (t *AgentsTool) SetWorktreeTools(fn func(dir string, write bool, names []string) (*tool.Registry, func(), error)) {
	t.worktreeTools = fn
}

// SetRunStore enables append-only manager run logs for agents batches. Empty
// disables persistence.
func (t *AgentsTool) SetRunStore(dir string) {
	t.runStoreDir = dir
}

func (t *AgentsTool) Name() string { return "agents" }

func (t *AgentsTool) Description() string {
	return "Spawn multiple focused sub-agents for independent subtasks. Defaults to read-only session isolation for parallel exploration; set isolation:\"worktree\" and mode:\"write\" for physically isolated writable agents that work on separate git branches. Supports depends_on DAG ordering, returns branch/commit/status summaries, and nests live child tool activity under this batch."
}

func (t *AgentsTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "agents":{"type":"array","minItems":1,"maxItems":4,"items":{"type":"object","properties":{
    "id":{"type":"string","description":"Stable short id for this sub-agent, e.g. frontend or api."},
    "description":{"type":"string","description":"Short label for display (3-7 words)."},
    "prompt":{"type":"string","description":"Self-contained task prompt. The sub-agent does not see this conversation."},
    "tools":{"type":"array","items":{"type":"string"},"description":"Optional tool whitelist. In read_only mode, writer tools are rejected. In worktree/write mode, writer tools are allowed inside that worktree."},
    "max_steps":{"type":"integer","description":"Optional cap on tool-call rounds for this sub-agent. Defaults to half the parent's cap (min 5).","minimum":1},
    "depends_on":{"type":"array","items":{"type":"string"},"description":"Agent ids that must finish with status done before this agent starts."},
    "isolation":{"type":"string","enum":["session","worktree"],"description":"Override batch isolation for this agent. session is context-only; worktree creates a git worktree and branch."},
    "mode":{"type":"string","enum":["read_only","write"],"description":"Override batch mode. write requires isolation worktree."},
    "branch":{"type":"string","description":"Optional branch name for worktree isolation. Defaults to branch_prefix/id-run."},
    "worktree_dir":{"type":"string","description":"Optional worktree directory. Relative paths resolve under worktree_base or the repository parent."}
  },"required":["prompt"]}},
  "concurrency":{"type":"integer","description":"Maximum sub-agents to run at once within one DAG layer. Defaults to 4.","minimum":1,"maximum":4},
  "isolation":{"type":"string","enum":["session","worktree"],"description":"Default isolation for agents. Defaults to session."},
  "mode":{"type":"string","enum":["read_only","write"],"description":"Default agent mode. Defaults to read_only. write requires worktree isolation."},
  "branch_prefix":{"type":"string","description":"Branch prefix for generated worktree branches. Defaults to reasonix/agent."},
  "worktree_base":{"type":"string","description":"Directory where generated worktrees are created. Relative paths resolve from the repository parent."},
  "base_ref":{"type":"string","description":"Git ref used as the worktree base. Defaults to HEAD."},
  "merge_strategy":{"type":"string","enum":["none","plan","merge"],"description":"How to handle successful worktree branches. plan returns merge commands; merge runs them when the parent repo is clean; none omits merge handling. Defaults to plan."},
  "cleanup":{"type":"string","enum":["none","worktrees","merged_branches"],"description":"Optional cleanup after successful agents. worktrees removes clean worktree directories; merged_branches also deletes branches after merge_strategy merge succeeds. Defaults to none."}
},
"required":["agents"]
}`)
}

// ReadOnly is false because this tool starts model loops, not a simple data
// lookup. Child agents default to reader tools; the batch owns concurrency
// instead of letting the generic dispatcher make ordering assumptions.
func (t *AgentsTool) ReadOnly() bool { return false }

func (t *AgentsTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil || t.task == nil {
		return "", fmt.Errorf("agents tool is not configured")
	}
	var p agentsPayload
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := p.normalize(t.defaultMaxRuns); err != nil {
		return "", err
	}
	runID := batchRunID(ctx)
	if err := p.fillAgentDefaults(runID); err != nil {
		return "", err
	}
	layers, err := p.layers()
	if err != nil {
		return "", err
	}
	repo, err := maybeGitRoot()
	if err != nil && p.needsWorktree() {
		return "", err
	}

	parentID, parentSink, _, ok := CallContext(ctx)
	if !ok || parentSink == nil {
		parentSink = event.Discard
	}
	serialSink := event.Sync(parentSink)
	rec, err := newManagerRunRecorder(t.runStoreDir, runID)
	if err != nil {
		serialSink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn,
			Text: "agents: manager run log disabled (" + err.Error() + ")"})
	}
	if rec != nil {
		rec.Record("started", managerRunStartRecord(repo, p))
	}

	results := make([]result, len(p.Agents))
	byID := map[string]int{}
	for i, spec := range p.Agents {
		byID[spec.ID] = i
		results[i] = result{
			id: spec.ID, label: spec.label(), task: spec.Prompt,
			status: "pending", isolation: spec.Isolation, mode: spec.Mode,
		}
	}

	var topoOrder []int
	for _, layer := range layers {
		topoOrder = append(topoOrder, layer...)
		var runnable []int
		for _, i := range layer {
			blockedBy := blockedDependency(p.Agents[i], results, byID)
			if blockedBy == "" {
				runnable = append(runnable, i)
				continue
			}
			results[i].status = "blocked"
			results[i].next = "dependency " + blockedBy + " did not finish successfully"
			t.emitSyntheticAgent(serialSink, parentID, i, p.Agents[i], results[i])
			if rec != nil {
				rec.Record("agent_finished", managerRunResultRecord(results[i]))
			}
		}
		sem := make(chan struct{}, p.Concurrency)
		var wg sync.WaitGroup
		for _, i := range runnable {
			i := i
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				results[i] = t.runAgentSpec(ctx, serialSink, parentID, i, p, p.Agents[i], repo, rec)
			}()
		}
		wg.Wait()
	}

	failed := hasFailedAgent(results)
	actions := managerActions{}
	if rec != nil {
		actions.runLog = rec.Path()
	}
	if !failed {
		actions = runManagerActions(repo, results, topoOrder, p)
		if rec != nil {
			actions.runLog = rec.Path()
			rec.Record("manager_actions", managerRunActionsRecord(actions))
		}
	}
	if failed {
		if rec != nil {
			rec.Record("finished", map[string]any{"status": "failed"})
		}
		addManagerRunWarning(&actions, rec)
		out := renderAgentsResult(results, p.MergeStrategy, topoOrder, actions)
		return out, fmt.Errorf("one or more agents failed or blocked")
	}
	if actions.err != nil {
		if rec != nil {
			rec.Record("finished", map[string]any{"status": "failed", "error": actions.err.Error()})
		}
		addManagerRunWarning(&actions, rec)
		out := renderAgentsResult(results, p.MergeStrategy, topoOrder, actions)
		return out, actions.err
	}
	if rec != nil {
		rec.Record("finished", map[string]any{"status": "done"})
	}
	addManagerRunWarning(&actions, rec)
	out := renderAgentsResult(results, p.MergeStrategy, topoOrder, actions)
	return out, nil
}

type agentsPayload struct {
	Agents        []agentSpec `json:"agents"`
	Concurrency   int         `json:"concurrency"`
	Isolation     string      `json:"isolation"`
	Mode          string      `json:"mode"`
	BranchPrefix  string      `json:"branch_prefix"`
	WorktreeBase  string      `json:"worktree_base"`
	BaseRef       string      `json:"base_ref"`
	MergeStrategy string      `json:"merge_strategy"`
	Cleanup       string      `json:"cleanup"`
}

type agentSpec struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools"`
	MaxSteps    int      `json:"max_steps"`
	DependsOn   []string `json:"depends_on"`
	Isolation   string   `json:"isolation"`
	Mode        string   `json:"mode"`
	Branch      string   `json:"branch"`
	WorktreeDir string   `json:"worktree_dir"`
}

type result struct {
	id        string
	label     string
	task      string
	answer    string
	status    string
	next      string
	err       error
	branch    string
	worktree  string
	commits   []string
	isolation string
	mode      string
}

func (p *agentsPayload) normalize(maxRuns int) error {
	if len(p.Agents) == 0 {
		return fmt.Errorf("agents must contain at least one sub-agent")
	}
	if len(p.Agents) > maxRuns {
		return fmt.Errorf("agents supports at most %d sub-agents per batch", maxRuns)
	}
	if p.Concurrency <= 0 || p.Concurrency > maxRuns {
		p.Concurrency = maxRuns
	}
	if p.Concurrency > len(p.Agents) {
		p.Concurrency = len(p.Agents)
	}
	p.Isolation = defaultString(p.Isolation, "session")
	p.Mode = defaultString(p.Mode, "read_only")
	p.BranchPrefix = defaultString(p.BranchPrefix, "reasonix/agent")
	p.BaseRef = defaultString(p.BaseRef, "HEAD")
	p.MergeStrategy = defaultString(p.MergeStrategy, "plan")
	p.Cleanup = defaultString(p.Cleanup, "none")
	if !validIsolation(p.Isolation) {
		return fmt.Errorf("invalid isolation %q", p.Isolation)
	}
	if !validMode(p.Mode) {
		return fmt.Errorf("invalid mode %q", p.Mode)
	}
	if p.Mode == "write" && p.Isolation != "worktree" {
		return fmt.Errorf("mode write requires isolation worktree")
	}
	if p.MergeStrategy != "none" && p.MergeStrategy != "plan" && p.MergeStrategy != "merge" {
		return fmt.Errorf("invalid merge_strategy %q", p.MergeStrategy)
	}
	if p.Cleanup != "none" && p.Cleanup != "worktrees" && p.Cleanup != "merged_branches" {
		return fmt.Errorf("invalid cleanup %q", p.Cleanup)
	}
	if p.Cleanup == "merged_branches" && p.MergeStrategy != "merge" {
		return fmt.Errorf("cleanup merged_branches requires merge_strategy merge")
	}
	return nil
}

func (p *agentsPayload) fillAgentDefaults(runID string) error {
	seen := map[string]bool{}
	for i := range p.Agents {
		a := &p.Agents[i]
		if strings.TrimSpace(a.Prompt) == "" {
			return fmt.Errorf("agents[%d].prompt is required", i)
		}
		a.ID = safeID(defaultString(a.ID, fmt.Sprintf("agent-%d", i+1)))
		if seen[a.ID] {
			return fmt.Errorf("duplicate agent id %q", a.ID)
		}
		seen[a.ID] = true
		a.Isolation = defaultString(a.Isolation, p.Isolation)
		a.Mode = defaultString(a.Mode, p.Mode)
		if !validIsolation(a.Isolation) {
			return fmt.Errorf("agents[%d].isolation %q is invalid", i, a.Isolation)
		}
		if !validMode(a.Mode) {
			return fmt.Errorf("agents[%d].mode %q is invalid", i, a.Mode)
		}
		if a.Mode == "write" && a.Isolation != "worktree" {
			return fmt.Errorf("agents[%d]: mode write requires isolation worktree", i)
		}
		if a.Isolation == "worktree" {
			if strings.TrimSpace(a.Branch) == "" {
				a.Branch = strings.TrimRight(p.BranchPrefix, "/") + "/" + a.ID + "-" + runID
			}
			if strings.TrimSpace(a.WorktreeDir) == "" {
				a.WorktreeDir = "wt-" + a.ID + "-" + runID
			}
		}
		for j, dep := range a.DependsOn {
			a.DependsOn[j] = safeID(dep)
		}
	}
	for i, a := range p.Agents {
		for _, dep := range a.DependsOn {
			if !seen[dep] {
				return fmt.Errorf("agents[%d] depends on unknown agent %q", i, dep)
			}
		}
	}
	return nil
}

func (p agentsPayload) needsWorktree() bool {
	for _, a := range p.Agents {
		if a.Isolation == "worktree" {
			return true
		}
	}
	return false
}

func (p agentsPayload) layers() ([][]int, error) {
	indegree := make([]int, len(p.Agents))
	next := make(map[int][]int, len(p.Agents))
	byID := map[string]int{}
	for i, a := range p.Agents {
		byID[a.ID] = i
	}
	for i, a := range p.Agents {
		for _, dep := range a.DependsOn {
			j := byID[dep]
			indegree[i]++
			next[j] = append(next[j], i)
		}
	}
	var ready []int
	for i, n := range indegree {
		if n == 0 {
			ready = append(ready, i)
		}
	}
	var layers [][]int
	seen := 0
	for len(ready) > 0 {
		sort.Ints(ready)
		layer := append([]int(nil), ready...)
		layers = append(layers, layer)
		seen += len(layer)
		ready = nil
		for _, i := range layer {
			for _, child := range next[i] {
				indegree[child]--
				if indegree[child] == 0 {
					ready = append(ready, child)
				}
			}
		}
	}
	if seen != len(p.Agents) {
		return nil, fmt.Errorf("agents dependency graph contains a cycle")
	}
	return layers, nil
}

func (a agentSpec) label() string {
	label := strings.TrimSpace(a.Description)
	if label == "" {
		label = a.ID
	}
	return label
}

func (t *AgentsTool) runAgentSpec(ctx context.Context, sink event.Sink, parentID string, idx int, p agentsPayload, spec agentSpec, repo string, rec *managerRunRecorder) result {
	res := result{
		id: spec.ID, label: spec.label(), task: spec.Prompt, status: "done",
		isolation: spec.Isolation, mode: spec.Mode, branch: spec.Branch,
	}
	childID := childAgentID(parentID, idx+1)
	displayWorktree := spec.WorktreeDir
	if spec.Isolation == "worktree" {
		if wt, err := resolveWorktreeDir(repo, p.WorktreeBase, spec.WorktreeDir); err == nil {
			displayWorktree = wt
		}
	}
	childArgs := agentEventArgs(spec, displayWorktree)
	sink.Emit(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
		ID: childID, Name: "agent", Args: string(childArgs), ParentID: parentID,
	}})

	workDir, baseCommit, err := t.prepareAgentWorkspace(repo, p, spec)
	if err != nil {
		res.status, res.err, res.next = "failed", err, "fix worktree setup and retry"
		t.emitAgentResult(sink, parentID, childID, childArgs, res)
		if rec != nil {
			rec.Record("agent_finished", managerRunResultRecord(res))
		}
		return res
	}
	res.worktree = workDir

	subReg, cleanup, err := t.buildBatchSubReg(spec.Tools, spec.Isolation, spec.Mode, workDir)
	if err != nil {
		res.status, res.err, res.next = "failed", err, "adjust tools/isolation/mode"
		t.emitAgentResult(sink, parentID, childID, childArgs, res)
		if rec != nil {
			rec.Record("agent_finished", managerRunResultRecord(res))
		}
		return res
	}
	if cleanup != nil {
		defer cleanup()
	}

	answer, err := t.task.runSub(ctx, agentPrompt(spec, workDir), subReg, subSinkFor(childID, sink), agentMaxSteps(t.task.maxSteps, spec.MaxSteps))
	res.answer, res.err = answer, err
	if err != nil {
		res.status, res.next = "failed", "inspect sub-agent error"
	} else {
		res.next = "ready"
	}
	if spec.Isolation == "worktree" {
		res.commits = gitListCommits(workDir, baseCommit)
		dirty := gitDirty(workDir)
		if spec.Mode == "read_only" && (dirty || len(res.commits) > 0) {
			res.status = "blocked"
			res.next = "read_only agent modified the worktree; inspect and discard or rerun as mode write"
		} else if dirty {
			res.status = "blocked"
			res.next = "commit or clean dirty worktree before merge"
		} else if spec.Mode == "write" && len(res.commits) == 0 {
			res.status = "blocked"
			res.next = "no commits produced; inspect worktree or rerun with clearer task"
		} else if len(res.commits) > 0 {
			res.next = "ready to merge"
		}
	}
	t.emitAgentResult(sink, parentID, childID, childArgs, res)
	if rec != nil {
		rec.Record("agent_finished", managerRunResultRecord(res))
	}
	return res
}

func (t *AgentsTool) prepareAgentWorkspace(repo string, p agentsPayload, spec agentSpec) (workDir, baseCommit string, err error) {
	if spec.Isolation != "worktree" {
		return "", "", nil
	}
	wtDir, err := resolveWorktreeDir(repo, p.WorktreeBase, spec.WorktreeDir)
	if err != nil {
		return "", "", err
	}
	baseCommit, err = gitOutput(repo, "rev-parse", p.BaseRef)
	if err != nil {
		return "", "", fmt.Errorf("resolve base_ref %q: %w", p.BaseRef, err)
	}
	if err := os.MkdirAll(filepath.Dir(wtDir), 0o755); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(wtDir); err == nil {
		return "", "", fmt.Errorf("worktree_dir already exists: %s", wtDir)
	}
	if _, err := gitOutput(repo, "worktree", "add", "-b", spec.Branch, wtDir, p.BaseRef); err != nil {
		return "", "", err
	}
	return wtDir, strings.TrimSpace(baseCommit), nil
}

func (t *AgentsTool) emitSyntheticAgent(sink event.Sink, parentID string, idx int, spec agentSpec, res result) {
	childID := childAgentID(parentID, idx+1)
	childArgs := agentEventArgs(spec, spec.WorktreeDir)
	sink.Emit(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
		ID: childID, Name: "agent", Args: string(childArgs), ParentID: parentID,
	}})
	t.emitAgentResult(sink, parentID, childID, childArgs, res)
}

func (t *AgentsTool) emitAgentResult(sink event.Sink, parentID, childID string, childArgs []byte, res result) {
	ev := event.Event{Kind: event.ToolResult, Tool: event.Tool{
		ID: childID, Name: "agent", Args: string(childArgs), ParentID: parentID,
		Output: agentResultText(res),
	}}
	if res.err != nil {
		ev.Tool.Err = firstLine(res.err.Error())
	}
	sink.Emit(ev)
}

func agentEventArgs(spec agentSpec, worktree string) []byte {
	args, _ := json.Marshal(map[string]string{
		"id":          spec.ID,
		"description": spec.label(),
		"isolation":   spec.Isolation,
		"mode":        spec.Mode,
		"branch":      spec.Branch,
		"worktree":    worktree,
	})
	return args
}

func (t *AgentsTool) buildBatchSubReg(names []string, isolation, mode, workDir string) (*tool.Registry, func(), error) {
	writeMode := mode == "write"
	if writeMode && isolation != "worktree" {
		return nil, nil, fmt.Errorf("mode write requires isolation worktree")
	}
	if isolation == "worktree" {
		if t.worktreeTools == nil {
			return nil, nil, fmt.Errorf("worktree isolation is not available in this runtime")
		}
		if !writeMode {
			for _, name := range names {
				if tl, ok := t.task.parentReg.Get(name); ok && !tl.ReadOnly() {
					return nil, nil, fmt.Errorf("%q is not read-only; use mode write with worktree isolation for writer tools", name)
				}
			}
		}
		return t.worktreeTools(workDir, writeMode, names)
	}
	if writeMode {
		return nil, nil, fmt.Errorf("mode write requires isolation worktree")
	}
	if len(names) == 0 {
		var readers []string
		for _, name := range t.task.parentReg.Names() {
			if tl, ok := t.task.parentReg.Get(name); ok && tl.ReadOnly() {
				readers = append(readers, name)
			}
		}
		return t.task.buildSubReg(readers), nil, nil
	}
	for _, name := range names {
		if isSubagentMetaTool(name) {
			continue
		}
		if tl, ok := t.task.parentReg.Get(name); ok && !tl.ReadOnly() {
			return nil, nil, fmt.Errorf("%q is not read-only; use isolation worktree and mode write for writer tools", name)
		}
	}
	return t.task.buildSubReg(names), nil, nil
}

func isSubagentMetaTool(name string) bool {
	for _, meta := range subagentMetaTools {
		if name == meta {
			return true
		}
	}
	return false
}

func childAgentID(parentID string, n int) string {
	if parentID == "" {
		return fmt.Sprintf("agent-%d", n)
	}
	return fmt.Sprintf("%s/agent-%d", parentID, n)
}

func blockedDependency(spec agentSpec, results []result, byID map[string]int) string {
	for _, dep := range spec.DependsOn {
		if r := results[byID[dep]]; r.status != "done" {
			return dep
		}
	}
	return ""
}

func agentMaxSteps(parent, explicit int) int {
	if explicit > 0 {
		return explicit
	}
	if parent <= 0 {
		return 0
	}
	steps := parent / 2
	if steps < 5 {
		steps = 5
	}
	return steps
}

func agentPrompt(spec agentSpec, workDir string) string {
	if spec.Isolation != "worktree" {
		return spec.Prompt
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are a worktree-isolated sub-agent.\nWorktree: %s\nBranch: %s\nMode: %s\n", workDir, spec.Branch, spec.Mode)
	if spec.Mode == "write" {
		fmt.Fprintln(&b, "Edit only inside this worktree. Commit intended changes before your final answer. Use a concise commit message that starts with the agent id when practical.")
	} else {
		fmt.Fprintln(&b, "Stay read-only. Do not modify files or create commits.")
	}
	fmt.Fprintln(&b, "Final answer must be concise and include what changed or what you found, blockers, and next steps.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, spec.Prompt)
	return b.String()
}

func agentResultText(r result) string {
	if r.err != nil {
		return "error: " + r.err.Error()
	}
	if strings.TrimSpace(r.answer) == "" && strings.TrimSpace(r.next) != "" {
		return r.status + ": " + r.next
	}
	return r.answer
}

type managerActions struct {
	runLog   string
	merged   []string
	cleaned  []string
	deleted  []string
	warnings []string
	err      error
}

func runManagerActions(repo string, results []result, topoOrder []int, p agentsPayload) managerActions {
	var actions managerActions
	if repo == "" {
		return actions
	}
	if p.MergeStrategy == "merge" {
		branches := mergeBranches(results, topoOrder)
		actions = mergeAgentBranches(repo, branches)
		if actions.err != nil {
			return actions
		}
	}
	if p.Cleanup != "none" {
		cleanup := cleanupAgentWorktrees(repo, results, p.Cleanup, actions.merged)
		actions.cleaned = append(actions.cleaned, cleanup.cleaned...)
		actions.deleted = append(actions.deleted, cleanup.deleted...)
		actions.warnings = append(actions.warnings, cleanup.warnings...)
		if cleanup.err != nil {
			actions.err = cleanup.err
		}
	}
	return actions
}

func mergeAgentBranches(repo string, branches []string) managerActions {
	var actions managerActions
	if len(branches) == 0 {
		return actions
	}
	if gitDirty(repo) {
		actions.err = fmt.Errorf("parent worktree is dirty; commit/stash changes before auto-merge")
		return actions
	}
	for _, branch := range branches {
		if _, err := gitOutput(repo, "merge", "--no-ff", branch, "-m", "merge: "+branch); err != nil {
			_, _ = gitOutput(repo, "merge", "--abort")
			actions.err = fmt.Errorf("merge %s failed: %w", branch, err)
			return actions
		}
		actions.merged = append(actions.merged, branch)
	}
	return actions
}

func cleanupAgentWorktrees(repo string, results []result, cleanup string, merged []string) managerActions {
	var actions managerActions
	mergedSet := map[string]bool{}
	for _, b := range merged {
		mergedSet[b] = true
	}
	for _, r := range results {
		if r.worktree == "" {
			continue
		}
		if gitDirty(r.worktree) {
			actions.warnings = append(actions.warnings, fmt.Sprintf("kept dirty worktree %s", r.worktree))
			continue
		}
		if _, err := gitOutput(repo, "worktree", "remove", r.worktree); err != nil {
			actions.err = fmt.Errorf("remove worktree %s: %w", r.worktree, err)
			return actions
		}
		actions.cleaned = append(actions.cleaned, r.worktree)
		if cleanup == "merged_branches" && r.branch != "" && mergedSet[r.branch] {
			if _, err := gitOutput(repo, "branch", "-d", r.branch); err != nil {
				actions.err = fmt.Errorf("delete branch %s: %w", r.branch, err)
				return actions
			}
			actions.deleted = append(actions.deleted, r.branch)
		}
	}
	return actions
}

func renderAgentsResult(results []result, mergeStrategy string, topoOrder []int, actions managerActions) string {
	var out strings.Builder
	fmt.Fprintf(&out, "| agent | branch | task | commits | status | next |\n|---|---|---|---|---|---|\n")
	for _, r := range results {
		commits := "-"
		if len(r.commits) > 0 {
			commits = strings.Join(shortCommits(r.commits), ", ")
		}
		next := r.next
		if r.err != nil {
			next = firstLine(r.err.Error())
		}
		if r.status == "pending" {
			r.status = "blocked"
			next = "not started"
		}
		fmt.Fprintf(&out, "| %s | %s | %s | %s | %s | %s |\n",
			escapeTableCell(r.label),
			escapeTableCell(defaultString(r.branch, "-")),
			escapeTableCell(firstLine(r.task)),
			escapeTableCell(commits),
			escapeTableCell(r.status),
			escapeTableCell(defaultString(next, "-")),
		)
	}
	if mergeStrategy == "plan" {
		branches := mergeBranches(results, topoOrder)
		if len(branches) > 0 {
			fmt.Fprintf(&out, "\nMerge plan:\n")
			for i, b := range branches {
				fmt.Fprintf(&out, "%d. git merge --no-ff %s\n", i+1, b)
			}
		}
	}
	if len(actions.merged) > 0 {
		fmt.Fprintf(&out, "\nMerged:\n")
		for _, b := range actions.merged {
			fmt.Fprintf(&out, "- %s\n", b)
		}
	}
	if len(actions.cleaned) > 0 || len(actions.deleted) > 0 {
		fmt.Fprintf(&out, "\nCleanup:\n")
		for _, wt := range actions.cleaned {
			fmt.Fprintf(&out, "- removed worktree %s\n", wt)
		}
		for _, b := range actions.deleted {
			fmt.Fprintf(&out, "- deleted branch %s\n", b)
		}
	}
	if len(actions.warnings) > 0 {
		fmt.Fprintf(&out, "\nWarnings:\n")
		for _, w := range actions.warnings {
			fmt.Fprintf(&out, "- %s\n", w)
		}
	}
	if actions.err != nil {
		fmt.Fprintf(&out, "\nManager action failed: %s\n", actions.err.Error())
	}
	if actions.runLog != "" {
		fmt.Fprintf(&out, "\nManager run log:\n- %s\n", actions.runLog)
	}
	return out.String()
}

func mergeBranches(results []result, topoOrder []int) []string {
	var out []string
	if len(topoOrder) == 0 {
		topoOrder = make([]int, len(results))
		for i := range results {
			topoOrder[i] = i
		}
	}
	for _, idx := range topoOrder {
		if idx < 0 || idx >= len(results) {
			continue
		}
		r := results[idx]
		if r.status == "done" && r.branch != "" && len(r.commits) > 0 {
			out = append(out, r.branch)
		}
	}
	return out
}

func hasFailedAgent(results []result) bool {
	for _, r := range results {
		if r.status != "done" {
			return true
		}
	}
	return false
}

func addManagerRunWarning(actions *managerActions, rec *managerRunRecorder) {
	if actions == nil || rec == nil {
		return
	}
	if err := rec.Err(); err != nil {
		actions.warnings = append(actions.warnings, "manager run log incomplete: "+firstLine(err.Error()))
	}
}

func shortCommits(commits []string) []string {
	out := make([]string, len(commits))
	for i, c := range commits {
		if len(c) > 7 {
			out[i] = c[:7]
		} else {
			out[i] = c
		}
	}
	return out
}

func maybeGitRoot() (string, error) {
	root, err := gitOutput(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("worktree isolation requires a git repository: %w", err)
	}
	return strings.TrimSpace(root), nil
}

func resolveWorktreeDir(repo, base, dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("worktree_dir is required")
	}
	if filepath.IsAbs(dir) {
		return "", fmt.Errorf("worktree_dir must be relative to the repository parent")
	}
	repoAbs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	repoParent := filepath.Dir(filepath.Clean(repoAbs))
	root := repoParent
	if base != "" {
		if filepath.IsAbs(base) {
			return "", fmt.Errorf("worktree_base must be relative to the repository parent")
		}
		cleanBase := filepath.Clean(base)
		if pathEscapesRoot(cleanBase) {
			return "", fmt.Errorf("worktree_base must stay under the repository parent")
		}
		root = filepath.Join(root, cleanBase)
	}
	cleanDir := filepath.Clean(dir)
	if cleanDir == "." {
		return "", fmt.Errorf("worktree_dir must name a child directory")
	}
	if pathEscapesRoot(cleanDir) {
		return "", fmt.Errorf("worktree_dir must stay under the repository parent")
	}
	out, err := filepath.Abs(filepath.Join(root, cleanDir))
	if err != nil {
		return "", err
	}
	if !pathWithin(root, out) {
		return "", fmt.Errorf("worktree_dir must stay under the selected worktree base")
	}
	if err := existingPathStaysUnder(repoParent, out); err != nil {
		return "", err
	}
	return out, nil
}

func pathEscapesRoot(p string) bool {
	return p == ".." || strings.HasPrefix(p, ".."+string(os.PathSeparator))
}

func pathWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || !pathEscapesRoot(rel)
}

func existingPathStaysUnder(root, p string) error {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	ancestor, err := existingAncestor(p)
	if err != nil {
		return err
	}
	ancestorReal, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return err
	}
	if !pathWithin(rootReal, ancestorReal) {
		return fmt.Errorf("worktree_dir must not pass through symlinks outside the repository parent")
	}
	return nil
}

func existingAncestor(p string) (string, error) {
	for {
		if _, err := os.Lstat(p); err == nil {
			return p, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", os.ErrNotExist
		}
		p = parent
	}
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return string(b), fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(b)))
	}
	return string(b), nil
}

func gitListCommits(dir, base string) []string {
	if strings.TrimSpace(base) == "" {
		return nil
	}
	out, err := gitOutput(dir, "rev-list", "--reverse", strings.TrimSpace(base)+"..HEAD")
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

func gitDirty(dir string) bool {
	out, err := gitOutput(dir, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

func batchRunID(ctx context.Context) string {
	parentID, _, _, ok := CallContext(ctx)
	if ok && strings.TrimSpace(parentID) != "" {
		parts := strings.Split(parentID, "/")
		if id := safeID(parts[len(parts)-1]); id != "" {
			return id
		}
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func defaultString(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return strings.TrimSpace(s)
}

func validIsolation(s string) bool { return s == "session" || s == "worktree" }
func validMode(s string) bool      { return s == "read_only" || s == "write" }

func safeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "agent"
	}
	return out
}

func escapeTableCell(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "<br>")
	s = strings.ReplaceAll(s, "|", "\\|")
	return s
}

// buildSubReg returns the sub-agent's tool set: the named whitelist (minus
// subagent/skill meta-tools, to bar recursive nesting), or every parent tool
// except those meta-tools.
func (t *TaskTool) buildSubReg(names []string) *tool.Registry {
	return FilterRegistry(t.parentReg, names, SubagentMetaTools()...)
}

// FilterRegistry builds a sub-registry from parent: the named whitelist (empty =
// every parent tool), minus any excluded names. Used to scope what a spawned
// sub-agent — a `task` sub-agent or a subagent skill — may call, e.g. excluding
// `task` to bar recursive nesting, or restricting to a skill's allowed-tools.
func FilterRegistry(parent *tool.Registry, names []string, exclude ...string) *tool.Registry {
	ex := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		ex[e] = true
	}
	sub := tool.NewRegistry()
	src := names
	if len(src) == 0 {
		src = parent.Names()
	}
	for _, name := range src {
		if ex[name] {
			continue
		}
		if tl, ok := parent.Get(name); ok {
			sub.Add(tl)
		}
	}
	return sub
}

// runSub builds a sub-agent over subReg, runs prompt to completion emitting to
// sink, and returns its final assistant answer. Shared by the foreground and
// background paths.
func (t *TaskTool) runSub(ctx context.Context, prompt string, subReg *tool.Registry, sink event.Sink, maxSteps int) (string, error) {
	return RunSubAgent(ctx, t.prov, subReg, t.sysPrompt, prompt, Options{
		MaxSteps:      maxSteps,
		Temperature:   t.temperature,
		Pricing:       t.pricing,
		Gate:          t.gate,
		ContextWindow: t.contextWindow,
		ArchiveDir:    t.archiveDir,
	}, sink)
}

// RunSubAgent runs prompt to completion in a fresh sub-agent session over reg,
// emitting tool activity to sink, and returns the sub-agent's final assistant
// answer. It is the shared core behind the `task` tool and subagent skills: a
// caller supplies the system prompt (the task persona or the skill body), the
// tool registry (already filtered), and the run Options (model budget, gate).
func RunSubAgent(ctx context.Context, prov provider.Provider, reg *tool.Registry, sysPrompt, prompt string, opts Options, sink event.Sink) (string, error) {
	sess := NewSession(sysPrompt)
	sub := New(prov, reg, sess, opts, sink)
	if err := sub.Run(ctx, prompt); err != nil {
		return "", fmt.Errorf("sub-agent: %w", err)
	}
	// Walk the session backwards for the last assistant message with content —
	// that's the sub-agent's final answer. Intermediate assistant messages with
	// tool_calls but no text don't count.
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		m := sess.Messages[i]
		if m.Role == provider.RoleAssistant && strings.TrimSpace(m.Content) != "" {
			return m.Content, nil
		}
	}
	return "", fmt.Errorf("sub-agent finished without producing a final answer")
}

// NestedSink returns a sink that forwards a sub-agent's tool activity to the
// parent stream, nested under the tool call carried by ctx, so a frontend shows
// it beneath that call (the same nesting `task` uses). Falls back to the given
// sink when ctx carries no call context. Used by subagent skills.
func NestedSink(ctx context.Context, fallback event.Sink) event.Sink {
	parentID, parent, _, ok := CallContext(ctx)
	if !ok || parent == nil {
		return fallback
	}
	return subSinkFor(parentID, parent)
}

// subSink forwards a sub-agent's tool dispatch/result events to the parent's
// event stream, tagged with the parent task call's ID so a frontend nests them
// under it. The sub-agent's own turn/usage/text/reasoning events are dropped —
// only its tool activity (the part worth seeing live) and its final answer
// (returned by Execute) reach the parent. The forwarded call IDs are namespaced
// with the parent ID so a sub-agent call can never collide with a parent call in
// the frontend's dispatch→result matching. Falls back to Discard when there's no
// parent stream (the headless run loop, or a direct Execute in tests).
func subSink(ctx context.Context) event.Sink {
	parentID, parent, _, ok := CallContext(ctx)
	if !ok || parent == nil {
		return event.Discard
	}
	return subSinkFor(parentID, parent)
}

// subSinkFor builds the nesting sink from an already-captured parent ID + stream,
// for the background path where the job runs under a context that no longer
// carries the call context. Falls back to Discard when there's no parent stream.
func subSinkFor(parentID string, parent event.Sink) event.Sink {
	if parent == nil {
		return event.Discard
	}
	return event.FuncSink(func(e event.Event) {
		switch e.Kind {
		case event.ToolDispatch, event.ToolResult:
			e.Tool.ParentID = parentID
			e.Tool.ID = parentID + "/" + e.Tool.ID
			parent.Emit(e)
		}
	})
}
