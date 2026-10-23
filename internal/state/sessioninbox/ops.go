package sessioninbox

import (
	"fmt"
	"strings"
	"time"
)

// DeleteItem removes metadata first, then the blob (crash may leave orphan).
func (s *Store) DeleteItem(id string) error {
	return s.removeItemInState(id, isPendingState, DispositionDeleted)
}

// CancelAcceptedSteer removes guidance a running turn accepted and has not read
// yet. The caller drops it from the executor's queue first: that drop is what
// proves nothing was ever on its way to the model, and this is the durable half
// of the same act.
func (s *Store) CancelAcceptedSteer(id string) error {
	return s.removeItemInState(id, func(st InboxState) bool { return st == StateSteerAccepted }, DispositionCancelled)
}

func (s *Store) removeItemInState(id string, allowed func(InboxState) bool, why Disposition) error {
	if s == nil {
		return ErrClosed
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return err
	}
	meta, ok := s.man.item(id)
	if !ok {
		return ErrNotFound
	}
	if !allowed(meta.State) {
		return ErrInvalidState
	}
	next := s.man.clone()
	keys := next.idempotencyKeysFor(id)
	next.rememberReceipt(keys, id, why, time.Now().UTC())
	removed, _ := next.removeItem(id)
	if err := s.commitManifestLocked(next); err != nil {
		return err
	}
	s.removeBlobLocked(blobNameFor(removed))
	s.notifyLocked(s.snapshotLocked())
	return nil
}

// DiscardPendingItems removes the named, not-yet-admitted items in one
// manifest transaction. Missing IDs are treated as already consumed so a
// frontend may safely cancel from a slightly stale metadata snapshot. Items
// that have crossed the admission boundary are never deleted here.
func (s *Store) DiscardPendingItems(ids []string) error {
	return s.DiscardPendingItemsOwned(ids, "")
}

// DiscardPendingItemsOwned atomically removes pending IDs belonging to source.
// Foreign-source IDs are ignored so one frontend cannot cancel another.
func (s *Store) DiscardPendingItemsOwned(ids []string, source string) error {
	if s == nil {
		return ErrClosed
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return err
	}
	for _, item := range s.man.Items {
		if _, ok := wanted[item.ID]; !ok {
			continue
		}
		if source != "" && item.Source != source {
			continue
		}
		switch item.State {
		case StateQueued, StateBlocked, StateUncertain:
		default:
			return ErrInvalidState
		}
	}

	next := s.man.clone()
	removed := make([]InboxItemMeta, 0, len(wanted))
	kept := next.Items[:0]
	for _, item := range next.Items {
		_, selected := wanted[item.ID]
		owned := source == "" || item.Source == source
		if selected && owned {
			removed = append(removed, item)
			continue
		}
		kept = append(kept, item)
	}
	if len(removed) == 0 {
		return nil
	}
	next.Items = kept
	now := time.Now().UTC()
	for _, item := range removed {
		keys := next.idempotencyKeysFor(item.ID)
		next.rememberReceipt(keys, item.ID, Disposition("discarded"), now)
		for _, key := range keys {
			delete(next.Idempotency, key)
			delete(next.IdempotencyHashes, key)
		}
	}
	if err := s.commitManifestLocked(next); err != nil {
		return err
	}
	for _, item := range removed {
		s.removeBlobLocked(blobNameFor(item))
	}
	s.notifyLocked(s.snapshotLocked())
	return nil
}

// MoveItem reorders the queue. toIndex is 0-based; values past the end append.
func (s *Store) MoveItem(id string, toIndex int) error {
	if s == nil {
		return ErrClosed
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return err
	}
	next := s.man.clone()
	from := next.indexOf(id)
	if from < 0 {
		return ErrNotFound
	}
	if !isPendingState(next.Items[from].State) {
		return ErrInvalidState
	}
	if toIndex < 0 {
		toIndex = 0
	}
	if toIndex >= len(next.Items) {
		toIndex = len(next.Items) - 1
	}
	if from == toIndex {
		return nil
	}
	it := next.Items[from]
	next.Items = append(next.Items[:from], next.Items[from+1:]...)
	if toIndex > len(next.Items) {
		toIndex = len(next.Items)
	}
	next.Items = append(next.Items[:toIndex], append([]InboxItemMeta{it}, next.Items[toIndex:]...)...)
	if err := s.commitManifestLocked(next); err != nil {
		return err
	}
	s.notifyLocked(s.snapshotLocked())
	return nil
}

