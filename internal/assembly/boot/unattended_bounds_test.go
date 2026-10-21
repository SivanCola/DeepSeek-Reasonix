package boot

import (
	"errors"
	"testing"

	"reasonix/internal/contract/agentcomposition"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/tool"
)

func TestUnattendedOptionsPreserveProcessBounds(t *testing.T) {
	network := false
	parent := Options{WorkspaceRoot: "/source", WorkspaceOnly: true, SandboxNetworkOverride: &network,
		SandboxBashOverride: "required", PermissionAllow: []string{"read_file"}, AdditionalDirs: []string{"/authorized"}}
	child, err := unattendedOptions(parent, event.Discard, "ask", "/candidate", "model")
	if err != nil {
		t.Fatal(err)
	}
	if !child.WorkspaceOnly || child.SandboxNetworkOverride == nil || *child.SandboxNetworkOverride || child.SandboxBashOverride != "required" {
		t.Fatalf("child lost process bounds: %+v", child)
	}
	if child.WorkspaceRoot != "/candidate" || child.FileOverlay != nil || child.TerminalRunner != nil || child.WorkspaceLease != nil {
		t.Fatal("candidate retained source I/O owners")
	}
	child.PermissionAllow[0] = "bash"
	child.AdditionalDirs[0] = "/changed"
	if parent.PermissionAllow[0] != "read_file" || parent.AdditionalDirs[0] != "/authorized" {
		t.Fatal("child modified parent grants")
	}
}

func TestUnattendedNeverSilentlyDropsComposition(t *testing.T) {
	for _, parent := range []Options{{AgentComposition: "restricted"}, {Composition: &agentcomposition.Binding{}}} {
		_, err := unattendedOptions(parent, event.Discard, "auto", "/candidate", "model")
		var refusal tool.Refusal
		if !errors.As(err, &refusal) || refusal.Code != "isolation.unsupported_arg" {
			t.Fatalf("composition: %v", err)
		}
	}
}
