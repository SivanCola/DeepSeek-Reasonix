package target

import (
	"encoding/json"
	"testing"
)

func TestLegacyMissingTargetIsLocal(t *testing.T) {
	var tab struct {
		ID     string           `json:"id"`
		Target *ExecutionTarget `json:"executionTarget,omitempty"`
	}
	if err := json.Unmarshal([]byte(`{"id":"tab_1"}`), &tab); err != nil {
		t.Fatal(err)
	}
	if tab.Target != nil {
		t.Fatalf("expected nil target, got %+v", tab.Target)
	}
	// Embedded zero value also normalizes to local.
	var zero ExecutionTarget
	if !zero.IsLocal() || zero.IsSSH() {
		t.Fatalf("zero target must be local: %+v", zero)
	}
}

func TestOldDesktopTabJSONDefaultsLocal(t *testing.T) {
	// Mirrors desktopTabEntry shape without executionTarget (pointer field).
	raw := `{"id":"t1","scope":"project","workspaceRoot":"/tmp/proj","topicId":"main"}`
	var entry struct {
		ID              string           `json:"id"`
		Scope           string           `json:"scope"`
		WorkspaceRoot   string           `json:"workspaceRoot"`
		TopicID         string           `json:"topicId"`
		ExecutionTarget *ExecutionTarget `json:"executionTarget,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.ExecutionTarget != nil && !entry.ExecutionTarget.IsLocal() {
		t.Fatalf("legacy entry must default local, got %+v", entry.ExecutionTarget)
	}
	out, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	// omitempty must not inject kind=local into old-compatible writes.
	if string(out) != `{"id":"t1","scope":"project","workspaceRoot":"/tmp/proj","topicId":"main"}` {
		t.Fatalf("unexpected marshal: %s", out)
	}
}

func TestSSHTargetRoundTripStripsConnectionID(t *testing.T) {
	in := ExecutionTarget{
		Kind:         KindSSH,
		HostID:       "lab",
		Workspace:    "/home/u/work",
		ConnectionID: "conn-should-not-persist",
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"kind":"ssh","hostId":"lab","workspace":"/home/u/work"}` {
		t.Fatalf("persistable marshal = %s", data)
	}
	var out ExecutionTarget
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !out.IsSSH() || out.HostID != "lab" || out.Workspace != "/home/u/work" {
		t.Fatalf("unmarshal = %+v", out)
	}
	if out.ConnectionID != "" {
		t.Fatalf("connectionId must not round-trip: %q", out.ConnectionID)
	}
}

func TestValidateSSHRequiresHostAndWorkspace(t *testing.T) {
	if err := (ExecutionTarget{Kind: KindSSH}).Validate(); err == nil {
		t.Fatal("expected validation error")
	}
	if err := (ExecutionTarget{Kind: KindSSH, HostID: "h", Workspace: "/w"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownKindIsLocal(t *testing.T) {
	var t0 ExecutionTarget
	if err := json.Unmarshal([]byte(`{"kind":"weird","hostId":"x"}`), &t0); err != nil {
		t.Fatal(err)
	}
	if !t0.IsLocal() {
		t.Fatalf("unknown kind must not enable remote: %+v", t0)
	}
}