// Pause holds the queue, naming why. A queue already paused keeps the cause that
// paused it first, except that the person's own pause takes the cause over: it
// is the one a standing consent never overrides, whatever came before it.
func (s *Store) Pause(code PauseCode) error { return s.setPaused(true, code) }

// Resume lifts the pause and the recovery mark that came with it.
func (s *Store) Resume() error { return s.setPaused(false, "") }

func (s *Store) setPaused(paused bool, code PauseCode) error {
	if s == nil {
		return ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return err
	}
	if s.man.Paused == paused && (!paused || code != PauseUser || s.man.PauseCode == PauseUser) {
		return nil
	}
	next := s.man.clone()
	next.Paused = paused
	next.PauseCode = code
	if !paused {
		next.Recovered = false
		next.RecoveredN = 0
	}
	if err := s.commitManifestLocked(next); err != nil {
		return err
	}
	s.notifyLocked(s.snapshotLocked())
	return nil
}

// SetState transitions one item's durable state.
func (s *Store) SetState(id string, state InboxState, blockReason string) error {
	return s.SetStateCoded(id, state, "", blockReason)
}

// SetStateCoded is SetState for a stop whose cause has a stable identity.
func (s *Store) SetStateCoded(id string, state InboxState, code BlockCode, blockReason string) error {
	if s == nil {
		return ErrClosed
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return err
	}
	next := s.man.clone()
	i := next.indexOf(id)
	if i < 0 {
		return ErrNotFound
	}
	next.Items[i].State = state
	next.Items[i].BlockReason = blockReason
	next.Items[i].BlockCode = code
	next.Items[i].UpdatedAt = time.Now().UTC()
	if err := s.commitManifestLocked(next); err != nil {
		return err
	}
	s.notifyLocked(s.snapshotLocked())
	return nil
}

// ClaimItem atomically transitions one queued item to running. It is the
// durable admission boundary for both asynchronous and synchronous frontends.
func (s *Store) ClaimItem(id string) error {
	return s.claimItem(id, false)
}

// ClaimItemOverPause is ClaimItem for an item the person has just let in: it is
// claimed although the queue is paused, and the pause stays.
func (s *Store) ClaimItemOverPause(id string) error {
	return s.claimItem(id, true)
}

func (s *Store) claimItem(id string, overPause bool) error {
	if s == nil {
		return ErrClosed
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return err
	}
	if s.man.Paused && !overPause {
		return ErrPaused
	}
	next := s.man.clone()
	i := next.indexOf(id)
	if i < 0 {
		return ErrNotFound
	}
	if next.Items[i].State != StateQueued {
		return ErrInvalidState
	}
	next.Items[i].State = StateRunning
	next.Items[i].BlockReason = ""
	next.Items[i].BlockCode = ""
	next.Items[i].UpdatedAt = time.Now().UTC()
	if err := s.commitManifestLocked(next); err != nil {
		return err
	}
	s.notifyLocked(s.snapshotLocked())
	return nil
}

// ConvertIntent changes followup ↔ steer while keeping the item queued.
func (s *Store) ConvertIntent(id string, intent InboxIntent) error {
	if s == nil {
		return ErrClosed
	}
	if intent != IntentSteer {
		intent = IntentFollowup
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return err
	}
	next := s.man.clone()
	i := next.indexOf(id)
	if i < 0 {
		return ErrNotFound
	}
	if !isPendingState(next.Items[i].State) {
		return ErrInvalidState
	}
	next.Items[i].Intent = intent
	next.Items[i].UpdatedAt = time.Now().UTC()
	if err := s.commitManifestLocked(next); err != nil {
		return err
	}
	s.notifyLocked(s.snapshotLocked())
	return nil
}

