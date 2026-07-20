package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/remote/sftpfs"
	"reasonix/internal/store"
)

// RuntimeOptions configures EnsureRemoteRuntime. It embeds Options so binary
// install/version policy stays shared with serve bootstrap.
type RuntimeOptions struct {
	Options
	// BrokerURL is the reverse-tunneled Provider Broker base URL as seen on the
	// remote host (typically http://127.0.0.1:<port>). Empty disables broker flags.
	BrokerURL string
	// BrokerToken is written to a 0600 remote file and passed via --broker-token-file.
	BrokerToken string
}

// runtimeDir is ~/.reasonix/remote-runtime on the remote host.
func runtimeDir(home string) string {
	return path.Join(home, ".reasonix", store.RemoteRuntimeDirName)
}

// runtimePathsFor derives per-workspace remote-runtime state paths.
func runtimePathsFor(home, workspace string) StatePaths {
	dir := runtimeDir(home)
	slug := store.RemoteWorkspaceSlug(workspace)
	return StatePaths{
		Dir:       dir,
		StateJSON: path.Join(dir, store.RemoteRuntimeStateName(slug)),
		TokenFile: path.Join(dir, store.RemoteRuntimeTokenName(slug)),
		LogFile:   path.Join(dir, store.RemoteRuntimeLogName(slug)),
		PortFile:  path.Join(dir, store.RemoteRuntimePortName(slug)),
		PidFile:   path.Join(dir, store.RemoteRuntimePidName(slug)),
		LockDir:   path.Join(dir, store.RemoteRuntimeLockName(slug)),
		LockOwner: path.Join(dir, store.RemoteRuntimeLockName(slug), "owner"),
	}
}

// brokerTokenPath is the remote file holding the Provider Broker capability token.
func brokerTokenPath(home, workspace string) string {
	slug := store.RemoteWorkspaceSlug(workspace)
	return path.Join(runtimeDir(home), store.RemoteRuntimeBrokerTokenName(slug))
}

// LaunchRemoteRuntimeCommand builds the detached remote-runtime start script.
func LaunchRemoteRuntimeCommand(bin, workspace string, p StatePaths, brokerURL, brokerTokenFile string) string {
	extra := ""
	if strings.TrimSpace(brokerURL) != "" {
		extra += " --broker-url " + shellQuote(brokerURL)
	}
	if strings.TrimSpace(brokerTokenFile) != "" {
		extra += " --broker-token-file " + shellQuote(brokerTokenFile)
	}
	return fmt.Sprintf(
		"mkdir -p %s && cd %s && rm -f %s %s && umask 077 && : >>%s && chmod 600 %s && "+
			"SX=; command -v setsid >/dev/null 2>&1 && SX=setsid; "+
			"$SX nohup %s remote-runtime --addr 127.0.0.1:0 --workspace %s --token-file %s --port-file %s --pid-file %s%s </dev/null >>%s 2>&1 & echo $!",
		shellQuote(p.Dir),
		shellQuote(workspace),
		shellQuote(p.PortFile),
		shellQuote(p.PidFile),
		shellQuote(p.LogFile),
		shellQuote(p.LogFile),
		shellQuote(bin),
		shellQuote(workspace),
		shellQuote(p.TokenFile),
		shellQuote(p.PortFile),
		shellQuote(p.PidFile),
		extra,
		shellQuote(p.LogFile),
	)
}

// RuntimeAliveCommand prints "1" only when pid is a reasonix remote-runtime process.
func RuntimeAliveCommand(pid int, p StatePaths) string {
	return fmt.Sprintf(
		"T=%s; P=%s; kill -0 %d 2>/dev/null || { echo 0; exit 0; }; "+
			"A=$(ps -p %d -o args= 2>/dev/null || ps -p %d -o command= 2>/dev/null); "+
			"case \"$A\" in *reasonix*remote-runtime*\"$T\"*\"$P\"*) echo 1;; *) echo 0;; esac",
		shellQuote(p.TokenFile), shellQuote(p.PortFile), pid, pid, pid,
	)
}

