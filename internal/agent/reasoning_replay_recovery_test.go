package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/provider/anthropic"
)

type strictNoWarningReasoningProvider struct{ *testutil.MockProvider }

func (p strictNoWarningReasoningProvider) RequiresToolCallReasoning() bool      { return true }
func (p strictNoWarningReasoningProvider) WarnOnMissingToolCallReasoning() bool { return false }

type strictFallbackReasoningProvider struct {
	*testutil.MockProvider
	mu       sync.Mutex
	fallback []bool
}

func (p *strictFallbackReasoningProvider) RequiresToolCallReasoning() bool      { return true }
func (p *strictFallbackReasoningProvider) WarnOnMissingToolCallReasoning() bool { return true }
func (p *strictFallbackReasoningProvider) SupportsMissingReasoningFallback() bool {
	return true
}
func (p *strictFallbackReasoningProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.fallback = append(p.fallback, provider.MissingReasoningFallbackFromContext(ctx))
	p.mu.Unlock()
	return p.MockProvider.Stream(ctx, req)
}
func (p *strictFallbackReasoningProvider) fallbackCalls() []bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bool(nil), p.fallback...)
}

func TestStrictMissingReasoningCircuitSuppressesDuplicateRetryAcrossRuns(t *testing.T) {
	stateDir := t.TempDir()
	call := provider.ToolCall{ID: "c1", Name: "echo", Arguments: `{"text":"hi"}`}
	seedProvider := strictAssistantReasoningProvider{testutil.NewMock("strict-replay")}
	fingerprint := provider.MissingToolCallReasoningWarningFingerprint(seedProvider)
	if !newMissingReasoningWarnState(stateDir).claim(fingerprint) {
		t.Fatal("failed to seed a persisted incident from the previous behavior")
	}

	firstProvider := testutil.NewMock("strict-replay",
		testutil.Turn{ToolCalls: []provider.ToolCall{call}},
		testutil.Turn{ToolCalls: []provider.ToolCall{call}},
	)
	firstSink := &recordSink{}
	first := New(strictAssistantReasoningProvider{firstProvider}, echoRegistry(), NewSession(""), Options{
		MissingReasoningWarnStateDir: stateDir,
	}, firstSink)
	var replayErr *ReasoningReplayError
	if err := first.Run(withNoClosedLoop(context.Background()), "go"); !errors.As(err, &replayErr) {
		t.Fatalf("first Run error = %v, want ReasoningReplayError", err)
	}
	if message := replayErr.Error(); !strings.Contains(message, "exhausted its safe automatic recovery") || !strings.Contains(message, "switch provider or protocol") {
		t.Fatalf("ReasoningReplayError = %q, want exhausted recovery and actionable provider guidance", message)
	}
	if got := firstSink.recoveryCount(event.ProtocolRecoveryMissingReasoningRetryAttempted); got != 0 {
		t.Fatalf("active circuit retries = %d, want 0", got)
	}
	if got := firstProvider.CallCount(); got != 1 {
		t.Fatalf("active circuit provider calls = %d, want one terminal probe", got)
	}

	secondProvider := testutil.NewMock("strict-replay",
		testutil.Turn{ToolCalls: []provider.ToolCall{call}},
	)
	secondSink := &recordSink{}
	second := New(strictAssistantReasoningProvider{secondProvider}, echoRegistry(), NewSession(""), Options{
		MissingReasoningWarnStateDir: stateDir,
	}, secondSink)
	if err := second.Run(withNoClosedLoop(context.Background()), "go"); !errors.As(err, &replayErr) {
		t.Fatalf("second Run error = %v, want ReasoningReplayError", err)
	}
	if got := secondSink.recoveryCount(event.ProtocolRecoveryMissingReasoningRetryAttempted); got != 0 {
		t.Fatalf("second Run retries = %d, want circuit suppression", got)
	}
	requests := secondProvider.Requests()
	if len(requests) != 1 {
		t.Fatalf("strict circuit requests = %d, want one terminal probe", len(requests))
	}
}

