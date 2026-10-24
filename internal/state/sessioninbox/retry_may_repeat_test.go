package sessioninbox

import (
	"errors"
	"testing"
)

func heldItem(t *testing.T, s *Store, state InboxState, code BlockCode) string {
	t.Helper()
	rec, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "do the thing"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetStateCoded(rec.ItemID, state, code, "held"); err != nil {
		t.Fatal(err)
	}
	return rec.ItemID
}

func stateOf(t *testing.T, s *Store, id string) (InboxState, BlockCode) {
	t.Helper()
	for _, it := range s.Snapshot().Items {
		if it.ID == id {
			return it.State, it.BlockCode
		}
	}
	t.Fatalf("item %s not in the queue", id)
	return "", ""
}

// An uncertain entry may already have run, so sending it again can repeat a turn.
// The queue refuses that for every caller, not only for the one that draws a button.
func TestRetryRefusesAnUncertainEntryThatMayHaveRun(t *testing.T) {
	for _, code := range []BlockCode{BlockSnapshotFailed, BlockAckFailed, BlockOwnerInactive, BlockManifestSalvaged, ""} {
		s := heldStore(t)
		id := heldItem(t, s, StateUncertain, code)
		if err := s.RetryItem(id); !errors.Is(err, ErrRetryMayRepeat) {
			t.Fatalf("retry of an uncertain entry coded %q = %v, want ErrRetryMayRepeat", code, err)
		}
		if state, got := stateOf(t, s, id); state != StateUncertain || got != code {
			t.Fatalf("a refused retry left the entry %s/%q, want it untouched as uncertain/%q", state, got, code)
		}
	}
}

func TestRetryStillQueuesWhatNeverReachedTheModel(t *testing.T) {
	s := heldStore(t)
	id := heldItem(t, s, StateUncertain, BlockSteerUnapplied)
	if err := s.RetryItem(id); err != nil {
		t.Fatalf("retry of a steer that was never applied = %v, want it queued", err)
	}
	if state, _ := stateOf(t, s, id); state != StateQueued {
		t.Fatalf("state after retry = %s, want queued", state)
	}
}

func TestRetryStillQueuesABlockedEntry(t *testing.T) {
	s := heldStore(t)
	id := heldItem(t, s, StateBlocked, "")
	if err := s.RetryItem(id); err != nil {
		t.Fatalf("retry of a blocked entry = %v, want it queued", err)
	}
	if state, _ := stateOf(t, s, id); state != StateQueued {
		t.Fatalf("state after retry = %s, want queued", state)
	}
}
