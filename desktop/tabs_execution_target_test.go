package main

import (
	"encoding/json"
	"testing"

	"reasonix/internal/remote/target"
)

func TestDesktopTabEntryLegacyJSONIsLocal(t *testing.T) {
	raw := `{"id":"t1","scope":"project","workspaceRoot":"/tmp/p","topicId":"main"}`
	var entry desktopTabEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.ExecutionTarget != nil {
		t.Fatalf("expected nil target, got %+v", entry.ExecutionTarget)
	}
	out, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != raw {
		// Field order may differ; ensure no executionTarget key.
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		if _, ok := m["executionTarget"]; ok {
			t.Fatalf("executionTarget must be omitted: %s", out)
		}
	}
}

func TestDesktopTabEntrySSHRoundTripStripsConnectionID(t *testing.T) {
	entry := desktopTabEntry{
		ID:            "t2",
		Scope:         "project",
		WorkspaceRoot: "/local/unused",
		TopicID:       "main",
		ExecutionTarget: &target.ExecutionTarget{
			Kind:         target.KindSSH,
			HostID:       "lab",
			Workspace:    "/home/u/work",
			ConnectionID: "runtime-only",
		},
		RemoteSessionID: "rs_abc",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var again desktopTabEntry
	if err := json.Unmarshal(data, &again); err != nil {
		t.Fatal(err)
	}
	if again.ExecutionTarget == nil || !again.ExecutionTarget.IsSSH() {
		t.Fatalf("target = %+v", again.ExecutionTarget)
	}
	if again.ExecutionTarget.ConnectionID != "" {
		t.Fatalf("connectionId must not persist: %q", again.ExecutionTarget.ConnectionID)
	}
	if again.RemoteSessionID != "rs_abc" {
		t.Fatalf("remote session = %q", again.RemoteSessionID)
	}
}