// StopRemoteRuntimeCommand terminates a remote-runtime process safely.
func StopRemoteRuntimeCommand(pid int, p StatePaths) string {
	return fmt.Sprintf(
		"T=%s; P=%s; ours() { A=$(ps -p %d -o args= 2>/dev/null || ps -p %d -o command= 2>/dev/null); "+
			"case \"$A\" in *reasonix*remote-runtime*\"$T\"*\"$P\"*) return 0;; *) return 1;; esac; }; "+
			"ours || exit 0; kill -TERM %d 2>/dev/null; "+
			"for i in 1 2 3 4 5; do kill -0 %d 2>/dev/null || exit 0; ours || exit 0; sleep 1; done; "+
			"ours && kill -KILL %d 2>/dev/null; exit 0",
		shellQuote(p.TokenFile), shellQuote(p.PortFile), pid, pid, pid, pid, pid,
	)
}

// runtimePortFileMarker is grepped from `remote-runtime --help`.
const runtimePortFileMarker = "port-file"

// LocateRemoteRuntimeCommand probes for a binary that supports remote-runtime
// with --port-file/--token-file. Prints path, version, and runtime:yes|no.
func LocateRemoteRuntimeCommand(uploadedBin string) string {
	return fmt.Sprintf(
		"BIN=\"$(command -v reasonix 2>/dev/null)\"; "+
			"if [ -z \"$BIN\" ] && [ -x %s ]; then BIN=%s; fi; "+
			"if [ -z \"$BIN\" ]; then P=\"$(npm prefix -g 2>/dev/null)\"; if [ -n \"$P\" ] && [ -x \"$P/bin/reasonix\" ]; then BIN=\"$P/bin/reasonix\"; fi; fi; "+
			"echo \"$BIN\"; "+
			"if [ -n \"$BIN\" ]; then \"$BIN\" --version 2>/dev/null; "+
			"if \"$BIN\" remote-runtime --help 2>&1 | grep -q -- %s; then echo runtime:yes; else echo runtime:no; fi; fi",
		shellQuote(uploadedBin), shellQuote(uploadedBin), shellQuote(runtimePortFileMarker),
	)
}