func TestStrictMissingReasoningUsesProviderFallbackBeforeToolExecution(t *testing.T) {
	stateDir := t.TempDir()
	call := func(id, text string) provider.ToolCall {
		return provider.ToolCall{ID: id, Name: "echo", Arguments: `{"text":"` + text + `"}`}
	}
	mock := testutil.NewMock("strict-fallback",
		testutil.Turn{ToolCalls: []provider.ToolCall{call("bad-1", "must not run 1")}, Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11, RequestCount: 1}},
		testutil.Turn{ToolCalls: []provider.ToolCall{call("bad-2", "must not run 2")}, Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11, RequestCount: 1}},
		testutil.Turn{ToolCalls: []provider.ToolCall{call("safe", "hi")}, Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11, RequestCount: 1}},
		testutil.Turn{Text: "done", Usage: &provider.Usage{PromptTokens: 12, CompletionTokens: 1, TotalTokens: 13, RequestCount: 1}},
	)
	prov := &strictFallbackReasoningProvider{MockProvider: mock}
	sink := &recordSink{}
	agent := New(prov, echoRegistry(), NewSession(""), Options{MissingReasoningWarnStateDir: stateDir}, sink)

	if err := agent.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := prov.fallbackCalls(), []bool{false, false, true, true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback calls = %v, want %v", got, want)
	}
	requests := mock.Requests()
	if len(requests) != 4 || !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatalf("requests = %d, want exact replay then fallback tool/final", len(requests))
	}
	results := sink.kinds(event.ToolResult)
	if len(results) != 1 || results[0].Tool.ID != "safe" {
		t.Fatalf("tool results = %+v, want only the fallback tool", results)
	}
	for _, message := range agent.Session().Snapshot() {
		for _, toolCall := range message.ToolCalls {
			if toolCall.ID == "bad-1" || toolCall.ID == "bad-2" {
				t.Fatalf("discarded tool call leaked into session history: %+v", toolCall)
			}
		}
	}
	usages := sink.kinds(event.Usage)
	if len(usages) < 2 || usages[0].Usage == nil || usages[0].Usage.RequestCount != 3 || usages[0].Usage.TotalTokens != 33 {
		t.Fatalf("fallback usage = %+v, want first round to bill exactly three requests/33 tokens", usages)
	}
	retries := sink.kinds(event.Retrying)
	if len(retries) != 2 || retries[0].RetryScope != event.RetryScopeProtocol || retries[1].RetryScope != event.RetryScopeProtocol {
		t.Fatalf("protocol retry events = %+v, want exact retry and fallback", retries)
	}
}

func TestStrictMissingReasoningOpenCircuitStartsNextSessionInFallback(t *testing.T) {
	stateDir := t.TempDir()
	seed := &strictFallbackReasoningProvider{MockProvider: testutil.NewMock("strict-fallback")}
	fingerprint := provider.MissingToolCallReasoningWarningFingerprint(seed)
	state := newMissingReasoningWarnState(stateDir)
	if !state.claim(fingerprint) || !state.openFallbackAt(fingerprint, time.Now()) {
		t.Fatal("failed to seed active circuit")
	}
	statePath := filepath.Join(stateDir, missingReasoningWarnStateFilename)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	call := provider.ToolCall{ID: "safe", Name: "echo", Arguments: `{"text":"hi"}`}
	mock := testutil.NewMock("strict-fallback",
		testutil.Turn{ToolCalls: []provider.ToolCall{call}},
		testutil.Turn{Text: "done"},
	)
	prov := &strictFallbackReasoningProvider{MockProvider: mock}
	sink := &recordSink{}
	agent := New(prov, echoRegistry(), NewSession(""), Options{MissingReasoningWarnStateDir: stateDir}, sink)

	if err := agent.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := prov.fallbackCalls(), []bool{true, true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback calls = %v, want direct circuit reuse %v", got, want)
	}
	if got := sink.recoveryCount(event.ProtocolRecoveryMissingReasoningRetryAttempted); got != 0 {
		t.Fatalf("exact retries = %d, want 0 after circuit opened", got)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("intentional fallback responses rewrote the missing-reasoning incident")
	}
}

