package control

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

func TestInboxSnapshotRecoversUnownedInFlightItem(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	rec, err := c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentSteer, Submit: "orphaned guidance"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, sessioninbox.StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}

	snap := c.InboxSnapshot()
	if !snap.Paused || !snap.Recovered || snap.RecoveredN != 1 {
		t.Fatalf("orphan recovery metadata = %+v", snap)
	}
	if len(snap.Items) != 1 || snap.Items[0].State != sessioninbox.StateUncertain {
		t.Fatalf("orphan recovery items = %+v", snap.Items)
	}
	if err := c.DeleteInboxItem(rec.ItemID); err != nil {
		t.Fatalf("delete recovered orphan: %v", err)
	}
}

func TestInboxSnapshotPreservesActivelyOwnedSteer(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	rec, err := c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentSteer, Submit: "active guidance"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, sessioninbox.StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}
	c.inbox.mu.Lock()
	c.inbox.trackActive(rec.ItemID)
	c.inbox.mu.Unlock()

	snap := c.InboxSnapshot()
	if snap.Paused || snap.Recovered || len(snap.Items) != 1 || snap.Items[0].State != sessioninbox.StateSteerAccepted {
		t.Fatalf("active steer was reclassified: %+v", snap)
	}

	c.inbox.mu.Lock()
	c.inbox.untrackActive(rec.ItemID)
	c.inbox.mu.Unlock()
	snap = c.InboxSnapshot()
	if !snap.Paused || len(snap.Items) != 1 || snap.Items[0].State != sessioninbox.StateUncertain {
		t.Fatalf("unowned steer was not recovered: %+v", snap)
	}
}

func TestTrySteerOrphanRequiresReviewBeforeExplicitRetry(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	rec, err := c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentSteer, Submit: "retry me"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, sessioninbox.StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := c.TrySteerInboxItem(rec.ItemID); !errors.Is(err, sessioninbox.ErrPaused) {
		t.Fatalf("first orphan retry error = %v, want ErrPaused", err)
	}
	snap := c.InboxSnapshot()
	if len(snap.Items) != 1 || snap.Items[0].State != sessioninbox.StateUncertain {
		t.Fatalf("first orphan retry state = %+v", snap)
	}
	if err := c.SetInboxPaused(false); err != nil {
		t.Fatal(err)
	}
	receipt, err := c.TrySteerInboxItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Disposition != sessioninbox.DispositionQueuedFollowup {
		t.Fatalf("explicit retry disposition = %q", receipt.Disposition)
	}
	meta, _, err := c.ReadInboxItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.State != sessioninbox.StateQueued || meta.Intent != sessioninbox.IntentFollowup {
		t.Fatalf("explicit retry meta = %+v", meta)
	}
}

func TestInboxAdmissionOwnsClaimBeforeSnapshotRecovery(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{
		Runner:      &fakeTurnRunner{},
		SessionPath: session,
		SessionDir:  dir,
		Sink:        event.Discard,
	})
	rec, err := c.EnqueueInbox(InboxRequest{Submit: "claimed atomically"})
	if err != nil {
		t.Fatal(err)
	}
	claimed := make(chan struct{})
	release := make(chan struct{})
	c.inbox.mu.Lock()
	c.inbox.beforePreparedAdmission = func() {
		close(claimed)
		<-release
	}
	c.inbox.mu.Unlock()
	type result struct {
		receipt sessioninbox.InboxReceipt
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		receipt, submitErr := c.TrySubmitInboxItem(rec.ItemID)
		resultCh <- result{receipt: receipt, err: submitErr}
	}()
	<-claimed
	lockEscaped := c.inbox.admissionMu.TryLock()
	if lockEscaped {
		c.inbox.admissionMu.Unlock()
	}
	close(release)
	got := <-resultCh
	if lockEscaped {
		t.Fatal("snapshot recovery could enter between durable claim and active ownership")
	}
	if got.err != nil || got.receipt.Disposition != sessioninbox.DispositionStarted {
		t.Fatalf("admission result = %+v, err=%v", got.receipt, got.err)
	}
	c.autosaveWG.Wait()
}

func TestInboxCompletionOwnsItemUntilDurableAck(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	rec, err := c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentSteer, Submit: "complete atomically"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, sessioninbox.StateSteerConsumed, ""); err != nil {
		t.Fatal(err)
	}
	beforeAck := make(chan struct{})
	release := make(chan struct{})
	c.inbox.mu.Lock()
	c.inbox.trackActive(rec.ItemID)
	c.inbox.beforeCompletionAck = func() {
		close(beforeAck)
		<-release
	}
	c.inbox.mu.Unlock()
	done := make(chan struct{})
	go func() {
		c.onInboxTurnDone()
		close(done)
	}()
	<-beforeAck
	lockEscaped := c.inbox.admissionMu.TryLock()
	if lockEscaped {
		c.inbox.admissionMu.Unlock()
	}
	close(release)
	<-done
	if lockEscaped {
		t.Fatal("snapshot recovery could enter between active ownership and durable acknowledgement")
	}
	snap := c.InboxSnapshot()
	if snap.Paused || len(snap.Items) != 0 {
		t.Fatalf("completed item survived durable acknowledgement: %+v", snap)
	}
}
