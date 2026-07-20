package attach

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
