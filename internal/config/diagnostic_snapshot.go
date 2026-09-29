package config

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"sync/atomic"

	"reasonix/internal/workspaceid"
)

var diagnosticInstance = rand.Text()
var diagnosticRevision atomic.Uint64

// DiagnosticSnapshot belongs to one explicitly resolved host/workspace. An
// unavailable result is never interchangeable with a successful empty result.
type DiagnosticSnapshot struct {
	HostID        string       `json:"hostId"`
	WorkspaceID   string       `json:"workspaceId"`
	WorkspaceRoot string       `json:"workspaceRoot"`
	InstanceID    string       `json:"instanceId"`
	Revision      uint64       `json:"revision"`
	Status        string       `json:"status"`
	Items         []Diagnostic `json:"items"`
}

func NewDiagnosticSnapshot(host, root, status string) DiagnosticSnapshot {
	return DiagnosticSnapshot{HostID: host, WorkspaceID: workspaceid.PathFingerprint(root), WorkspaceRoot: root,
		InstanceID: diagnosticInstance, Revision: diagnosticRevision.Add(1), Status: status, Items: []Diagnostic{}}
}

// InspectDiagnostics does not migrate files, resolve credentials, or execute
// project programs. Callers must resolve root from their own trusted binding.
func InspectDiagnostics(host, root string) DiagnosticSnapshot {
	return InspectDiagnosticDetails(host, root, "")
}

// InspectDiagnosticDetails includes redacted values only for the selected
// issue. Callers must bind root exactly as for InspectDiagnostics.
func InspectDiagnosticDetails(host, root, detailID string) DiagnosticSnapshot {
	snapshot := NewDiagnosticSnapshot(host, root, "unavailable")
	if root == "" {
		return snapshot
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return snapshot
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
		snapshot.WorkspaceRoot = root
		snapshot.WorkspaceID = workspaceid.PathFingerprint(root)
	}
	cfg, err := LoadForRootWithoutCredentialsReadOnly(root)
	if err != nil {
		snapshot.Items = LoadFailureDiagnostics(root, err)
		return snapshot
	}
	snapshot.Status, snapshot.Items = "ready", cfg.diagnosticGroups(detailID)
	return snapshot
}
