package agent

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/contract/tool"
	"reasonix/internal/state/sessionstore"
)

type terminalSummaryProvider struct {
	closed chan struct{}
	usage  *provider.Usage
}

func (*terminalSummaryProvider) Name() string { return "terminal-summary" }

func (p *terminalSummaryProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: p.usage}
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "## Digest\npartial"}
	go func() {
		<-ctx.Done()
		close(ch)
		close(p.closed)
	}()
	return ch, nil
}

func TestSummaryEOFDoesNotAcceptAnInterruptedDigest(t *testing.T) {
	for _, tc := range []struct {
		name         string
		ceiling      time.Duration
		cancel       bool
		finishReason string
		want         CompactionNoopReason
	}{
		{name: "ceiling", ceiling: time.Second, want: FailSummaryCeiling},
		{name: "cancel", ceiling: time.Minute, cancel: true, want: FailCancelled},
		{name: "ceiling_after_length", ceiling: time.Second, finishReason: "length", want: FailSummaryCeiling},
		{name: "cancel_after_length", ceiling: time.Minute, cancel: true, finishReason: "length", want: FailCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				prev := SummaryBounds
				SummaryBounds = SummaryLimits{Idle: time.Minute, Ceiling: tc.ceiling}
				t.Cleanup(func() { SummaryBounds = prev })
				// Both select arms are ready on release. Repetition exercises either
				// choice without relying on scheduler delays to create the overlap.
				for attempt := range 32 {
					ctx, cancel := context.WithCancel(context.Background())
					prov := &terminalSummaryProvider{closed: make(chan struct{}), usage: &provider.Usage{TotalTokens: 7, FinishReason: tc.finishReason}}
					var spend []event.Event
					sink := event.FuncSink(func(e event.Event) {
						if e.Kind == event.Usage {
							spend = append(spend, e)
						}
						if e.Kind != event.CompactionProgress {
							return
						}
						if tc.cancel {
							cancel()
						}
						select {
						case <-prov.closed:
						case <-time.After(5 * time.Second):
							t.Fatal("provider did not close after interruption")
						}
					})
					a := New(prov, tool.NewRegistry(), &sessionstore.Session{}, Options{}, sink)
					s, usage, err := a.window().summarize(ctx, []provider.Message{{Role: provider.RoleUser, Content: "x"}}, "")
					cancel()
					if got := compactionFailureCode(err); got != tc.want || s != "" {
						t.Fatalf("attempt %d: summary = %q, code = %q (err %v), want empty / %q", attempt, s, got, err, tc.want)
					}
					if usage == nil || usage.TotalTokens != 7 {
						t.Fatalf("usage = %+v, want the interrupted request's spend", usage)
					}
					if len(spend) != 1 || spend[0].Usage.TotalTokens != 7 || spend[0].UsageSource != event.UsageSourceCompaction {
						t.Fatalf("spend = %+v, want exactly one compaction usage event", spend)
					}
					if tc.cancel && !errors.Is(err, context.Canceled) {
						t.Fatalf("err = %v, want caller cancellation", err)
					}
				}
			})
		})
	}
}

func TestSummaryEOFPreservesLiveSuccessAndTruncation(t *testing.T) {
	for _, tc := range []struct {
		name string
		prov provider.Provider
		want CompactionNoopReason
	}{
		{name: "complete", prov: &fakeProvider{reply: "complete"}},
		{name: "truncated", prov: &truncatingProvider{truncateAtOrBelow: summaryOutputMaxTokens}, want: FailSummaryTruncated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New(tc.prov, tool.NewRegistry(), &sessionstore.Session{}, Options{}, event.Discard)
			s, _, err := a.window().summarize(context.Background(), []provider.Message{{Role: provider.RoleUser, Content: "x"}}, "")
			if got := compactionFailureCode(err); got != tc.want {
				t.Fatalf("code = %q (err %v), want %q", got, err, tc.want)
			}
			if (s != "") != (tc.want == "") {
				t.Fatalf("summary = %q, want output only on a complete stream", s)
			}
		})
	}
}
