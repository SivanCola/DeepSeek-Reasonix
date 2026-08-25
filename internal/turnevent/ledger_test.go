package turnevent

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/store"
)

func testSessionPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "session.jsonl")
}

func TestLedgerPersistsMonotonicEventsAndExactlyOneTerminal(t *testing.T) {
	path := testSessionPath(t)
	l, err := Open(path, "session")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	l.SetRoutingMetadata("epoch-1", "submission-1")
	turnID, err := l.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	l.SetTranscriptSnapshot(7, "digest-1")
	started, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress)
	if err != nil || !ok {
		t.Fatalf("append started: ok=%v err=%v", ok, err)
	}
	done, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted)
	if err != nil || !ok {
		t.Fatalf("append done: ok=%v err=%v", ok, err)
	}
	if started.TurnID != turnID || done.TurnID != turnID || started.Sequence != 1 || done.Sequence != 2 {
		t.Fatalf("stamps = started(%q,%d) done(%q,%d), want turn %q seq 1,2", started.TurnID, started.Sequence, done.TurnID, done.Sequence, turnID)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnFailed); err != nil || ok {
		t.Fatalf("second terminal: ok=%v err=%v, want compare-and-append rejection", ok, err)
	}
	recs, err := l.EventsAfter(0)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(recs) != 2 || recs[0].Sequence != 1 || recs[1].Sequence != 2 {
		t.Fatalf("records = %#v, want two monotonic events", recs)
	}
	if recs[1].RuntimeEpoch != "epoch-1" || recs[1].SubmissionID != "submission-1" || recs[1].TranscriptRevision != 7 || recs[1].TranscriptDigest != "digest-1" {
		t.Fatalf("terminal metadata = %+v, want routing and transcript identity", recs[1])
	}
	if latest, replayAfter := l.ProjectionCursor(); latest != 2 || replayAfter != 2 {
		t.Fatalf("terminal cursor = (%d,%d), want (2,2)", latest, replayAfter)
	}
}

func TestLedgerProjectionCursorReplaysOnlyActiveTurn(t *testing.T) {
	path := testSessionPath(t)
	l, err := Open(path, "session")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin first: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted); err != nil || !ok {
		t.Fatalf("complete first: ok=%v err=%v", ok, err)
	}
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin second: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStatusChanged}, event.TurnQueued); err != nil || !ok {
		t.Fatalf("queue second: ok=%v err=%v", ok, err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("start second: ok=%v err=%v", ok, err)
	}
	if latest, replayAfter := l.ProjectionCursor(); latest != 3 || replayAfter != 1 {
		t.Fatalf("active cursor = (%d,%d), want (3,1)", latest, replayAfter)
	}
}

func TestLedgerSubmissionReceiptSurvivesCompletionAndIsOneShot(t *testing.T) {
	path := testSessionPath(t)
	l, err := Open(path, "session")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	l.SetRoutingMetadata("epoch-1", "submission-1")
	first, err := l.Begin()
	if err != nil {
		t.Fatalf("Begin first: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted); err != nil || !ok {
		t.Fatalf("complete first: ok=%v err=%v", ok, err)
	}
	if got := l.TurnIDForSubmission("submission-1"); got != first {
		t.Fatalf("receipt after completion = %q, want %q", got, first)
	}
	second, err := l.Begin()
	if err != nil {
		t.Fatalf("Begin automatic follow-up: %v", err)
	}
	if second == first {
		t.Fatal("follow-up reused the completed turn id")
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("start follow-up: ok=%v err=%v", ok, err)
	}
	records, err := l.EventsAfter(0)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	last := records[len(records)-1]
	if last.SubmissionID != "" || last.RuntimeEpoch != "" {
		t.Fatalf("automatic follow-up inherited routing metadata: %+v", last)
	}
	if got := l.TurnIDForSubmission("submission-1"); got != first {
		t.Fatalf("receipt after replacement = %q, want original %q", got, first)
	}
}

func TestLedgerRejectsStatusRegressionAndKeepsCancellationSticky(t *testing.T) {
	l, err := Open(testSessionPath(t), "session")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("start: ok=%v err=%v", ok, err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStatusChanged}, event.TurnQueued); err == nil || ok {
		t.Fatalf("status regression: ok=%v err=%v, want rejection", ok, err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStatusChanged}, event.TurnCancelling); err != nil || !ok {
		t.Fatalf("cancel: ok=%v err=%v", ok, err)
	}
	stamped, ok, err := l.Append(event.Event{Kind: event.PromptAnswered}, event.TurnInProgress)
	if err != nil || !ok || stamped.Status != event.TurnCancelling || l.CurrentStatus() != event.TurnCancelling {
		t.Fatalf("late prompt answer = (%+v,%v,%v), current=%q, want sticky cancelling", stamped, ok, err, l.CurrentStatus())
	}
}

