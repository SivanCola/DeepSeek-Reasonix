package control

// RED: a projection settled at the controller→runner hand-off is not in the
// model's next request. Arms: A an extension refusal at agent.before_start, B a
// fresh /new session, C a failed served turn; all read the provider's bytes.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/contract/tool"
	"reasonix/internal/ext/extension"
	"reasonix/internal/ext/extension/protocol"
	"reasonix/internal/ext/skill"
	"reasonix/internal/runtime/agent"
	"reasonix/internal/state/sessionstore"
)

func redUserMessage(req provider.Request, text string) string {
	for _, m := range req.Messages {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, text) {
			return m.Content
		}
	}
	return ""
}

// arm A: the turn composes the listing, is then refused by an extension at
// agent.before_start — after runSettled, before the provider — and the next
// eligible request must still owe the listing, because the model never read it.
func TestProjectionIsStillOwedAfterAnAgentStartRefusal(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "s.jsonl")
	prov := &recordingProvider{streams: [][]provider.Chunk{{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}}
	sess := sessionstore.NewSession("sys")
	refused := 0
	client := &fakeExtClient{interceptFn: func(ev protocol.InterceptEvent, _ json.RawMessage) (protocol.InterceptResult, error) {
		if ev == protocol.EventAgentBeforeStart && refused == 0 {
			refused++
			return protocol.InterceptResult{Decision: protocol.DecisionBlock, Reason: "policy: not now"}, nil
		}
		return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
	}}
	d := newExtensionTestDispatcher(client, []extension.InterceptorPoint{extension.PointAgentBeforeStart}, nil)
	exec := agent.New(prov, tool.NewRegistry(), sess, agent.Options{SessionPath: path}, event.Discard)
	c := New(Options{
		Executor: exec, Runner: exec, Extensions: d,
		Skills:     []skill.Skill{{Name: "alpha", Description: "does the alpha thing"}},
		SessionDir: dir, SessionPath: path, Sink: event.Discard,
	})
	t.Cleanup(func() { c.Close() })

	// Fixture guard: the session does owe the listing before any turn runs, so a
	// later request without it is a lost delivery, not a listing never composed.
	if warm := c.Compose("warm"); !strings.Contains(warm, "<available-skills>") {
		t.Fatalf("the session does not owe a skills listing to begin with: %q", warm)
	}

	if err := c.RunTurn(context.Background(), "turn-one"); err == nil {
		t.Fatal("the extension did not refuse the first turn")
	}
	if n := len(prov.requests); n != 0 {
		t.Fatalf("first turn reached the provider %d time(s) despite the refusal", n)
	}

	if err := c.RunTurn(context.Background(), "turn-two"); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	last := prov.requests[len(prov.requests)-1]
	if got := redUserMessage(last, "turn-two"); !strings.Contains(got, "<available-skills>") {
		t.Fatalf("the request after the refusal carries no listing — the model is never told the skills:\n%s", got)
	}
}

// arm B: /new rotates to a session whose history is empty, so nothing carries
// the standing blocks; the first request of that session must carry them again.
func TestProjectionIsStillOwedInANewSession(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "s.jsonl")
	prov := &recordingProvider{streams: [][]provider.Chunk{{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}}
	sess := sessionstore.NewSession("sys")
	exec := agent.New(prov, tool.NewRegistry(), sess, agent.Options{SessionPath: path}, event.Discard)
	c := New(Options{
		Executor: exec, Runner: exec,
		Skills:     []skill.Skill{{Name: "alpha", Description: "does the alpha thing"}},
		SessionDir: dir, SessionPath: path, Sink: event.Discard,
	})
	t.Cleanup(func() { c.Close() })

	if err := c.RunTurn(context.Background(), "turn-one"); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if first := redUserMessage(prov.requests[len(prov.requests)-1], "turn-one"); !strings.Contains(first, "<available-skills>") {
		t.Fatalf("the first session's request carried no listing, so this arm measures nothing:\n%s", first)
	}
	if err := c.NewSession(); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := c.RunTurn(context.Background(), "turn-two"); err != nil {
		t.Fatalf("turn after /new: %v", err)
	}
	last := prov.requests[len(prov.requests)-1]
	if got := redUserMessage(last, "turn-two"); !strings.Contains(got, "<available-skills>") {
		t.Fatalf("the new session's first request carries no listing and its history is empty:\n%s", got)
	}
}

// arm C: the same recovery that a budget refusal takes — a failed served turn
// keeps the user message but strips the composed prefixes from it
// (turn_orchestrator.go:291-299 → turn_cancel.go:200). The provider failed, so
// the block never reached the model; the debt was settled at the hand-off, so
// the next request must re-owe it.
type redFailingOnceProvider struct {
	calls int
	reqs  []provider.Request
}

func (p *redFailingOnceProvider) Name() string { return "red-fail-once" }

func (p *redFailingOnceProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	call := p.calls
	p.calls++
	p.reqs = append(p.reqs, req)
	if call == 0 {
		return nil, errRedProviderFailure
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

var errRedProviderFailure = errors.New("scripted provider failure")

func TestProjectionIsStillOwedAfterAFailedServedTurn(t *testing.T) {
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "s.jsonl")
	prov := &redFailingOnceProvider{}
	sess := sessionstore.NewSession("sys")
	exec := agent.New(prov, tool.NewRegistry(), sess, agent.Options{SessionPath: path}, event.Discard)
	c := New(Options{
		Executor: exec, Runner: exec,
		Skills:     []skill.Skill{{Name: "alpha", Description: "does the alpha thing"}},
		SessionDir: dir, SessionPath: path, Sink: event.Discard,
	})
	t.Cleanup(func() { c.Close() })

	if err := c.RunTurn(context.Background(), "turn-alpha"); err == nil {
		t.Fatal("the scripted provider failure did not fail the turn")
	}
	if err := c.RunTurn(context.Background(), "turn-beta"); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	last := prov.reqs[len(prov.reqs)-1]
	if got := redUserMessage(last, "turn-beta"); !strings.Contains(got, "<available-skills>") {
		t.Fatalf("the request after the failed turn carries no listing — the model is never told the skills:\n%s", got)
	}
}
