package config

import (
	"log/slog"
	"os"
	"strings"

	"reasonix/internal/sandbox"
)

// readCredentialFile keeps successful reads independent of sandbox ACL locks.
// Repair is attempted only for a denied read of the global credential store.
func readCredentialFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil || !os.IsPermission(err) || runtimeGOOS != "windows" {
		return data, err
	}
	credentials := strings.TrimSpace(UserCredentialsPath())
	if path == "" || credentials == "" || !samePath(path, credentials) {
		return nil, err
	}
	if repairErr := sandbox.RepairLegacyCredentialDeny(path); repairErr != nil {
		slog.Warn("config: legacy credential ACL repair failed", "path", path, "err", repairErr)
		return nil, repairErr
	}
	return os.ReadFile(path)
}
