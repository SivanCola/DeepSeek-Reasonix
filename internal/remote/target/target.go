// Package target defines the desktop execution target for local and SSH-backed
// workspaces. It is shared by persistence, frontend wire types, and the remote
// desktop kernel so local and remote tabs never share a Controller identity.
package target

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Kind classifies where tool execution and session authority live.
type Kind string

const (
	// KindLocal runs the controller in the current process (default).
	KindLocal Kind = "local"
	// KindSSH runs the controller on a remote host via remote-runtime.
	KindSSH Kind = "ssh"
)

// ExecutionTarget identifies where a desktop tab or session executes.
//
// Persistence rules:
//   - ConnectionID is runtime-only and must not be written to disk.
//   - Host key fingerprints live in independent trust records, never here.
//   - Missing or empty fields always mean KindLocal for backward compatibility.
type ExecutionTarget struct {
	Kind         Kind   `json:"kind,omitempty"`
	HostID       string `json:"hostId,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
	ConnectionID string `json:"connectionId,omitempty"` // runtime only
}

// Local returns the zero/local target used by legacy tabs and sessions.
func Local() ExecutionTarget {
	return ExecutionTarget{Kind: KindLocal}
}

// IsLocal reports whether t resolves to a local controller.
func (t ExecutionTarget) IsLocal() bool {
	return t.Normalized().Kind != KindSSH
}

// IsSSH reports whether t targets a remote SSH workspace.
func (t ExecutionTarget) IsSSH() bool {
	return t.Normalized().Kind == KindSSH
}

// Normalized fills defaults so legacy JSON without an execution target is local.
func (t ExecutionTarget) Normalized() ExecutionTarget {
	kind := Kind(strings.ToLower(strings.TrimSpace(string(t.Kind))))
	switch kind {
	case KindSSH:
		t.Kind = KindSSH
	default:
		// Empty, "local", or any unknown value → local. Unknown values must not
		// accidentally enable remote execution.
		t.Kind = KindLocal
		t.HostID = ""
		t.Workspace = ""
		t.ConnectionID = ""
	}
	t.HostID = strings.TrimSpace(t.HostID)
	t.Workspace = strings.TrimSpace(t.Workspace)
	t.ConnectionID = strings.TrimSpace(t.ConnectionID)
	return t
}

// Persistable returns a copy safe for desktop tab / state JSON: connection IDs
// are stripped so reconnects allocate a fresh runtime identity.
func (t ExecutionTarget) Persistable() ExecutionTarget {
	n := t.Normalized()
	n.ConnectionID = ""
	if n.Kind == KindLocal {
		return ExecutionTarget{}
	}
	return n
}

// Validate checks persisted or caller-supplied target fields.
func (t ExecutionTarget) Validate() error {
	n := t.Normalized()
	if n.Kind == KindLocal {
		return nil
	}
	if n.HostID == "" {
		return fmt.Errorf("execution target: hostId is required for ssh")
	}
	if n.Workspace == "" {
		return fmt.Errorf("execution target: workspace is required for ssh")
	}
	if strings.ContainsAny(n.HostID, "\x00\n\r") {
		return fmt.Errorf("execution target: hostId contains invalid characters")
	}
	return nil
}

// EqualStable compares the durable identity (kind/host/workspace), ignoring
// the runtime ConnectionID.
func (t ExecutionTarget) EqualStable(other ExecutionTarget) bool {
	a, b := t.Normalized(), other.Normalized()
	return a.Kind == b.Kind && a.HostID == b.HostID && a.Workspace == b.Workspace
}

// UnmarshalJSON accepts legacy payloads that omit kind and treats them as local.
func (t *ExecutionTarget) UnmarshalJSON(data []byte) error {
	type raw ExecutionTarget
	var v raw
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*t = ExecutionTarget(v).Normalized()
	if t.Kind == KindLocal {
		// Keep omitempty-friendly zero values for pure local targets.
		*t = ExecutionTarget{}
	}
	return nil
}

// MarshalJSON encodes pure-local targets as JSON null so pointer fields with
// omitempty stay absent, and non-local targets without runtime connection IDs.
func (t ExecutionTarget) MarshalJSON() ([]byte, error) {
	n := t.Persistable()
	if n.Kind == "" || n.Kind == KindLocal {
		return []byte("null"), nil
	}
	type raw ExecutionTarget
	return json.Marshal(raw(n))
}
