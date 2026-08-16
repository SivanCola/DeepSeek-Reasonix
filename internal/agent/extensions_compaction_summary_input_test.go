package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/extension"
	"reasonix/internal/extension/dispatch"
	"reasonix/internal/extension/protocol"
)

func TestCompactionPrepareReplaceGuidance(t *testing.T) {
	client := &fakeDispatchClient{interceptFn: func(ev protocol.InterceptEvent, payload json.RawMessage) (protocol.InterceptResult, error) {
		if ev == protocol.EventCompactionPrepare {
			var in dispatch.CompactionPreparePayload
			if err := json.Unmarshal(payload, &in); err != nil {
				return protocol.InterceptResult{}, err
			}
			in.Guidance = "EXTENSION GUIDANCE"
			return replaceWith(t, in), nil
		}
		return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
	}}
	d := newExtDispatcher(client, true, nil, extension.PointCompactionPrepare)
	mp, a := newCompactionAgent(t, d)
	telemetry := ""
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Text == "compaction telemetry" {
			telemetry = e.Detail
		}
	})
	if err := a.CompactNow(context.Background(), ""); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	for i, req := range mp.requests {
		if instruction := req.Messages[len(req.Messages)-1].Content; !strings.Contains(instruction, "EXTENSION GUIDANCE") {
			t.Fatalf("summarizer call %d of %d missing the replaced guidance:\n%.200q", i+1, len(mp.requests), instruction)
		}
	}
	if sc := joinContents(visibleContext(a)); !strings.Contains(sc, "SUMMARY TEXT") {
		t.Fatalf("projection missing the summary:\n%.200q", sc)
	}
	if n := client.notifyCountFor(protocol.EventCompactionPrepare); n != 1 {
		t.Fatalf("compaction.prepare events = %d, want 1", n)
	}
	if !strings.Contains(telemetry, "summary_input="+SummaryInputCachePrefix) {
		t.Fatalf("telemetry = %q, want cache-prefix summary input", telemetry)
	}
}

func TestCompactionPrepareReplaceMessages(t *testing.T) {
	client := &fakeDispatchClient{interceptFn: func(ev protocol.InterceptEvent, _ json.RawMessage) (protocol.InterceptResult, error) {
		if ev == protocol.EventCompactionPrepare {
			return replaceWith(t, dispatch.CompactionPreparePayload{
				Messages: []protocol.ProviderMessage{{Role: protocol.ProviderRoleUser, Content: "EXTENSION FOLD"}},
			}), nil
		}
		return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
	}}
	d := newExtDispatcher(client, true, nil, extension.PointCompactionPrepare)
	mp, a := newCompactionAgent(t, d)
	telemetry := ""
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Text == "compaction telemetry" {
			telemetry = e.Detail
		}
	})
	if err := a.CompactNow(context.Background(), ""); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if got := joinContents(mp.requests[0].Messages); !strings.Contains(got, "EXTENSION FOLD") {
		t.Fatalf("summarizer messages = %.200q, want the replaced fold", got)
	}
	if !strings.Contains(telemetry, "summary_input="+SummaryInputExtensionRewritten) {
		t.Fatalf("telemetry = %q, want extension-rewritten summary input", telemetry)
	}
}
