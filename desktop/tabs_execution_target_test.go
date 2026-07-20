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

// TestSaveTabsCollectLockedPersistsExecutionTarget proves the real save path
// (not a hand-built struct) writes ExecutionTarget and RemoteSessionID.
func TestSaveTabsCollectLockedPersistsExecutionTarget(t *testing.T) {
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"tab_ssh": {
				ID:            "tab_ssh",
				Scope:         "project",
				WorkspaceRoot: "/unused",
				TopicID:       "main",
				ExecutionTarget: target.ExecutionTarget{
					Kind:         target.KindSSH,
					HostID:       "lab",
					Workspace:    "/home/u/work",
					ConnectionID: "must-strip",
				},
				RemoteSessionID: "rs_live",
				model:           "deepseek/chat",
			},
		},
		tabOrder:    []string{"tab_ssh"},
		activeTabID: "tab_ssh",
	}
	_, entries, _, _ := app.saveTabsCollectLocked()
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	e := entries[0]
	if e.ExecutionTarget == nil || !e.ExecutionTarget.IsSSH() {
		t.Fatalf("execution target not saved: %+v", e.ExecutionTarget)
	}
	if e.ExecutionTarget.ConnectionID != "" {
		t.Fatalf("connectionId must be stripped on save: %q", e.ExecutionTarget.ConnectionID)
	}
	if e.ExecutionTarget.HostID != "lab" || e.ExecutionTarget.Workspace != "/home/u/work" {
		t.Fatalf("target fields = %+v", e.ExecutionTarget)
	}
	if e.RemoteSessionID != "rs_live" {
		t.Fatalf("remoteSessionId = %q", e.RemoteSessionID)
	}
}
