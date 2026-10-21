package isolation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The entry lock serializes ID allocation across all workspaces in this root.
func (s *Store) reserveIdentity(id string) error {
	for _, name := range []string{entryFile, legacyEntryFile} {
		marks, err := filepath.Glob(filepath.Join(s.managedRoot, "*", name))
		if err != nil {
			return err
		}
		for _, mark := range marks {
			raw, err := os.ReadFile(mark)
			if err != nil {
				return err
			}
			var header struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &header); err != nil {
				return fmt.Errorf("read retained isolation identity: %w", err)
			}
			if header.ID == id {
				return fmt.Errorf("isolated identity already exists: %s", id)
			}
		}
	}
	f, err := os.OpenFile(filepath.Join(s.managedRoot, "."+id+".allocated"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}