// EnsureRemoteRuntime returns a running remote-runtime for (host, workspace).
// State lives under ~/.reasonix/remote-runtime/ and never collides with serve.
func EnsureRemoteRuntime(ctx context.Context, conn Conn, opts RuntimeOptions) (Result, error) {
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
	paths := runtimePathsFor(home, workspace)

	// 1. Reuse a live remote-runtime when possible.
	// When a Provider Broker reverse tunnel is involved, never reuse: the running
	// process cached broker URL/token at start, so rewriting the token file alone
	// would leave streams aimed at the previous reverse-bound port.
	if st, tok, ok := tryReuseRuntime(ctx, conn, fs, paths, workspace); ok {
		if strings.TrimSpace(opts.BrokerURL) != "" || strings.TrimSpace(opts.BrokerToken) != "" {
			opts.progress("restart_broker", "broker endpoint changed; restarting remote-runtime")
			_ = StopRemoteRuntime(ctx, conn, workspace)
		} else {
			opts.progress("reuse", st.Addr)
			return Result{State: st, Token: tok, Reused: true}, nil
		}
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

	// 3. Locate or install a binary that supports remote-runtime.
	bin, version, err := ensureRuntimeBinary(ctx, conn, fs, opts.Options, home, goos, goarch)
	if err != nil {
		return Result{}, err
	}

	// 4. Serialize launch/publish.
	opts.progress("waiting_lock", "")
	lock, err := acquireServeLock(ctx, fs, paths, opts.clock())
	if err != nil {
		return Result{}, err
	}
	defer lock.release()
	if st, tok, ok := tryReuseRuntime(ctx, conn, fs, paths, workspace); ok {
		if strings.TrimSpace(opts.BrokerURL) != "" || strings.TrimSpace(opts.BrokerToken) != "" {
			opts.progress("restart_broker", "broker endpoint changed; restarting remote-runtime")
			_ = StopRemoteRuntime(ctx, conn, workspace)
		} else {
			opts.progress("reuse", st.Addr)
			return Result{State: st, Token: tok, Reused: true}, nil
		}
	}

	// 5. Write runtime auth token + optional broker token.
	token, err := generateToken()
	if err != nil {
		return Result{}, err
	}
	if err := fs.MkdirAll(ctx, paths.Dir); err != nil {
		return Result{}, err
	}
	if err := fs.WriteFileAtomic(ctx, paths.TokenFile, []byte(token+"\n"), 0o600); err != nil {
		return Result{}, fmt.Errorf("bootstrap: write runtime token: %w", err)
	}
	brokerTokPath := ""
	if strings.TrimSpace(opts.BrokerToken) != "" {
		brokerTokPath = brokerTokenPath(home, workspace)
		if err := writeBrokerTokenFile(ctx, fs, home, workspace, opts.BrokerToken); err != nil {
			return Result{}, err
		}
	}

	// 6. Launch detached remote-runtime.
	opts.progress("launch", "")
	launchRes, err := conn.Exec(ctx, LaunchRemoteRuntimeCommand(bin, workspace, paths, opts.BrokerURL, brokerTokPath))
	if err != nil {
		cleanupFailedRuntimeLaunch(conn, fs, paths, 0)
		return Result{}, fmt.Errorf("bootstrap: launch remote-runtime: %w", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(launchRes.Stdout)))

	opts.progress("health_check", "")
	addr, err := pollPortFile(ctx, fs, paths.PortFile, opts.clock())
	if err != nil {
		cleanupFailedRuntimeLaunch(conn, fs, paths, pid)
		return Result{}, err
	}
	if filePID, perr := readPIDFile(ctx, fs, paths.PidFile); perr == nil {
		pid = filePID
	}
	if pid <= 0 || !pidIsRuntime(ctx, conn, pid, paths) {
		cleanupFailedRuntimeLaunch(conn, fs, paths, pid)
		return Result{}, errors.New("bootstrap: launched process did not become the expected reasonix remote-runtime")
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
		cleanupFailedRuntimeLaunch(conn, fs, paths, pid)
		return Result{}, err
	}
	if err := fs.WriteFileAtomic(ctx, paths.StateJSON, data, 0o600); err != nil {
		cleanupFailedRuntimeLaunch(conn, fs, paths, pid)
		return Result{}, fmt.Errorf("bootstrap: write runtime state: %w", err)
	}
	opts.progress("ready", addr)
	return Result{State: st, Token: token}, nil
}

// StopRemoteRuntime terminates the recorded remote-runtime process.
func StopRemoteRuntime(ctx context.Context, conn Conn, workspace string) error {
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
	paths := runtimePathsFor(home, ws)
	st, err := readState(ctx, fs, paths.StateJSON)
	if err != nil {
		return nil
	}
	if st.PID > 0 {
		if _, err := conn.Exec(ctx, StopRemoteRuntimeCommand(st.PID, paths)); err != nil {
			return fmt.Errorf("bootstrap: stop remote-runtime pid %d: %w", st.PID, err)
		}
	}
	_ = fs.Remove(ctx, paths.StateJSON, false)
	_ = fs.Remove(ctx, paths.TokenFile, false)
	_ = fs.Remove(ctx, paths.PortFile, false)
	_ = fs.Remove(ctx, paths.PidFile, false)
	_ = fs.Remove(ctx, brokerTokenPath(home, ws), false)
	return nil
}

// RuntimeLogs tails the remote-runtime log.
func RuntimeLogs(ctx context.Context, conn Conn, workspace string, n int, w interface{ Write([]byte) (int, error) }) error {
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
	paths := runtimePathsFor(home, ws)
	res, err := conn.Exec(ctx, LogsCommand(paths.LogFile, n))
	if err != nil {
		return err
	}
	_, err = w.Write(res.Stdout)
	return err
}

func writeBrokerTokenFile(ctx context.Context, fs *sftpfs.FS, home, workspace, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	path := brokerTokenPath(home, workspace)
	if err := fs.MkdirAll(ctx, runtimeDir(home)); err != nil {
		return err
	}
	if err := fs.WriteFileAtomic(ctx, path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("bootstrap: write broker token: %w", err)
	}
	return nil
}

func tryReuseRuntime(ctx context.Context, conn Conn, fs *sftpfs.FS, paths StatePaths, workspace string) (ServeState, string, bool) {
	st, err := readState(ctx, fs, paths.StateJSON)
	if err != nil || st.PID <= 0 || st.Addr == "" {
		return ServeState{}, "", false
	}
	if st.Workspace != workspace {
		return ServeState{}, "", false
	}
	if !validServeAddr(st.Addr) || !pidIsRuntime(ctx, conn, st.PID, paths) {
		return ServeState{}, "", false
	}
	tok, err := readToken(ctx, fs, paths.TokenFile)
	if err != nil {
		return ServeState{}, "", false
	}
	return st, tok, true
}

func pidIsRuntime(ctx context.Context, conn Conn, pid int, paths StatePaths) bool {
	if pid <= 0 {
		return false
	}
	res, err := conn.Exec(ctx, RuntimeAliveCommand(pid, paths))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(res.Stdout)) == "1"
}