func TestStrictMissingReasoningHalfOpenFailureSkipsExactReplayAndBacksOff(t *testing.T) {
	stateDir := t.TempDir()
	seed := &strictFallbackReasoningProvider{MockProvider: testutil.NewMock("strict-fallback")}
	fingerprint := provider.MissingToolCallReasoningWarningFingerprint(seed)
	state := newMissingReasoningWarnState(stateDir)
	openedAt := time.Now().Add(-missingReasoningFallbackBackoffs[0] - 2*time.Second)
	if !state.claimAt(fingerprint, openedAt) || !state.openFallbackAt(fingerprint, openedAt.Add(time.Second)) {
		t.Fatal("failed to seed due half-open circuit")
	}
	call := func(id, text string) provider.ToolCall {
		return provider.ToolCall{ID: id, Name: "echo", Arguments: `{"text":"` + text + `"}`}
	}
	mock := testutil.NewMock("strict-fallback",
		testutil.Turn{ToolCalls: []provider.ToolCall{call("probe-bad", "must not run")}},
		testutil.Turn{ToolCalls: []provider.ToolCall{call("fallback-safe", "hi")}},
		testutil.Turn{Text: "done"},
	)
	prov := &strictFallbackReasoningProvider{MockProvider: mock}
	sink := &recordSink{}
	agent := New(prov, echoRegistry(), NewSession(""), Options{MissingReasoningWarnStateDir: stateDir}, sink)

	if err := agent.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := prov.fallbackCalls(), []bool{false, true, true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("half-open calls = %v, want normal probe then direct fallback %v", got, want)
	}
	if got := sink.recoveryCount(event.ProtocolRecoveryMissingReasoningRetryAttempted); got != 0 {
		t.Fatalf("half-open exact retries = %d, want 0", got)
	}
	results := sink.kinds(event.ToolResult)
	if len(results) != 1 || results[0].Tool.ID != "fallback-safe" {
		t.Fatalf("tool results = %+v, want only fallback-safe", results)
	}
	incidents, err := state.load(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	incident := incidents[fingerprint]
	if incident.FallbackLevel != 2 {
		t.Fatalf("fallback level = %d, want 2 after failed half-open probe", incident.FallbackLevel)
	}
	if got := time.Unix(0, incident.NextProbeAtUnixNano).Sub(time.Unix(0, incident.FallbackAtUnixNano)); got != missingReasoningFallbackBackoffs[1] {
		t.Fatalf("next probe delay = %v, want %v", got, missingReasoningFallbackBackoffs[1])
	}
}

func TestStrictMissingReasoningHalfOpenClosesAfterThreeHealthyToolRounds(t *testing.T) {
	stateDir := t.TempDir()
	seed := &strictFallbackReasoningProvider{MockProvider: testutil.NewMock("strict-fallback")}
	fingerprint := provider.MissingToolCallReasoningWarningFingerprint(seed)
	state := newMissingReasoningWarnState(stateDir)
	openedAt := time.Now().Add(-missingReasoningFallbackBackoffs[0] - 2*time.Second)
	if !state.claimAt(fingerprint, openedAt) || !state.openFallbackAt(fingerprint, openedAt.Add(time.Second)) {
		t.Fatal("failed to seed due half-open circuit")
	}
	call := func(id string) provider.ToolCall {
		return provider.ToolCall{ID: id, Name: "echo", Arguments: `{"text":"hi"}`}
	}
	mock := testutil.NewMock("strict-fallback",
		testutil.Turn{Reasoning: "healthy one", ToolCalls: []provider.ToolCall{call("h1")}},
		testutil.Turn{Reasoning: "healthy two", ToolCalls: []provider.ToolCall{call("h2")}},
		testutil.Turn{Reasoning: "healthy three", ToolCalls: []provider.ToolCall{call("h3")}},
		testutil.Turn{Text: "done"},
	)
	prov := &strictFallbackReasoningProvider{MockProvider: mock}
	agent := New(prov, echoRegistry(), NewSession(""), Options{MissingReasoningWarnStateDir: stateDir}, event.Discard)

	if err := agent.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := prov.fallbackCalls(), []bool{false, false, false, false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("healthy half-open calls = %v, want normal thinking %v", got, want)
	}
	if state.fallbackActiveAt(fingerprint, time.Now()) {
		t.Fatal("three healthy half-open rounds did not close fallback circuit")
	}
	if got := state.claimRecoveryModeAt(fingerprint, time.Now()).Mode; got != missingReasoningRecoveryNormal {
		t.Fatalf("post-recovery mode = %v, want normal", got)
	}
}

func TestStrictNoWarningReplayDoesNotLeakSpeculativeEvents(t *testing.T) {
	missingTurn := func(id string) testutil.Turn {
		call := provider.ToolCall{ID: id, Name: "echo", Arguments: `{"text":"must not run"}`}
		return testutil.Turn{Chunks: []provider.Chunk{
			{Type: provider.ChunkText, Text: "speculative"},
			{Type: provider.ChunkToolCallStart, ToolCall: &call},
			{Type: provider.ChunkToolCall, ToolCall: &call},
			{Type: provider.ChunkDone},
		}}
	}
	providerMock := testutil.NewMock("strict-no-warning", missingTurn("c1"), missingTurn("c2"))
	sink := &recordSink{}
	agent := New(strictNoWarningReasoningProvider{providerMock}, echoRegistry(), NewSession(""), Options{}, sink)

	var replayErr *ReasoningReplayError
	if err := agent.Run(withNoClosedLoop(context.Background()), "go"); !errors.As(err, &replayErr) {
		t.Fatalf("Run error = %v, want ReasoningReplayError", err)
	}
	if got := providerMock.CallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want malformed turn plus one exact retry", got)
	}
	for _, kind := range []event.Kind{event.ToolDispatch, event.ToolResult, event.Text, event.Message} {
		if got := len(sink.kinds(kind)); got != 0 {
			t.Fatalf("speculative %v events = %d, want 0", kind, got)
		}
	}
}

