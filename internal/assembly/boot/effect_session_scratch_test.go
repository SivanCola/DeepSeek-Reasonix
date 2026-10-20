package boot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/state/sessiontemp"
)

// scratchProvider plays a model that verifies in the scratch directory it was
// told about, spelling it exactly as the block says, one tool call per round.
type scratchProvider struct {
	mu   sync.Mutex
	reqs []provider.Request
}

func (p *scratchProvider) Name() string { return "boot-effect-scratch" }

func (p *scratchProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	round := len(p.reqs)
	told := round > 1 || strings.Contains(blockOf(lastUserContent(req), "session-scratch"), "`$TMPDIR/<name>`")
	p.mu.Unlock()

	call := func(id, name string, args map[string]string) provider.Chunk {
		raw, _ := json.Marshal(args)
		return provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: id, Name: name, Arguments: string(raw)}}
	}
	ch := make(chan provider.Chunk, 2)
	switch {
	case !told:
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "no scratch block was projected"}
	case round == 1:
		ch <- call("w1", "write_file", map[string]string{"path": "$TMPDIR/check/probe_test.py", "content": "assert True\n"})
	case round == 2:
		ch <- call("b1", "bash", map[string]string{"command": `cd "$TMPDIR/check" && cat probe_test.py`})
	case round == 3:
		ch <- call("b2", "bash", map[string]string{"command": `rm -rf "$TMPDIR/check"`})
	case round == 4:
		ch <- call("b3", "bash", map[string]string{"command": `scratch=$(mktemp -d); rm -rf "$scratch"`})
	default:
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "done"}
	}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func lastUserContent(req provider.Request) string {
	for _, m := range slices.Backward(req.Messages) {
		if m.Role == provider.RoleUser {
			return m.Content
		}
	}
	return ""
}

func (p *scratchProvider) toolResult(round int, id string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if round >= len(p.reqs) {
		return ""
	}
	for _, m := range p.reqs[round].Messages {
		if m.Role == provider.RoleTool && m.ToolCallID == id {
			return m.Content
		}
	}
	return ""
}

// TestEffectSessionScratchIsReachableByItsSpelling pins what the scratch block
// buys at the tool boundary: the one spelling it gives, `$TMPDIR/<name>`, reaches
// the same directory through write_file and through bash, the host path never
// reaches the model, and the deletes the gate exists to refuse are still
// refused for the scratch directory itself.
func TestEffectSessionScratchIsReachableByItsSpelling(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	rec := &scratchProvider{}
	resolver := &provider.StaticResolver{
		Descriptors: []provider.Descriptor{{Ref: "test-model/x", Model: "x", Default: true}},
		Providers:   map[string]provider.Provider{"test-model/x": rec},
	}
	writeUserConfig(t, "[sandbox]\nbash = \"off\"\n")
	writeFile(t, dir, "reasonix.toml", "\n[agent]\nsystem_prompt = \"BASE\"\n")
	ctrl, err := Build(context.Background(), Options{Sink: event.Discard, Model: "test-model/x", ProviderResolver: resolver, resolvedShell: pinnedEffectShell()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()
	ctrl.SetSessionPath(filepath.Join(dir, ".reasonix", "sessions", "scratch.jsonl"))
	ctrl.SetBypass(true)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := ctrl.Run(ctx, "verify in scratch"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rec.mu.Lock()
	first := rec.reqs[0]
	rec.mu.Unlock()
	scratch := ctrl.SessionTemp().Dir()
	if scratch == "" {
		t.Fatal("the session has no scratch directory")
	}
	for _, m := range first.Messages {
		if strings.Contains(m.Content, scratch) || strings.Contains(m.Content, sessiontemp.Prefix()) {
			t.Fatalf("the host scratch path reached the model in a %s message", m.Role)
		}
	}
	if got := rec.toolResult(1, "w1"); strings.Contains(got, "refused") {
		t.Fatalf("write_file to $TMPDIR/check was refused: %s", got)
	}
	if _, err := os.Stat(filepath.Join(scratch, "check", "probe_test.py")); err != nil {
		t.Fatalf("write_file did not land in the session's directory: %v", err)
	}
	if got := rec.toolResult(2, "b1"); strings.Contains(got, "refused") || !strings.Contains(got, "assert True") {
		t.Fatalf(`bash did not reach the file write_file wrote through "$TMPDIR/check": %s`, got)
	}
	if got := rec.toolResult(3, "b2"); !strings.Contains(got, "refused") {
		t.Fatalf("a recursive delete through $TMPDIR was not refused: %s", got)
	}
	if got := rec.toolResult(4, "b3"); !strings.Contains(got, "refused") {
		t.Fatalf("a delete through a mktemp variable was not refused: %s", got)
	}
	if _, err := os.Stat(filepath.Join(scratch, "check", "probe_test.py")); err != nil {
		t.Fatalf("a refused delete still removed the scratch file: %v", err)
	}
}
