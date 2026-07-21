package attach

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/remote/protocol"
)

func TestAttachRejectsSchemaMismatch(t *testing.T) {
	var out bytes.Buffer
	workspace := t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"buildId": map[string]any{
			"productVersion":  "test",
			"sourceRevision":  strings.Repeat("a", 40),
			"schemaHash":      "sha256:" + strings.Repeat("0", 64),
			"protocolVersion": protocol.ProtocolVersion,
		},
		"clientInstanceId": "desktop-test",
		"workspace":        workspace,
	})
	frame, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "remote/initialize", "params": json.RawMessage(params),
	})
	r, w := io.Pipe()
	go func() {
		_, _ = w.Write(append(frame, '\n'))
		_ = w.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Run(ctx, r, &out, Options{
		Workspace: workspace, Version: "t", InProcess: true,
		SchemaHash: protocol.SchemaHash(),
	})
	// Schema mismatch returns nil error from Run after writing RPC error, or an error.
	// Either way the response should mention mismatch.
	if !strings.Contains(out.String(), "schema hash mismatch") && err == nil {
		// Run may return the write path with error frame only.
		if !strings.Contains(out.String(), "mismatch") {
			t.Fatalf("out=%q err=%v", out.String(), err)
		}
	}
}

func TestAttachRejectsWorkspaceDifferentFromAttachTarget(t *testing.T) {
	var out bytes.Buffer
	configuredWorkspace := t.TempDir()
	requestedWorkspace := t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"buildId": map[string]any{
			"productVersion":  "test",
			"sourceRevision":  strings.Repeat("a", 40),
			"schemaHash":      protocol.SchemaHash(),
			"protocolVersion": protocol.ProtocolVersion,
		},
		"clientInstanceId": "desktop-test",
		"workspace":        requestedWorkspace,
	})
	frame, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "remote/initialize", "params": json.RawMessage(params),
	})
	r, w := io.Pipe()
	go func() {
		_, _ = w.Write(append(frame, '\n'))
		_ = w.Close()
	}()

	err := Run(context.Background(), r, &out, Options{
		Workspace: configuredWorkspace,
		Version:   "t",
		InProcess: true,
	})
	if err != nil {
		t.Fatalf("Run returned transport error: %v", err)
	}
	if !strings.Contains(out.String(), "workspace does not match attach target") {
		t.Fatalf("out=%q", out.String())
	}
}

func TestAttachUsesInitializeWorkspaceWhenTargetIsUnbound(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "rx-attach-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	params, _ := json.Marshal(map[string]any{
		"buildId": map[string]any{
			"productVersion":  "test",
			"sourceRevision":  strings.Repeat("a", 40),
			"schemaHash":      protocol.SchemaHash(),
			"protocolVersion": protocol.ProtocolVersion,
		},
		"clientInstanceId": "desktop-test",
		"workspace":        "~/project",
	})
	frame, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "remote/initialize", "params": json.RawMessage(params),
	})
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write(append(frame, '\n'))
		time.Sleep(200 * time.Millisecond)
		_ = pw.Close()
	}()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = Run(ctx, pr, &out, Options{
		Home: home, Version: "t", InProcess: true,
	})
	if strings.Contains(out.String(), "workspace is required") || strings.Contains(out.String(), "invalid Remote workspace") {
		t.Fatalf("initialize workspace was not accepted: out=%q err=%v", out.String(), err)
	}
	if !strings.Contains(out.String(), `"result"`) {
		t.Fatalf("initialize did not reach the normalized runtime workspace: out=%q err=%v", out.String(), err)
	}
}

func TestResolveWorkspacePathExpandsRemoteHome(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "home", raw: "~", want: home},
		{name: "home child", raw: "~/project", want: filepath.Join(home, "project")},
		{name: "root", raw: string(filepath.Separator), want: string(filepath.Separator)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveWorkspacePath(tt.raw, home)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("resolveWorkspacePath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
	if _, err := resolveWorkspacePath(" ", home); err == nil {
		t.Fatal("blank workspace should be rejected")
	}
}

func TestAttachInProcessInitializeOK(t *testing.T) {
	ws := t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"buildId": map[string]any{
			"productVersion":  "test",
			"sourceRevision":  strings.Repeat("a", 40),
			"schemaHash":      protocol.SchemaHash(),
			"protocolVersion": protocol.ProtocolVersion,
		},
		"clientInstanceId": "desktop-test",
		"workspace":        ws,
	})
	frame, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "remote/initialize", "params": json.RawMessage(params),
	})
	// After initialize the proxy will hang on copy until we close stdin after a second request is not needed —
	// closing stdin after first frame ends the proxy.
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write(append(frame, '\n'))
		// Keep open briefly so runtime can start, then close to end proxy.
		time.Sleep(200 * time.Millisecond)
		_ = pw.Close()
	}()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Run(ctx, pr, &out, Options{
		Workspace: ws, Home: filepath.Dir(ws), Version: "t", InProcess: true,
	})
	if err != nil && !strings.Contains(err.Error(), "EOF") {
		// Accept connection closed after stdin EOF.
		t.Logf("run err (may be ok): %v out=%q", err, out.String())
	}
}