func thinkingReplay400Error() error {
	return provider.ParseReasoningReplayError(&provider.APIError{
		Provider: "strict-replay", Status: 400,
		Body: `{"error":{"message":"The ` + "`content[].thinking`" + ` in the thinking mode must be passed back to the API"}}`,
	})
}

func reasoningReplaySeededSession() *Session {
	session := NewSession("system")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "earlier"})
	session.Add(provider.Message{Role: provider.RoleAssistant, Content: "old answer", ReasoningContent: "stale thinking"})
	return session
}

func TestReasoningReplay400RepairsProjectionAndRetriesOnce(t *testing.T) {
	mp := testutil.NewMock("strict-replay",
		testutil.ErrorTurn(thinkingReplay400Error()),
		testutil.Turn{Text: "done"},
		testutil.Turn{Text: "again done"},
	)
	sink := &recordSink{}
	session := reasoningReplaySeededSession()
	a := New(strictAssistantReasoningProvider{mp}, echoRegistry(), session, Options{}, sink)

	if err := a.Run(withNoClosedLoop(context.Background()), "next"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mp.CallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want rejected attempt plus one repair retry", got)
	}
	requests := mp.Requests()
	var firstReasoning, secondReasoning int
	for _, m := range requests[0].Messages {
		if m.ReasoningContent != "" {
			firstReasoning++
		}
	}
	for _, m := range requests[1].Messages {
		if m.ReasoningContent != "" {
			secondReasoning++
		}
	}
	if firstReasoning != 1 || secondReasoning != 0 {
		t.Fatalf("reasoning in attempts = %d then %d, want the repair retry stripped", firstReasoning, secondReasoning)
	}
	// The frozen request may change only in Messages; everything else is
	// byte-identical to the rejected attempt.
	strippedTools := requests[1]
	strippedTools.Messages = requests[0].Messages
	if !reflect.DeepEqual(requests[0], strippedTools) {
		t.Fatalf("repair retry changed more than Messages:\nfirst=%+v\nretry=%+v", requests[0], requests[1])
	}
	// Canonical history is never modified by the provider-visible projection.
	for _, m := range session.Snapshot() {
		if m.Role == provider.RoleAssistant && m.Content == "old answer" && m.ReasoningContent != "stale thinking" {
			t.Fatalf("canonical history lost its reasoning: %+v", m)
		}
	}
	if got := sink.recoveryCount(event.ProtocolRecoveryReasoningReplay400Detected); got != 1 {
		t.Fatalf("reasoning_replay_400_detected audits = %d, want 1", got)
	}
	if got := sink.recoveryCount(event.ProtocolRecoveryReasoningReplay400Recovered); got != 1 {
		t.Fatalf("reasoning_replay_400_recovered audits = %d, want 1", got)
	}
	var repairNotices int
	for _, e := range sink.kinds(event.Notice) {
		if e.Code == event.NoticeCodeReasoningReplayRepair {
			repairNotices++
			if e.Level != event.LevelWarn {
				t.Fatalf("repair notice level = %v, want warn", e.Level)
			}
		}
	}
	if repairNotices != 1 {
		t.Fatalf("repair notices = %d, want 1", repairNotices)
	}

	// The strong projection stays active for the rest of the conversation.
	if err := a.Run(withNoClosedLoop(context.Background()), "again"); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := mp.CallCount(); got != 3 {
		t.Fatalf("provider calls = %d, want no fresh 400 on the next run", got)
	}
	for _, m := range mp.Requests()[2].Messages {
		if m.ReasoningContent != "" {
			t.Fatalf("later request still carries reasoning under strong projection: %+v", m)
		}
	}
}