func cleanupFailedRuntimeLaunch(conn Conn, fs *sftpfs.FS, paths StatePaths, pid int) {
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	if pid <= 0 {
		pid, _ = readPIDFile(ctx, fs, paths.PidFile)
	}
	if pid > 0 {
		_, _ = conn.Exec(ctx, StopRemoteRuntimeCommand(pid, paths))
	}
	_ = fs.Remove(ctx, paths.StateJSON, false)
	_ = fs.Remove(ctx, paths.TokenFile, false)
	_ = fs.Remove(ctx, paths.PortFile, false)
	_ = fs.Remove(ctx, paths.PidFile, false)
}

func ensureRuntimeBinary(ctx context.Context, conn Conn, fs *sftpfs.FS, opts Options, home, goos, goarch string) (bin, version string, err error) {
	uploaded := uploadedBinPath(home)
	bin, version = locateRuntime(ctx, conn, uploaded)
	if bin != "" {
		return bin, version, nil
	}
	strategy := opts.Install
	if strategy == "" {
		strategy = InstallAuto
	}
	opts.progress("install", strategy)
	switch strategy {
	case InstallNever:
		return "", "", fmt.Errorf("bootstrap: reasonix remote-runtime not found on remote and serve_install = never")
	case InstallNPM:
		_, _ = conn.Exec(ctx, "npm i -g reasonix 2>&1")
	case InstallUpload:
		if err := uploadBinaryRaw(ctx, fs, opts, home, goos, goarch, uploaded); err != nil {
			return "", "", err
		}
	default: // auto: npm then upload
		_, _ = conn.Exec(ctx, "npm i -g reasonix 2>&1")
		if bin, version = locateRuntime(ctx, conn, uploaded); bin != "" {
			return bin, version, nil
		}
		if err := uploadBinaryRaw(ctx, fs, opts, home, goos, goarch, uploaded); err != nil && opts.LocalBinary == "" {
			return "", "", fmt.Errorf("%w; bootstrap: no local Reasonix CLI is available for upload", err)
		}
	}
	bin, version = locateRuntime(ctx, conn, uploaded)
	if bin == "" {
		return "", "", fmt.Errorf("bootstrap: reasonix remote-runtime not available on remote (install a matching Reasonix build)")
	}
	return bin, version, nil
}

// uploadBinaryRaw copies the local CLI binary without the serve --port-file probe
// (remote-runtime is the gate for desktop remote).
func uploadBinaryRaw(ctx context.Context, fs *sftpfs.FS, opts Options, home, goos, goarch, uploaded string) error {
	if opts.LocalBinary == "" {
		return fmt.Errorf("bootstrap: upload strategy needs the local reasonix binary path")
	}
	if opts.LocalGOOS != goos || opts.LocalGOARCH != goarch {
		return fmt.Errorf("bootstrap: cannot upload: local binary is %s/%s but remote is %s/%s; use serve_install = npm",
			opts.LocalGOOS, opts.LocalGOARCH, goos, goarch)
	}
	data, err := os.ReadFile(opts.LocalBinary)
	if err != nil {
		return fmt.Errorf("bootstrap: read local binary: %w", err)
	}
	if err := fs.MkdirAll(ctx, dirOf(uploaded)); err != nil {
		return err
	}
	if err := fs.WriteFileAtomic(ctx, uploaded, data, 0o755); err != nil {
		return fmt.Errorf("bootstrap: upload binary: %w", err)
	}
	return nil
}

func locateRuntime(ctx context.Context, conn Conn, uploaded string) (bin, version string) {
	res, err := conn.Exec(ctx, LocateRemoteRuntimeCommand(uploaded))
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.TrimRight(string(res.Stdout), "\n"), "\n")
	if len(lines) == 0 {
		return "", ""
	}
	path := strings.TrimSpace(lines[0])
	if path == "" {
		return "", ""
	}
	supports := false
	for _, ln := range lines[1:] {
		ln = strings.TrimSpace(ln)
		switch {
		case ln == "runtime:yes":
			supports = true
		case ln == "runtime:no":
			supports = false
		default:
			if v, verr := ParseVersion(ln); verr == nil {
				version = v
			}
		}
	}
	if !supports {
		return "", ""
	}
	return path, version
}
