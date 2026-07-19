// Package bootstrap starts and manages a detached `reasonix serve` process on
// a remote host over an established SSH connection. It detects the remote
// OS/arch, locates or installs reasonix, launches serve bound to a random
// loopback port with a file-based token (never in argv), and records the
// result under the remote ~/.reasonix/remote so a later reconnect can reuse
// it. V1 targets Linux and macOS remotes.
package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/remote"
	"reasonix/internal/remote/sftpfs"
)

// Conn is the subset of *remote.Client bootstrap needs. *remote.Client
// satisfies it directly; tests inject a fake. bootstrap depends on remote
// (never the reverse), so using remote.ExecResult here introduces no cycle.
type Conn interface {
	Exec(ctx context.Context, cmd string) (remote.ExecResult, error)
	SFTP() (*sftpfs.FS, error)
}

// Install strategies.
const (
	InstallAuto   = "auto"
	InstallNPM    = "npm"
	InstallUpload = "upload"
	InstallNever  = "never"
)

// MinServeVersion is the first reasonix release shipping serve's --port-file
// and --token-file flags. A remote binary older than this is treated as
// missing so it gets upgraded before launch.
const MinServeVersion = "1.0.0"

// Options configures EnsureServe.
type Options struct {
	Workspace   string                    // remote workspace path (may start with ~)
	Install     string                    // auto|npm|upload|never
	LocalBinary string                    // path to the running reasonix binary, for same-platform upload
	LocalGOOS   string                    // GOOS of LocalBinary
	LocalGOARCH string                    // GOARCH of LocalBinary
	MinVersion  string                    // minimum acceptable remote version
	Progress    func(step, detail string) // optional progress callback
	Clock       func() time.Time          // nil => time.Now
}

func (o Options) progress(step, detail string) {
	if o.Progress != nil {
		o.Progress(step, detail)
	}
}

func (o Options) clock() func() time.Time {
	if o.Clock != nil {
		return o.Clock
	}
	return time.Now
}

// Result is the outcome of EnsureServe.
type Result struct {
	State  ServeState
	Token  string // the pre-shared auth token (read from or written to TokenFile)
	Reused bool   // true when an already-running serve was reused
}

// EnsureServe returns a running serve for (host, workspace), starting one if
// needed. It is also the reconnect path: an existing live process is reused.
func EnsureServe(ctx context.Context, conn Conn, opts Options) (Result, error) {
	fs, err := conn.SFTP()
	if err != nil {
		return Result{}, err
	}
	home, err := fs.RealPath(ctx, "~")
	if err != nil {
		return Result{}, fmt.Errorf("bootstrap: resolve remote home: %w", err)
	}
	workspace, err := resolveWorkspace(ctx, fs, opts.Workspace, home)
	if err != nil {
		return Result{}, err
	}
	paths := pathsFor(home, workspace)

	// 1. Reuse a live process if the recorded pid is still running.
	if st, tok, ok := tryReuse(ctx, conn, fs, paths); ok {
		opts.progress("reuse", st.Addr)
		return Result{State: st, Token: tok, Reused: true}, nil
	}

	// 2. Detect remote platform.
	opts.progress("detect", "")
	unameRes, err := conn.Exec(ctx, "uname -sm")
	if err != nil {
		return Result{}, fmt.Errorf("bootstrap: uname: %w", err)
	}
	goos, goarch, err := ParseUname(string(unameRes.Stdout))
	if err != nil {
		return Result{}, err
	}

	// 3. Locate or install a usable reasonix.
	bin, version, err := ensureBinary(ctx, conn, fs, opts, home, goos, goarch, paths)
	if err != nil {
		return Result{}, err
	}

	// 4. Generate token, write it 0600, and launch detached serve.
	token, err := generateToken()
	if err != nil {
		return Result{}, err
	}
	if err := fs.MkdirAll(ctx, paths.Dir); err != nil {
		return Result{}, err
	}
	if err := fs.WriteFileAtomic(ctx, paths.TokenFile, []byte(token+"\n"), 0o600); err != nil {
		return Result{}, fmt.Errorf("bootstrap: write token: %w", err)
	}
	opts.progress("launch", "")
	launchRes, err := conn.Exec(ctx, LaunchCommand(bin, workspace, paths))
	if err != nil {
		return Result{}, fmt.Errorf("bootstrap: launch: %w", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(launchRes.Stdout)))

	// 5. Poll the port file for the real bound address.
	opts.progress("health_check", "")
	addr, err := pollPortFile(ctx, fs, paths.PortFile, opts.clock())
	if err != nil {
		return Result{}, err
	}

	st := ServeState{
		PID:       pid,
		Addr:      addr,
		Workspace: workspace,
		Version:   version,
		TokenFile: paths.TokenFile,
		LogFile:   paths.LogFile,
		StartedAt: nowUnix(opts.clock()),
	}
	data, err := MarshalState(st)
	if err != nil {
		return Result{}, err
	}
	if err := fs.WriteFileAtomic(ctx, paths.StateJSON, data, 0o600); err != nil {
		return Result{}, fmt.Errorf("bootstrap: write state: %w", err)
	}
	opts.progress("ready", addr)
	return Result{State: st, Token: token}, nil
}

