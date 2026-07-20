package control

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/permission"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

type recoveryWriteTool struct {
	name     string
	readOnly bool
	mu       sync.Mutex
	runs     int
	failOnce bool
	failed   bool
}

func (t *recoveryWriteTool) Name() string            { return t.name }
func (t *recoveryWriteTool) Description() string     { return "test tool" }
func (t *recoveryWriteTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *recoveryWriteTool) ReadOnly() bool          { return t.readOnly }
func (t *recoveryWriteTool) Execute(context.Context, json.RawMessage) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runs++
	if t.failOnce && !t.failed {
		t.failed = true
		return "FAIL", errRecoveryTestFail
	}
	return "ok", nil
}

type recoveryTestFailError struct{}

func (recoveryTestFailError) Error() string { return "exit status 1" }

var errRecoveryTestFail = recoveryTestFailError{}

func TestRecoveryCheckpointBlocksStrategyChangeUntilContinue(t *testing.T) {
	bash := &recoveryWriteTool{name: "bash", failOnce: true}
	write := &recoveryWriteTool{name: "write_file"}
	reg := tool.NewRegistry()
	reg.Add(bash)
	reg.Add(write)

	prov := &recordingProvider{streams: [][]provider.Chunk{
		{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "1", Name: "bash", Arguments: `{"command":"go test ./..."}`}}},
		{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "2", Name: "write_file", Arguments: `{"path":"a.go","content":"x"}`}}},
		{{Type: provider.ChunkText, Text: "done"}},
	}}

	sess := agent.NewSession("sys")
	ag := agent.New(prov, reg, sess, agent.Options{MaxSteps: 6}, event.Discard)
	c := New(Options{
		Runner:                    ag,
		Executor:                  ag,
		Policy:                    permission.Policy{Mode: permission.Allow},
		RecoveryCheckpointEnabled: true,
	})
	c.SetToolApprovalMode(ToolApprovalAuto)
	c.EnableInteractiveApproval()

	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			c.mu.Lock()
			gate := c.recoveryGate
			c.mu.Unlock()
			if gate != nil {
				snap := gate.Snapshot()
				for _, st := range snap.Tasks {
					if st != nil && st.ApprovalID != "" {
						_ = c.ResolveRecovery(st.ApprovalID, agent.RecoveryActionContinue, "")
						return
					}
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	if err := c.Run(context.Background(), "test then fix"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-done

	if bash.runs < 1 {
		t.Fatalf("expected failing bash to run")
	}
	if write.runs != 1 {
		t.Fatalf("write runs = %d, want 1 after continue", write.runs)
	}
}

func TestRecoveryReviseBlocksWrite(t *testing.T) {
	bash := &recoveryWriteTool{name: "bash", failOnce: true}
	write := &recoveryWriteTool{name: "write_file"}
	reg := tool.NewRegistry()
	reg.Add(bash)
	reg.Add(write)

	prov := &recordingProvider{streams: [][]provider.Chunk{
		{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "1", Name: "bash", Arguments: `{"command":"go test ./pkg"}`}}},
		{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "2", Name: "write_file", Arguments: `{"path":"a.go","content":"x"}`}}},
		{{Type: provider.ChunkText, Text: "done"}},
	}}

	sess := agent.NewSession("sys")
	ag := agent.New(prov, reg, sess, agent.Options{MaxSteps: 6}, event.Discard)
	c := New(Options{
		Runner:                    ag,
		Executor:                  ag,
		Policy:                    permission.Policy{Mode: permission.Allow},
		RecoveryCheckpointEnabled: true,
	})
	c.SetToolApprovalMode(ToolApprovalAuto)
	c.EnableInteractiveApproval()

	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			c.mu.Lock()
			gate := c.recoveryGate
			c.mu.Unlock()
			if gate != nil {
				snap := gate.Snapshot()
				for _, st := range snap.Tasks {
					if st != nil && st.ApprovalID != "" {
						_ = c.ResolveRecovery(st.ApprovalID, agent.RecoveryActionRevise, "only edit tests")
						return
					}
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	if err := c.Run(context.Background(), "test then fix"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if write.runs != 0 {
		t.Fatalf("write must not run after revise, runs=%d", write.runs)
	}
	if len(prov.requests) == 0 {
		t.Fatal("expected provider requests")
	}
	last := requestMessagesText(prov.requests[len(prov.requests)-1].Messages)
	if got := strings.Count(last, "only edit tests"); got != 1 {
		t.Fatalf("revision feedback occurrences = %d, want exactly one\n%s", got, last)
	}
}

func TestRecoveryInactiveUnderYolo(t *testing.T) {
	bash := &recoveryWriteTool{name: "bash", failOnce: true}
	write := &recoveryWriteTool{name: "write_file"}
	reg := tool.NewRegistry()
	reg.Add(bash)
	reg.Add(write)
	prov := &recordingProvider{streams: [][]provider.Chunk{
		{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "1", Name: "bash", Arguments: `{"command":"go test ./..."}`}}},
		{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "2", Name: "write_file", Arguments: `{"path":"a.go","content":"x"}`}}},
		{{Type: provider.ChunkText, Text: "done"}},
	}}
	sess := agent.NewSession("sys")
	ag := agent.New(prov, reg, sess, agent.Options{MaxSteps: 6}, event.Discard)
	c := New(Options{
		Runner:                    ag,
		Executor:                  ag,
		Policy:                    permission.Policy{Mode: permission.Allow},
		RecoveryCheckpointEnabled: true,
	})
	c.SetToolApprovalMode(ToolApprovalYolo)
	c.EnableInteractiveApproval()

	if err := c.Run(context.Background(), "test then fix"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if write.runs != 1 {
		t.Fatalf("yolo should run write without recovery pause, runs=%d", write.runs)
	}
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate != nil {
		if st := gate.Snapshot().Tasks["root"]; st != nil && st.Failure != nil {
			t.Fatalf("yolo must not arm recovery failure: %+v", st)
		}
	}
}

func TestRecoveryHeadlessBlocksInsteadOfWaiting(t *testing.T) {
	bash := &recoveryWriteTool{name: "bash", failOnce: true}
	write := &recoveryWriteTool{name: "write_file"}
	reg := tool.NewRegistry()
	reg.Add(bash)
	reg.Add(write)
	prov := &recordingProvider{streams: [][]provider.Chunk{
		{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "1", Name: "bash", Arguments: `{"command":"go test ./..."}`}}},
		{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "2", Name: "write_file", Arguments: `{"path":"a.go","content":"x"}`}}},
		{{Type: provider.ChunkText, Text: "reported blocker"}},
	}}
	sess := agent.NewSession("sys")
	ag := agent.New(prov, reg, sess, agent.Options{MaxSteps: 6}, event.Discard)
	c := New(Options{
		Runner:                    ag,
		Executor:                  ag,
		Policy:                    permission.Policy{Mode: permission.Allow},
		RecoveryCheckpointEnabled: true,
		RecoveryHeadless:          true,
	})
	c.SetToolApprovalMode(ToolApprovalAuto)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Run(ctx, "test then fix"); err != nil {
		t.Fatalf("headless Run: %v", err)
	}
	if write.runs != 0 {
		t.Fatalf("headless recovery must block the write, runs=%d", write.runs)
	}
	if got := requestMessagesText(prov.requests[len(prov.requests)-1].Messages); !strings.Contains(got, "no decision channel") {
		t.Fatalf("final provider request lacks structured blocker:\n%s", got)
	}
}
