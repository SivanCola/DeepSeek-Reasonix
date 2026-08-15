package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

type scriptedBudgetProvider struct {
	mu     sync.Mutex
	policy provider.ContextBudgetPolicy
	errs   []error
	reqs   []provider.Request
	texts  []string
}

func (p *scriptedBudgetProvider) Name() string { return "scripted-budget" }
func (p *scriptedBudgetProvider) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	return p.policy
}
func (p *scriptedBudgetProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	idx := len(p.reqs) - 1
	var err error
	if idx < len(p.errs) {
		err = p.errs[idx]
	}
	text := "ok"
	if idx < len(p.texts) && p.texts[idx] != "" {
		text = p.texts[idx]
	}
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: text}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func issue8909Limit() *provider.ContextLimitError {
	return &provider.ContextLimitError{
		APIError:         &provider.APIError{Provider: "p", Status: 400, Body: "context"},
		WindowTokens:     1_048_576,
		RequestedTokens:  1_165_351,
		PromptTokens:     810_882,
		CompletionTokens: 354_469,
	}
}

func newBudgetAgent(t *testing.T, p provider.Provider) *Agent {
	t.Helper()
	sess := NewSession("")
	sess.Replace([]provider.Message{{Role: provider.RoleUser, Content: "continue"}})
	return New(p, tool.NewRegistry(), sess, Options{ContextWindow: 1_048_576, CompactRatio: 2, MaxOutputTokens: 0}, event.Discard)
}

func TestContextLimitRecoveryChangesOnlyOutputField(t *testing.T) {
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{
			WindowMode: provider.ContextWindowShared, AutoOutputTokens: 384_000,
			MaxOutputTokens: 384_000, LimitMode: provider.OutputLimitOmitWhenSafe,
		},
		errs: []error{issue8909Limit(), nil},
	}
	a := newBudgetAgent(t, prov)
	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err != nil {
		t.Fatalf("recovery failed: %v", got.err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(prov.reqs))
	}
	if requestWireDigest(prov.reqs[0]) != requestWireDigest(prov.reqs[1]) {
		t.Fatalf("messages/tools digest changed across recovery")
	}
	if prov.reqs[1].MaxTokens != 229_502 {
		t.Fatalf("retry MaxTokens = %d, want 229502", prov.reqs[1].MaxTokens)
	}
	if a.lastAdmission().LastRecovery != contextRecoveryLearnedRetry {
		t.Fatalf("last recovery = %s", a.lastAdmission().LastRecovery)
	}
	if len(a.sess.conversation.Snapshot()) != 1 {
		t.Fatalf("recovery wrote extra turns: %+v", a.sess.conversation.Snapshot())
	}
}

func TestContextLimitRecoveryStopsAfterBudgetAndCompact(t *testing.T) {
	limit := issue8909Limit()
	limit.PromptTokens = 1_040_000
	limit.CompletionTokens = 20_000
	limit.RequestedTokens = 1_060_000
	prov := &scriptedBudgetProvider{
		policy: provider.ContextBudgetPolicy{
			WindowMode: provider.ContextWindowShared, AutoOutputTokens: 384_000,
			LimitMode: provider.OutputLimitOmitWhenSafe,
		},
		errs: []error{limit, limit, limit},
	}
	a := newBudgetAgent(t, prov)
	got := a.streamWithSamplingRecovery(context.Background(), 1)
	if got.err == nil {
		t.Fatal("expected terminal context overflow")
	}
	if a.lastAdmission().LastRecovery != contextRecoveryFailed {
		t.Fatalf("last recovery = %s, want failed", a.lastAdmission().LastRecovery)
	}
	if provider.AsContextLimitError(got.err) == nil && !errors.Is(got.err, ErrCompactionRequired) {
		t.Fatalf("terminal err = %v", got.err)
	}
}

func TestContextBudgetLearnAndSnapshotRace(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 1_048_576}, sess: sessionRuntime{conversation: NewSession("")}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			a.learnContextBudget(1_000_000-i, 1000+i, true)
			a.setLastRecovery(contextRecoveryLearnedRetry)
			_ = a.ContextMaintenanceSnapshot()
			_ = a.effectiveContextWindow()
		}
	}()
	for range 200 {
		a.learnContextBudget(900_000, 2000, true)
		_ = a.ContextMaintenanceSnapshot()
		_ = a.lastAdmission()
	}
	<-done
}

func TestThreeStateMaxOutputTokens(t *testing.T) {
	prov := &policyWindowProvider{policy: provider.ContextBudgetPolicy{
		WindowMode: provider.ContextWindowShared, AutoOutputTokens: 384_000,
		MaxOutputTokens: 384_000, LimitMode: provider.OutputLimitOmitWhenSafe,
	}}
	a := &Agent{agentConfig: agentConfig{contextWindow: 1_048_576}, svc: agentServices{prov: prov}}
	msgs := []provider.Message{{Role: provider.RoleUser, Content: "hi"}}
	pos := provider.Request{Messages: msgs, MaxTokens: 8192}
	if err := a.applyAdmissionToRequest(&pos); err != nil || pos.MaxTokens != 8192 {
		t.Fatalf("positive cap = %d err=%v", pos.MaxTokens, err)
	}
	zero := provider.Request{Messages: msgs, MaxTokens: 0}
	if err := a.applyAdmissionToRequest(&zero); err != nil || zero.MaxTokens != 0 {
		t.Fatalf("auto omit = %d err=%v", zero.MaxTokens, err)
	}
	neg := provider.Request{Messages: msgs, MaxTokens: -1}
	if err := a.applyAdmissionToRequest(&neg); err != nil || neg.MaxTokens != -1 {
		t.Fatalf("explicit omit = %d err=%v", neg.MaxTokens, err)
	}
}
