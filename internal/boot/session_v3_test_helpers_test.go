package boot

import (
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/sessionv3"
)

// withTestSessionV3 models the production host boundary explicitly. Boot no
// longer creates a persistence service as a side effect of controller assembly.
func withTestSessionV3(t *testing.T, opts Options) Options {
	t.Helper()
	sessionDir := opts.SessionDir
	if sessionDir == "" {
		sessionDir = config.SessionDir()
		opts.SessionDir = sessionDir
	}
	service, err := sessionv3.NewService("local", sessionv3.NewFilesystemPersistence(sessionv3.RootForLegacyDir(sessionDir)))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	opts.SessionService = service
	opts.SessionHostID = "local"
	return opts
}
