package isolation

import (
	"encoding/json"
	"os"
	"path/filepath"

	"reasonix/internal/contract/tool"
)

// Management scope comes from the original's durable identity, independently
// of its current run lock or later execution generation.
func (s *Store) managedResultMetadata(id string) (durableEntry, error) {
	var found *durableEntry
	for _, marker := range s.resultMarkers() {
		raw, err := os.ReadFile(marker)
		var d durableEntry
		if err != nil || json.Unmarshal(raw, &d) != nil || d.ID != id {
			continue
		}
		if found != nil {
			return durableEntry{}, tool.Refusal{Code: CodeUnsupportedState, Message: "duplicate original result identities retained"}
		}
		d.legacy = filepath.Base(marker) == legacyEntryFile
		found = &d
	}
	if found == nil {
		return durableEntry{}, tool.Refusal{Code: CodeUnknownID, Message: "original isolated result identity is unavailable"}
	}
	if !sameDir(found.Workspace, s.workspace) {
		return durableEntry{}, tool.Refusal{Code: CodeOwnerMismatch, Message: "original result belongs to another workspace"}
	}
	if !validID(found.ID) || !supportedResultVersion(*found) || !supportedLifecycle(*found) {
		return durableEntry{}, tool.Refusal{Code: CodeUnsupportedState, Message: "original result identity is unsupported; record retained"}
	}
	return *found, nil
}
