package agent

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/provider"
)

// ownsWritableBaseline: digest ownership, or exclusive lease at same revision (#8294).
func (s *Session) ownsWritableBaseline(path string, existingDigest, rawDigest [sha256.Size]byte, rawDiffers bool, existingRevision int64, existingLedgerDigest string, nextVersion uint64) bool {
	if s.ownsPersistedState(path, existingDigest, existingRevision, existingLedgerDigest, nextVersion) {
		return true
	}
	if rawDiffers && s.ownsPersistedState(path, rawDigest, existingRevision, existingLedgerDigest, nextVersion) {
		return true
	}
	state := s.persistState(path)
	if !state.ok || !state.revisionKnown || state.version > nextVersion {
		return false
	}
	if existingRevision != 0 && state.revision != existingRevision {
		return false
	}
	return SessionLeaseHeldByCurrentRuntime(path)
}

// shutdownRecoverySessionPath is one fixed isolated path per writer (#8342).
func shutdownRecoverySessionPath(originalPath string) string {
	writerDigest := sha256.Sum256([]byte(SessionWriterID()))
	suffix := fmt.Sprintf("-recovery-%x", writerDigest[:6])
	id := BranchID(originalPath)
	if strings.HasSuffix(id, suffix) {
		return originalPath // already this writer's copy: rewrite in place
	}
	return filepath.Join(filepath.Dir(originalPath),
		fmt.Sprintf("%s%s.jsonl", recoveryParentStem(id), suffix))
}

// writeRecoveryEventLog writes a recovery event log; isolated lanes compact.
func writeRecoveryEventLog(path string, msgs []provider.Message, digest [sha256.Size]byte, isolated bool) error {
	if isolated {
		return compactSessionEventLog(path, msgs, digest, 0, "recovery")
	}
	return appendSessionReplaceEvent(path, msgs, digest, 0, "recovery")
}
