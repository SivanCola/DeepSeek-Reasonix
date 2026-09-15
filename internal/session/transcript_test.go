package session

import (
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/transcript"
	"reasonix/internal/turnevent"
)

func TestRuntimeTranscriptCoverageIncludesNonVisibleBusinessCommits(t *testing.T) {
	_, runtime := reviewRuntime(t)
	p := runtime.Transcript()
	before, err := p.Snapshot(transcript.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), "configuration", []Event{{Kind: "session/config", Payload: []byte(`{"modelRef":"test"}`)}}); err != nil {
		t.Fatal(err)
	}
	after, err := p.Snapshot(transcript.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if after.CoveredThroughSeq != runtime.StateSnapshot().Session.EventSequence || after.CoveredThroughSeq <= before.CoveredThroughSeq {
		t.Fatalf("non-visible commit lost business coverage: before=%d after=%d business=%d", before.CoveredThroughSeq, after.CoveredThroughSeq, runtime.StateSnapshot().Session.EventSequence)
	}
	if len(after.Records) != len(before.Records) {
		t.Fatal("configuration commit manufactured a chat record")
	}
	for index, text := range []string{"first ", "second"} {
		e := eventwire.ToWire(event.Event{Kind: event.Text, MessageID: "answer", AttemptID: "answer", Text: text})
		// Deliberately unrelated to the business sequence. A frame number is
		// never the accepted-log cursor, even when the values happen to match.
		if err := runtime.PublishTranscriptFrame(turnevent.Envelope{SessionID: runtime.Ref().SessionID, RuntimeEpoch: runtime.StateSnapshot().Epoch, TurnID: "turn", Sequence: uint64(1000 + index), Kind: e.Kind, Status: event.TurnInProgress, Event: e}); err != nil {
			t.Fatal(err)
		}
		cut, err := p.Snapshot(transcript.PageRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if cut.CoveredThroughSeq != after.CoveredThroughSeq || cut.ProjectionRevision <= after.ProjectionRevision {
			t.Fatalf("frame conflates revision and coverage: prior=%+v current=%+v", after.Boundary, cut.Boundary)
		}
		after = cut
	}
	if len(after.Records) != 1 || after.Records[0].Message.Content != "first second" {
		t.Fatalf("streaming prefix was not retained: %+v", after.Records)
	}
}

func TestRuntimeTranscriptSurvivesExecutionReplacement(t *testing.T) {
	_, runtime := reviewRuntime(t)
	p := runtime.Transcript()
	if p == nil {
		t.Fatal("runtime lacks its transcript publisher")
	}
	first := &testExecution{phase: RuntimeIdle, runtime: runtime}
	first.gen = runtime.BindExecution(first)
	if first.gen == 0 {
		t.Fatal("initial execution was not bound")
	}
	second := &testExecution{phase: RuntimeIdle, runtime: runtime}
	second.gen = runtime.ReplaceExecution(first.gen, second)
	if second.gen == 0 {
		t.Fatal("idle execution replacement was rejected")
	}
	t.Cleanup(func() { runtime.UnbindExecution(second.gen) })
	if runtime.Transcript() != p {
		t.Fatal("execution replacement created another transcript authority")
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), "after-replacement", []Event{{Kind: "session/config", Payload: []byte(`{"modelRef":"replacement"}`)}}); err != nil {
		t.Fatal(err)
	}
	cut, err := p.Snapshot(transcript.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if cut.CoveredThroughSeq != runtime.StateSnapshot().Session.EventSequence || cut.CoveredThroughSeq == 0 {
		t.Fatal("original publisher stopped observing commits after replacement")
	}
}
