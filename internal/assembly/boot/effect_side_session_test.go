package boot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
)

// sideScriptProvider answers the main session plainly. For the side conversation it
// first tries to write a file, as a model told to "just fix it" might, then
// answers once that was refused.
type sideScriptProvider struct {
	mu       sync.Mutex
	target   string
	sideReqs []provider.Request
	hold     chan struct{}
	finished chan struct{}
}

func (p *sideScriptProvider) Name() string { return "boot-side" }

func (p *sideScriptProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 6)
	side := len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "side question beside their main session")
	switch {
	case !side:
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "main answer"}
	default:
		p.mu.Lock()
		p.sideReqs = append(p.sideReqs, req)
		p.mu.Unlock()
		attempted := false
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				attempted = true
			}
		}
		if !attempted {
			ch <- provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{
				ID: "w1", Name: "write_file", Arguments: `{"path":` + jsonQuote(p.target) + `,"content":"changed by the side"}`,
			}}
		} else {
			ch <- provider.Chunk{Type: provider.ChunkText, Text: "side answer"}
			if p.hold != nil {
				go func() {
					defer close(ch)
					defer close(p.finished)
					select {
					case <-p.hold:
						ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 7, TotalTokens: 107}}
						ch <- provider.Chunk{Type: provider.ChunkDone}
					case <-ctx.Done():
					}
				}()
				return ch, nil
			}
		}
		ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 7, TotalTokens: 107}}
	}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func jsonQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// A side conversation, through the real Build: it answers, it cannot write, it
// leaves the main transcript and the workspace alone, it bills the session
// under its own source, and closing it leaves nothing running.
func TestEffectSideConversationIsReadOnlyEphemeralAndBilled(t *testing.T) {
	home := statedBootHome(t)
	dir := testenv.TempDir(t)
	target := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	firstHold := make(chan struct{})
	prov := &sideScriptProvider{target: target, hold: firstHold, finished: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-firstHold:
		default:
			close(firstHold)
		}
	})
	kind := "boot-side-" + t.Name()
	provider.Register(kind, func(provider.Config) (provider.Provider, error) { return prov, nil })
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"

[[providers]]
name = "test-model"
kind = "`+kind+`"
model = "x"
`)
	approveWorkspaceAt(t, home, dir)

	var mu sync.Mutex
	var usage []event.Event
	var mainEvents []event.Event
	sink := event.FuncSink(func(e event.Event) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Kind {
		case event.Usage:
			usage = append(usage, e)
		default:
			mainEvents = append(mainEvents, e)
		}
	})
	ctrl, err := Build(context.Background(), Options{Home: home, WorkspaceRoot: dir, Sink: sink})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(ctrl.Close)
	if err := ctrl.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("main turn: %v", err)
	}
	historyBefore := len(ctrl.History())
	mu.Lock()
	eventsBefore, usageBefore := len(mainEvents), len(usage)
	mu.Unlock()
	goroutinesBefore := runtime.NumGoroutine()

	id, err := ctrl.OpenSide()
	if err != nil {
		t.Fatalf("OpenSide: %v", err)
	}
	var answer string
	progress := make(chan event.Event, 64)
	done := make(chan error, 1)
	go func() {
		var askErr error
		answer, askErr = ctrl.AskSide(context.Background(), id, "what is in victim.txt?", []string{"quoted passage"}, event.FuncSink(func(e event.Event) {
			progress <- e
		}))
		done <- askErr
	}()
	for {
		select {
		case e := <-progress:
			if e.Kind == event.Usage {
				t.Fatal("usage reached the private progress sink")
			}
			if e.Kind != event.Text {
				continue
			}
			if e.Text != "side answer" {
				t.Fatalf("first delta = %q", e.Text)
			}
		case err := <-done:
			t.Fatalf("AskSide finished before streaming text: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("boot buffered side text until completion")
		}
		break
	}
	select {
	case <-prov.finished:
		t.Fatal("provider finished before the side text was delivered")
	case err := <-done:
		t.Fatalf("AskSide returned while provider was held: %v", err)
	default:
	}
	close(prov.hold)
	if err := <-done; err != nil || answer != "side answer" {
		t.Fatalf("AskSide = %q %v", answer, err)
	}

	if got, _ := os.ReadFile(target); string(got) != "original" {
		t.Fatalf("the side wrote to the workspace: %q", got)
	}
	if len(ctrl.History()) != historyBefore {
		t.Fatalf("the main transcript went from %d to %d messages", historyBefore, len(ctrl.History()))
	}
	mu.Lock()
	leaked := append([]event.Event(nil), mainEvents[eventsBefore:]...)
	side := append([]event.Event(nil), usage[usageBefore:]...)
	mu.Unlock()
	if len(leaked) != 0 {
		t.Fatalf("private side events reached the main stream: %+v", leaked)
	}
	if len(side) != 2 {
		t.Fatalf("side usage must be billed exactly once per provider call, got %d events", len(side))
	}
	for _, e := range side {
		if e.UsageSource != event.UsageSourceSide {
			t.Fatalf("a side bill is labelled %q", e.UsageSource)
		}
	}

	prov.mu.Lock()
	first := prov.sideReqs[0]
	prov.mu.Unlock()
	names := map[string]bool{}
	for _, tl := range first.Tools {
		names[tl.Name] = true
	}
	for _, writer := range []string{"write_file", "edit_file", "remember", "todo_write", "task"} {
		if names[writer] {
			t.Fatalf("the side was shown %q: %v", writer, names)
		}
	}
	if !names["read_file"] {
		t.Fatalf("the side cannot read: %v", names)
	}

	// A second question reuses only the ephemeral side history, not the parent.
	prov.hold = nil
	if answer, err := ctrl.AskSide(context.Background(), id, "follow-up", nil, nil); err != nil || answer != "side answer" {
		t.Fatalf("follow-up = %q %v", answer, err)
	}
	prov.mu.Lock()
	followup := prov.sideReqs[len(prov.sideReqs)-1]
	prov.mu.Unlock()
	remembered := false
	for _, m := range followup.Messages {
		if m.Role == provider.RoleAssistant && m.Content == "side answer" {
			remembered = true
		}
		if strings.Contains(m.Content, "main answer") {
			t.Fatal("parent history entered the side conversation")
		}
	}
	if !remembered {
		t.Fatal("a second question lost the ephemeral side history")
	}
	if len(ctrl.History()) != historyBefore {
		t.Fatal("a follow-up changed the parent history")
	}

	// Cancellation crosses the real provider boundary and releases the run.
	prov.hold, prov.finished = make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	cancelled := make(chan error, 1)
	go func() {
		_, err := ctrl.AskSide(ctx, id, "cancel this question", nil, event.FuncSink(func(e event.Event) {
			if e.Kind == event.Text {
				started <- struct{}{}
			}
		}))
		cancelled <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation question never streamed")
	}
	cancel()
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled side = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not free the side run")
	}
	select {
	case <-prov.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation left the provider running")
	}

	ctrl.CloseSide(id)
	if _, err := ctrl.AskSide(context.Background(), id, "again", nil, nil); err == nil {
		t.Fatal("a closed side still answers")
	}
	ctrl.Close()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > goroutinesBefore && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > goroutinesBefore {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines %d before, %d after closing:\n%s", goroutinesBefore, after, buf[:runtime.Stack(buf, true)])
	}
}
