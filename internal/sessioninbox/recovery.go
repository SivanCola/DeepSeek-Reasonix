package sessioninbox

import "time"

// RecoverOrphanedInFlight converts admitted items that no live Controller owns
// into reviewable pending work. The transition is atomic so a crash cannot
// leave only part of a multi-item active set recoverable.
func (s *Store) RecoverOrphanedInFlight(ownedIDs []string) (int, error) {
	if s == nil {
		return 0, ErrClosed
	}
	owned := make(map[string]struct{}, len(ownedIDs))
	for _, id := range ownedIDs {
		if id != "" {
			owned[id] = struct{}{}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	needsRecovery := false
	for i := range s.man.Items {
		if _, ok := owned[s.man.Items[i].ID]; ok {
			continue
		}
		switch s.man.Items[i].State {
		case StateRunning, StateSteerAccepted, StateSteerConsumed:
			needsRecovery = true
		}
	}
	if !needsRecovery {
		return 0, nil
	}
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return 0, err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return 0, err
	}

	next := s.man.clone()
	now := time.Now().UTC()
	recovered := 0
	for i := range next.Items {
		if _, ok := owned[next.Items[i].ID]; ok {
			continue
		}
		switch next.Items[i].State {
		case StateRunning, StateSteerAccepted, StateSteerConsumed:
			next.Items[i].State = StateUncertain
			next.Items[i].BlockReason = "in-flight owner is no longer active"
			next.Items[i].UpdatedAt = now
			recovered++
		}
	}
	if recovered == 0 {
		return 0, nil
	}
	next.Paused = true
	next.Recovered = true
	next.RecoveredN = min(len(next.Items), next.RecoveredN+recovered)
	if err := s.commitManifestLocked(next); err != nil {
		return 0, err
	}
	s.notifyLocked(s.snapshotLocked())
	return recovered, nil
}