// AckDequeue removes a running/consumed item after durable transcript commit.
func (s *Store) AckDequeue(id string) error {
	if s == nil {
		return ErrClosed
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return err
	}
	next := s.man.clone()
	keys := next.idempotencyKeysFor(id)
	next.rememberReceipt(keys, id, Disposition("acknowledged"), time.Now().UTC())
	removed, ok := next.removeItem(id)
	if !ok {
		return ErrNotFound
	}
	switch removed.State {
	case StateRunning, StateSteerAccepted, StateSteerConsumed:
	default:
		return ErrInvalidState
	}
	if err := s.commitManifestLocked(next); err != nil {
		return err
	}
	s.removeBlobLocked(blobNameFor(removed))
	s.notifyLocked(s.snapshotLocked())
	return nil
}

// RetryItem resets uncertain/blocked items to queued.
func (s *Store) RetryItem(id string) error {
	if s == nil {
		return ErrClosed
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return err
	}
	next := s.man.clone()
	i := next.indexOf(id)
	if i < 0 {
		return ErrNotFound
	}
	switch next.Items[i].State {
	case StateUncertain, StateBlocked:
		if next.Items[i].State == StateUncertain && !next.Items[i].BlockCode.RetrySafe() {
			return ErrRetryMayRepeat
		}
		next.Items[i].State = StateQueued
		next.Items[i].BlockReason = ""
		next.Items[i].BlockCode = ""
		next.Items[i].UpdatedAt = time.Now().UTC()
	default:
		return ErrInvalidState
	}
	if err := s.commitManifestLocked(next); err != nil {
		return err
	}
	s.notifyLocked(s.snapshotLocked())
	return nil
}

// NextQueued returns the first FIFO queued follow-up (or rejected steer kept as
// follow-up) when the inbox is not paused. The bool reports whether that work
// was found; a non-nil error reports that the queue could not be read at all,
// which a caller must never treat as an empty queue.
func (s *Store) NextQueued() (InboxItemMeta, bool, error) {
	if s == nil {
		return InboxItemMeta{}, false, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if release, err := s.beginDiskTransactionLocked(); err == nil {
		release()
	} else if rerr := s.readManifestWithoutTransaction(); rerr != nil {
		// The lock is unavailable (another process holds it, or it cannot be
		// created) and the published snapshot is unreadable as well. That is not
		// evidence of an empty queue, so the failure is surfaced instead.
		return InboxItemMeta{}, false, fmt.Errorf("sessioninbox: read queue: %w (transaction: %w)", rerr, err)
	}
	if s.man == nil || s.man.Paused || s.readonly {
		return InboxItemMeta{}, false, nil
	}
	for _, it := range s.man.Items {
		if it.State == StateQueued && it.Intent == IntentFollowup {
			return it, true, nil
		}
		// Rejected steers that remain intent=steer but queued are still follow-ups
		// for the dispatcher after ConvertIntent; only followup intent is admitted.
	}
	// Also admit steer-intent items that are still queued (user wants them as turns).
	for _, it := range s.man.Items {
		if it.State == StateQueued {
			return it, true, nil
		}
	}
	return InboxItemMeta{}, false, nil
}

// Pause marks paused=true without requiring a mutation check beyond schema.
func (s *Store) ForcePause(reasonRecovered bool, n int) error {
	if s == nil {
		return ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return err
	}
	defer release()
	if s.closed || s.readonly {
		if s.readonly {
			return nil
		}
		return ErrClosed
	}
	next := s.man.clone()
	next.Paused = true
	next.PauseCode = PauseRecovered
	if reasonRecovered {
		next.Recovered = true
		if n > 0 {
			next.RecoveredN = n
		}
	}
	if err := s.commitManifestLocked(next); err != nil {
		return err
	}
	s.notifyLocked(s.snapshotLocked())
	return nil
}

func isPendingState(state InboxState) bool {
	switch state {
	case StateQueued, StateBlocked, StateUncertain:
		return true
	default:
		return false
	}
}