func TestLedgerRepairsTornTailAndKeepsValidPrefix(t *testing.T) {
	path := testSessionPath(t)
	l, err := Open(path, "session")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}
	ledgerPath := store.SessionTurnEventLog(path)
	f, err := os.OpenFile(ledgerPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open ledger tail: %v", err)
	}
	if _, err := f.WriteString(`{"schemaVersion":1,"seq":2`); err != nil {
		t.Fatalf("write torn tail: %v", err)
	}
	_ = f.Close()

	reopened, err := Open(path, "session")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	recs, err := reopened.EventsAfter(0)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(recs) != 2 || recs[0].Status != event.TurnInProgress || recs[1].Status != event.TurnInterrupted {
		t.Fatalf("recovered records = %#v, want valid start plus interrupted terminal", recs)
	}
	damaged, err := os.ReadFile(store.SessionTurnEventLogDamaged(path))
	if err != nil {
		t.Fatalf("read damaged tail: %v", err)
	}
	if len(damaged) == 0 {
		t.Fatal("damaged tail was not isolated")
	}
}

func TestLedgerIsolatesNonMonotonicTail(t *testing.T) {
	path := testSessionPath(t)
	l, err := Open(path, "session")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}
	ledgerPath := store.SessionTurnEventLog(path)
	f, err := os.OpenFile(ledgerPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	if _, err := f.WriteString(`{"schemaVersion":1,"sessionId":"session","turnId":"duplicate","seq":1,"kind":"turn_started","status":"in_progress","createdAt":1,"event":{"kind":"turn_started"}}` + "\n"); err != nil {
		t.Fatalf("append duplicate sequence: %v", err)
	}
	_ = f.Close()

	reopened, err := Open(path, "session")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	recs, err := reopened.EventsAfter(0)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(recs) != 2 || recs[0].Sequence != 1 || recs[1].Status != event.TurnInterrupted {
		t.Fatalf("records = %#v, want valid seq 1 plus interrupted recovery", recs)
	}
	damaged, err := os.ReadFile(store.SessionTurnEventLogDamaged(path))
	if err != nil || len(damaged) == 0 {
		t.Fatalf("non-monotonic tail was not isolated: bytes=%d err=%v", len(damaged), err)
	}
}

func TestLedgerRecoveryClosesRunningToolsWithoutReplay(t *testing.T) {
	path := testSessionPath(t)
	l, err := Open(path, "session")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("append start: ok=%v err=%v", ok, err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
		ID: "write-1", Name: "write_file", Args: `{"path":"important.txt"}`,
	}}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("append dispatch: ok=%v err=%v", ok, err)
	}

	reopened, err := Open(path, "session")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	recs, err := reopened.EventsAfter(0)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(recs) != 4 {
		t.Fatalf("records = %#v, want start, dispatch, synthetic result, terminal", recs)
	}
	result := recs[2]
	if result.Kind != "tool_result" || result.Event.Tool == nil || result.Event.Tool.ID != "write-1" || result.Event.Tool.Err == "" {
		t.Fatalf("synthetic result = %#v, want interrupted write-1", result)
	}
	if result.Event.Tool.Args != "" || result.Event.Tool.Output != "" {
		t.Fatalf("synthetic result must not replay tool input/output: %#v", result.Event.Tool)
	}
	if recs[3].Kind != "turn_done" || recs[3].Status != event.TurnInterrupted {
		t.Fatalf("terminal = %#v, want interrupted turn_done", recs[3])
	}
}

func TestLedgerBootstrapsLegacyTranscriptWithoutRewritingIt(t *testing.T) {
	path := testSessionPath(t)
	original := []byte("legacy provider transcript\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	l, err := Open(path, "legacy")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	recs, err := l.EventsAfter(0)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(recs) != 1 || recs[0].Kind != "turn_status" || recs[0].Status != event.TurnCompleted {
		t.Fatalf("bootstrap = %#v, want one completed turn_status", recs)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("legacy transcript changed: %q", got)
	}
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin after bootstrap: %v", err)
	}
}

func TestLedgerReplayEmptyArrayAndUnknownFields(t *testing.T) {
	path := testSessionPath(t)
	l, err := Open(path, "session")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	recs, err := l.EventsAfter(99)
	if err != nil {
		t.Fatalf("EventsAfter empty: %v", err)
	}
	if recs == nil || len(recs) != 0 {
		t.Fatalf("empty replay = %#v, want non-nil empty slice", recs)
	}
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}
	ledgerPath := store.SessionTurnEventLog(path)
	data, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	data = append(data[:len(data)-2], []byte(`,"futureField":{"nested":true}}\n`)...)
	if err := os.WriteFile(ledgerPath, data, 0o600); err != nil {
		t.Fatalf("rewrite with unknown field: %v", err)
	}
	_, err = Open(path, "session")
	if err != nil {
		t.Fatalf("unknown fields must be ignored: %v", err)
	}
}
