package runtime

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/remote/protocol"
	"reasonix/internal/rpcwire"
)

func TestRuntimeSessionCreateAndFileList(t *testing.T) {
	ws := t.TempDir()
	// Short absolute socket path — macOS rejects long unix paths.
	sock := filepath.Join(t.TempDir(), "r.sock")
	if len(sock) > 100 {
		sock = filepath.Join("/tmp", "rx-wb-"+t.Name()+".sock")
		t.Cleanup(func() { _ = os.Remove(sock) })
	}
	srv := New(Options{Workspace: ws, Version: "test"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(ctx, sock) }()

	// Wait for socket.
	var conn net.Conn
	var err error
	for i := 0; i < 50; i++ {
		select {
		case e := <-errCh:
			t.Fatalf("listen failed: %v", e)
		default:
		}
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	wire := rpcwire.NewConn(conn, conn, rpcwire.Options{
		Name: "test", StrictJSONRPC: true,
		MaxInboundBytes: protocol.FrameBytes, MaxOutboundBytes: protocol.FrameBytes,
	})
	go wire.Serve(ctx)

	raw, err := wire.Request(ctx, string(protocol.MethodRemoteInitialize), map[string]any{
		"buildId": map[string]any{"schemaHash": protocol.SchemaHash(), "protocolVersion": protocol.ProtocolVersion},
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("empty initialize result")
	}

	raw, err = wire.Request(ctx, string(protocol.MethodSessionCreate), map[string]any{"model": "m"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created map[string]any
	_ = json.Unmarshal(raw, &created)
	if created["session"] == nil {
		t.Fatalf("create = %s", raw)
	}

	raw, err = wire.Request(ctx, string(protocol.MethodFileList), map[string]any{"path": ""})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listed map[string]any
	_ = json.Unmarshal(raw, &listed)
	if listed["entries"] == nil {
		t.Fatalf("list = %s", raw)
	}
	srv.Close()
}

func TestRuntimeGraceDetach(t *testing.T) {
	srv := New(Options{Workspace: t.TempDir(), Version: "t"})
	srv.ForceDetachForTest()
	if srv.Attached() {
		t.Fatal("expected detached")
	}
}
