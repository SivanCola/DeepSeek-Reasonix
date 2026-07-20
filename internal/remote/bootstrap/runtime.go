package bootstrap

import (
	"context"
	"fmt"
	"strings"
)

// RuntimeStatePaths are remote-side paths for remote-runtime (separate from serve).
type RuntimeStatePaths struct {
	Dir       string
	StateJSON string
	TokenFile string
	LogFile   string
	PortFile  string
	PidFile   string
	LockDir   string
}

// RuntimePathsFor builds paths under ~/.reasonix/remote-runtime/ (separate from
// serve state under ~/.reasonix/remote/) so PID/port/token files never collide.
func RuntimePathsFor(home, workspace string) RuntimeStatePaths {
	p := pathsFor(home, workspace)
	// pathsFor Dir is ~/.reasonix/remote; swap the final "remote" segment.
	dir := strings.Replace(p.Dir, "/.reasonix/remote", "/.reasonix/remote-runtime", 1)
	if dir == p.Dir {
		dir = p.Dir + "-runtime"
	}
	return RuntimeStatePaths{
		Dir:       dir,
		StateJSON: dir + "/state.json",
		TokenFile: dir + "/token",
		LogFile:   dir + "/runtime.log",
		PortFile:  dir + "/port",
		PidFile:   dir + "/pid",
		LockDir:   dir + "/lock",
	}
}

// LaunchRemoteRuntimeCommand builds the detached remote-runtime start script.
// Broker URL/token are optional; when set they point at the reverse-tunneled
// local Provider Broker bound on remote 127.0.0.1.
func LaunchRemoteRuntimeCommand(bin, workspace string, p RuntimeStatePaths, brokerURL, brokerTokenFile string) string {
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

// RuntimeAliveCommand checks that pid is a reasonix remote-runtime process.
func RuntimeAliveCommand(pid int, p RuntimeStatePaths) string {
	return fmt.Sprintf(
		"T=%s; P=%s; kill -0 %d 2>/dev/null || { echo 0; exit 0; }; "+
			"A=$(ps -p %d -o args= 2>/dev/null || ps -p %d -o command= 2>/dev/null); "+
			"case \"$A\" in *reasonix*remote-runtime*\"$T\"*\"$P\"*) echo 1;; *) echo 0;; esac",
		shellQuote(p.TokenFile), shellQuote(p.PortFile), pid, pid, pid,
	)
}

// StopRemoteRuntimeCommand stops a remote-runtime process.
func StopRemoteRuntimeCommand(pid int, p RuntimeStatePaths) string {
	return fmt.Sprintf(
		"T=%s; P=%s; ours() { A=$(ps -p %d -o args= 2>/dev/null || ps -p %d -o command= 2>/dev/null); "+
			"case \"$A\" in *reasonix*remote-runtime*\"$T\"*\"$P\"*) return 0;; *) return 1;; esac; }; "+
			"ours || exit 0; kill -TERM %d 2>/dev/null; "+
			"for i in 1 2 3 4 5; do kill -0 %d 2>/dev/null || exit 0; ours || exit 0; sleep 1; done; "+
			"ours && kill -KILL %d 2>/dev/null; exit 0",
		shellQuote(p.TokenFile), shellQuote(p.PortFile), pid, pid, pid, pid, pid,
	)
}

// EnsureRemoteRuntime is a thin adapter: for now it reuses EnsureServe's binary
// install path by launching remote-runtime via the same ensure flow when the
// remote binary supports the subcommand. Callers that only need the launch
// script use LaunchRemoteRuntimeCommand directly.
//
// Full ensure/reuse parity with EnsureServe lands with remote desktop E2E; this
// helper documents the intended entry point.
func EnsureRemoteRuntime(ctx context.Context, conn Conn, opts Options) (Result, error) {
	// Delegate to EnsureServe-shaped install, but the launch is remote-runtime.
	// Implementation strategy: install binary via ensureBinary then launch
	// remote-runtime. For the initial kernel PR we call EnsureServe for binary
	// placement and then prefer remote-runtime when the flag is present.
	//
	// To avoid dual daemons, production OpenRemoteWorkspace stops serve and
	// starts remote-runtime. Here we still use EnsureServe for install+locate
	// so version matching works, then the desktop replaces the process.
	return EnsureServe(ctx, conn, opts)
}