func TestReasoningReplay400RepairExhaustionStaysTerminal(t *testing.T) {
	mp := testutil.NewMock("strict-replay",
		testutil.ErrorTurn(thinkingReplay400Error()),
		testutil.ErrorTurn(thinkingReplay400Error()),
		testutil.Turn{Text: "unreachable"},
	)
	sink := &recordSink{}
	a := New(strictAssistantReasoningProvider{mp}, echoRegistry(), reasoningReplaySeededSession(), Options{}, sink)

	err := a.Run(withNoClosedLoop(context.Background()), "next")
	var replayErr *provider.ReasoningReplayError
	if !errors.As(err, &replayErr) {
		t.Fatalf("Run error = %v, want ReasoningReplayError", err)
	}
	if got := mp.CallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want exactly one repair retry", got)
	}
	if got := sink.recoveryCount(event.ProtocolRecoveryReasoningReplay400Detected); got != 1 {
		t.Fatalf("reasoning_replay_400_detected audits = %d, want 1", got)
	}
	if got := sink.recoveryCount(event.ProtocolRecoveryReasoningReplay400Recovered); got != 0 {
		t.Fatalf("reasoning_replay_400_recovered audits = %d, want 0 after a failed repair", got)
	}
}

func TestNonReplay400DoesNotTriggerReasoningRepair(t *testing.T) {
	mp := testutil.NewMock("strict-replay",
		testutil.ErrorTurn(&provider.APIError{Provider: "strict-replay", Status: 400, Body: `{"error":{"message":"invalid request: unknown tool"}}`}),
		testutil.Turn{Text: "unreachable"},
	)
	sink := &recordSink{}
	a := New(strictAssistantReasoningProvider{mp}, echoRegistry(), reasoningReplaySeededSession(), Options{}, sink)

	err := a.Run(withNoClosedLoop(context.Background()), "next")
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Run error = %v, want the raw APIError", err)
	}
	if replayErr := provider.AsReasoningReplayError(err); replayErr != nil {
		t.Fatalf("unrelated 400 misclassified as reasoning replay: %v", replayErr)
	}
	if got := mp.CallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want no retry for an unrelated 400", got)
	}
	if got := sink.recoveryCount(event.ProtocolRecoveryReasoningReplay400Detected); got != 0 {
		t.Fatalf("reasoning_replay_400_detected audits = %d, want 0", got)
	}
}

// TestStrictMissingReasoningFallbackStreamsTextLive drives the real
// Anthropic-adapter wire shape for #9750: once the disabled-thinking fallback
// owns the session its responses contain no reasoning event, so a
// reasoning-aware buffer could only release at commit time. The server holds
// the final answer's stream open after the first text delta; the text must
// reach the sink while the response is still in flight.
func TestStrictMissingReasoningFallbackStreamsTextLive(t *testing.T) {
	held := make(chan struct{})
	release := make(chan struct{})
	var heldOnce, releaseOnce sync.Once

	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		i := len(bodies) - 1
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if i < 3 {
			_, _ = io.WriteString(w, missingReasoningToolSSE)
			return
		}
		_, _ = io.WriteString(w, `data: {"type":"message_start","message":{"usage":{"input_tokens":20}}}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"streamed live"}}`+"\n\n")
		w.(http.Flusher).Flush()
		heldOnce.Do(func() { close(held) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	t.Cleanup(srv.Close)
	// Runs before srv.Close (LIFO) so a failed test never parks server close
	// behind the held final response.
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	prov, err := anthropic.New(provider.Config{
		Name: "deepseek-anthropic", BaseURL: srv.URL, Model: "deepseek-v4-flash", APIKey: "k",
		Extra: map[string]any{"reasoning_protocol": "deepseek", "thinking": "enabled"},
	})
	if err != nil {
		t.Fatalf("New provider: %v", err)
	}
	sink := &textSignalSink{recordSink: &recordSink{}, textSeen: make(chan struct{})}
	a := New(prov, echoRegistry(), NewSession(""), Options{MissingReasoningWarnStateDir: t.TempDir()}, sink)
	done := make(chan error, 1)
	go func() { done <- a.Run(withNoClosedLoop(context.Background()), "go") }()

	select {
	case <-held:
	case err := <-done:
		t.Fatalf("Run finished before the fallback final answer was held: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("provider never reached the held final answer")
	}
	select {
	case <-sink.textSeen:
	case err := <-done:
		t.Fatalf("Run completed before the held fallback response was released: %v", err)
	case <-time.After(2 * time.Second):
		releaseOnce.Do(func() { close(release) })
		if err := <-done; err != nil {
			t.Logf("Run after release: %v", err)
		}
		t.Fatal("fallback text stayed buffered until the response completed")
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 4 {
		t.Fatalf("HTTP requests = %d, want malformed, exact replay, fallback, final", len(bodies))
	}
	for _, i := range []int{2, 3} {
		if !bytes.Contains(bodies[i], []byte(`"thinking":{"type":"disabled"}`)) {
			t.Fatalf("request %d did not run under the disabled-thinking fallback: %s", i+1, bodies[i])
		}
	}
}
