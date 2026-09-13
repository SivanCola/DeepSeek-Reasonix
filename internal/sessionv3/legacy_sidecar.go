package sessionv3

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"reasonix/internal/agent"
)

var ErrLegacySidecarConflict = errors.New("legacy transcript has an unresolved event sidecar")

// Until paired-source import can prove which history is authoritative, never
// silently choose the legacy transcript over possibly newer event-log work.
func rejectUnresolvedLegacySidecar(sourcePath string) error {
	dir := filepath.Join(RootForLegacyDir(filepath.Dir(sourcePath)), agent.BranchID(sourcePath))
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("%w: retain both sources read-only and use a paired-source importer before continuing", ErrLegacySidecarConflict)
}