// Status reads the recorded state and reports whether the process is alive.
func Status(ctx context.Context, conn Conn, workspace string) (ServeState, bool, error) {
	fs, err := conn.SFTP()
	if err != nil {
		return ServeState{}, false, err
	}
	home, err := fs.RealPath(ctx, "~")
	if err != nil {
		return ServeState{}, false, err
	}
	ws, err := resolveWorkspace(ctx, fs, workspace, home)
	if err != nil {
		return ServeState{}, false, err
	}
	paths := pathsFor(home, ws)
	st, err := readState(ctx, fs, paths.StateJSON)
	if err != nil {
		return ServeState{}, false, nil // no state => not running
	}
	alive := pidAlive(ctx, conn, st.PID)
	return st, alive, nil
}

// Stop terminates the recorded process and removes its state files.
func Stop(ctx context.Context, conn Conn, workspace string) error {
	fs, err := conn.SFTP()
	if err != nil {
		return err
	}
	home, err := fs.RealPath(ctx, "~")
	if err != nil {
		return err
	}
	ws, err := resolveWorkspace(ctx, fs, workspace, home)
	if err != nil {
		return err
	}
	paths := pathsFor(home, ws)
	st, err := readState(ctx, fs, paths.StateJSON)
	if err != nil {
		return nil // nothing recorded
	}
	if st.PID > 0 {
		if _, err := conn.Exec(ctx, StopCommand(st.PID)); err != nil {
			return fmt.Errorf("bootstrap: stop pid %d: %w", st.PID, err)
		}
	}
	_ = fs.Remove(ctx, paths.StateJSON, false)
	_ = fs.Remove(ctx, paths.TokenFile, false)
	_ = fs.Remove(ctx, paths.PortFile, false)
	_ = fs.Remove(ctx, paths.PidFile, false)
	return nil
}

// Logs writes up to n tail lines of the serve log to w.
func Logs(ctx context.Context, conn Conn, workspace string, n int, w io.Writer) error {
	fs, err := conn.SFTP()
	if err != nil {
		return err
	}
	home, err := fs.RealPath(ctx, "~")
	if err != nil {
		return err
	}
	ws, err := resolveWorkspace(ctx, fs, workspace, home)
	if err != nil {
		return err
	}
	paths := pathsFor(home, ws)
	res, err := conn.Exec(ctx, LogsCommand(paths.LogFile, n))
	if err != nil {
		return err
	}
	_, err = w.Write(res.Stdout)
	return err
}

func tryReuse(ctx context.Context, conn Conn, fs *sftpfs.FS, paths StatePaths) (ServeState, string, bool) {
	st, err := readState(ctx, fs, paths.StateJSON)
	if err != nil || st.PID <= 0 || st.Addr == "" {
		return ServeState{}, "", false
	}
	if !pidAlive(ctx, conn, st.PID) {
		return ServeState{}, "", false
	}
	tok, err := readToken(ctx, fs, st.TokenFile)
	if err != nil {
		return ServeState{}, "", false
	}
	return st, tok, true
}

func pidAlive(ctx context.Context, conn Conn, pid int) bool {
	if pid <= 0 {
		return false
	}
	res, err := conn.Exec(ctx, AliveCommand(pid))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(res.Stdout)) == "1"
}

func readState(ctx context.Context, fs *sftpfs.FS, path string) (ServeState, error) {
	data, _, _, err := fs.ReadFile(ctx, path, 1<<20)
	if err != nil {
		return ServeState{}, err
	}
	return UnmarshalState(data)
}

func readToken(ctx context.Context, fs *sftpfs.FS, path string) (string, error) {
	data, _, _, err := fs.ReadFile(ctx, path, 64<<10)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", errors.New("bootstrap: empty token file")
	}
	return tok, nil
}

func pollPortFile(ctx context.Context, fs *sftpfs.FS, portFile string, clock func() time.Time) (string, error) {
	deadline := clock().Add(20 * time.Second)
	for {
		data, _, _, err := fs.ReadFile(ctx, portFile, 128)
		if err == nil {
			addr := strings.TrimSpace(string(data))
			if addr != "" {
				return addr, nil
			}
		}
		if clock().After(deadline) {
			return "", errors.New("bootstrap: timed out waiting for serve to report its port")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func resolveWorkspace(ctx context.Context, fs *sftpfs.FS, workspace, home string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return home, nil
	}
	if workspace == "~" {
		return home, nil
	}
	if strings.HasPrefix(workspace, "~/") {
		return strings.TrimRight(home, "/") + "/" + strings.TrimPrefix(workspace, "~/"), nil
	}
	if strings.HasPrefix(workspace, "/") {
		return workspace, nil
	}
	// Relative to home.
	return strings.TrimRight(home, "/") + "/" + workspace, nil
}

func generateToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
