// Package attach implements `reasonix remote attach-workspace --stdio`.
// It validates initialize (Schema Hash), starts or reuses the workspace
// runtime, and becomes a transparent Unix-socket proxy for bidirectional RPC.
package attach

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reasonix/internal/remote/protocol"
	"reasonix/internal/remote/workbench/runtime"
	"reasonix/internal/rpcwire"
)

// Options configures attach-workspace.
type Options struct {
	// Workspace absolute path (from init DTO or CLI flag; never shell-interpolated).
	Workspace string
	// Home is the remote user home for socket placement.
	Home string
	// Version is the product version string for diagnostics.
	Version string
	// SchemaHash expected; empty uses protocol.SchemaHash().
	SchemaHash string
	// RuntimeBinary is optional path to reasonix for launching runtime child.
	// When empty, attach serves the runtime in-process (tests / single binary).
	RuntimeBinary string
	// InProcess when true always serves runtime in this process (tests).
	InProcess bool
}

// Run reads the first initialize frame, ensures runtime for workspace, then
// proxies stdio ↔ Unix socket until EOF.
func Run(ctx context.Context, stdin io.ReadCloser, stdout io.Writer, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if stdin == nil || stdout == nil {
		return errors.New("attach-workspace requires stdio")
	}
	ws := strings.TrimSpace(opts.Workspace)
	if ws == "" {
		return errors.New("workspace is required")
	}
	configuredWorkspace, err := filepath.Abs(ws)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	home := strings.TrimSpace(opts.Home)
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	schemaHash := strings.TrimSpace(opts.SchemaHash)
	if schemaHash == "" {
		schemaHash = protocol.SchemaHash()
	}

	reader := bufio.NewReaderSize(stdin, 64<<10)
	stop := context.AfterFunc(ctx, func() { _ = stdin.Close() })
	frame, err := rpcwire.ReadStrictRequestFrame(reader, protocol.FrameBytes)
	stop()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("attach bootstrap: %w", err)
	}
	if protocol.Method(frame.Method) != protocol.MethodRemoteInitialize {
		return writeRPCError(stdout, frame.ID, rpcwire.ErrInvalidRequest, "remote/initialize must be first")
	}
	decoded, err := protocol.DecodeRequestParams(protocol.MethodRemoteInitialize, frame.Params)
	if err != nil {
		return writeRPCError(stdout, frame.ID, rpcwire.ErrInvalidParams, "invalid remote/initialize params")
	}
	init := decoded.(protocol.InitializeParams)
	requestedWorkspace := strings.TrimSpace(init.Workspace)
	if abs, absErr := filepath.Abs(requestedWorkspace); absErr == nil {
		ws = abs
	} else {
		return writeRPCError(stdout, frame.ID, rpcwire.ErrInvalidParams, "invalid Remote workspace")
	}
	if filepath.Clean(ws) != filepath.Clean(configuredWorkspace) {
		return writeRPCError(stdout, frame.ID, rpcwire.ErrInvalidParams, "Remote workspace does not match attach target")
	}
	peerHash := strings.TrimSpace(init.BuildID.SchemaHash)
	if !strings.EqualFold(peerHash, schemaHash) {
		return writeRPCError(stdout, frame.ID, rpcwire.ErrInvalidRequest,
			fmt.Sprintf("schema hash mismatch: expected %s", schemaHash))
	}

	sock := runtime.SocketPath(home, ws)
	if err := ensureRuntime(ctx, opts, home, ws, sock); err != nil {
		return writeRPCError(stdout, frame.ID, rpcwire.ErrInternal, "runtime start failed: "+err.Error())
	}

	conn, err := dialSocket(ctx, sock, 10*time.Second)
	if err != nil {
		return writeRPCError(stdout, frame.ID, rpcwire.ErrInternal, "runtime dial failed")
	}
	defer conn.Close()

	// Re-encode only the frozen typed fields. This prevents bootstrap-only or
	// legacy fields from bypassing the runtime Router's strict DTO boundary.
	forward, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": json.RawMessage(frame.ID),
		"method": string(protocol.MethodRemoteInitialize), "params": init,
	})
	if err != nil {
		return fmt.Errorf("encode initialize: %w", err)
	}
	if _, err := conn.Write(append(forward, '\n')); err != nil {
		return fmt.Errorf("forward initialize: %w", err)
	}
	return proxy(ctx, stdin, reader, stdout, conn)
}

func ensureRuntime(ctx context.Context, opts Options, home, workspace, sock string) error {
	// Try dial first (reuse live runtime).
	if c, err := dialSocket(ctx, sock, 200*time.Millisecond); err == nil {
		_ = c.Close()
		return nil
	}
	lockDir := sock + ".start.lock"
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := os.Mkdir(lockDir, 0o700); err == nil {
			defer os.Remove(lockDir)
			break
		} else if !os.IsExist(err) {
			return err
		}
		if c, err := dialSocket(ctx, sock, 200*time.Millisecond); err == nil {
			_ = c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for workspace runtime startup lease")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Re-check after taking the lease: another attach may have started it.
	if c, err := dialSocket(ctx, sock, 200*time.Millisecond); err == nil {
		_ = c.Close()
		return nil
	}
	if opts.InProcess {
		// In-process: start listener in background of this process.
		// For production CLI, prefer a detached child; tests use InProcess.
		srv := runtime.New(runtime.Options{Workspace: workspace, Version: opts.Version})
		go func() { _ = srv.ListenAndServe(ctx, sock) }()
		// Wait until socket accepts.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if c, err := dialSocket(ctx, sock, 100*time.Millisecond); err == nil {
				_ = c.Close()
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		return errors.New("in-process runtime did not become ready")
	}
	if strings.TrimSpace(opts.RuntimeBinary) == "" {
		return errors.New("runtime binary required outside tests")
	}
	// Detached child: reasonix remote-runtime-workbench --workspace --socket
	cmd := exec.Command(opts.RuntimeBinary, "remote", "runtime-workbench",
		"--workspace", workspace, "--socket", sock, "--version", opts.Version)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := dialSocket(ctx, sock, 200*time.Millisecond); err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("runtime child did not become ready")
}

func dialSocket(ctx context.Context, sock string, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{Timeout: timeout}
	return d.DialContext(ctx, "unix", sock)
}

func proxy(ctx context.Context, stdin io.ReadCloser, reader *bufio.Reader, stdout io.Writer, conn net.Conn) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		// Drain buffered remainder then stdin → conn.
		if reader.Buffered() > 0 {
			buf := make([]byte, reader.Buffered())
			_, _ = reader.Read(buf)
			if _, err := conn.Write(buf); err != nil {
				errCh <- err
				_ = conn.Close()
				return
			}
		}
		_, err := io.Copy(conn, stdin)
		errCh <- err
		_ = conn.Close()
	}()
	go func() {
		defer wg.Done()
		_, err := io.Copy(stdout, conn)
		errCh <- err
		_ = stdin.Close()
	}()
	select {
	case <-ctx.Done():
		_ = conn.Close()
		_ = stdin.Close()
		wg.Wait()
		return ctx.Err()
	case err := <-errCh:
		_ = conn.Close()
		_ = stdin.Close()
		wg.Wait()
		if err == nil || errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func writeRPCError(w io.Writer, id json.RawMessage, code int, message string) error {
	if !json.Valid(bytes.TrimSpace(id)) {
		id = json.RawMessage("null")
	}
	frame := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"error":   map[string]any{"code": code, "message": message},
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}
