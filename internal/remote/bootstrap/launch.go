package bootstrap

import (
	"fmt"
	"strings"
)

// StatePaths are the absolute remote-side paths for one workspace's serve
// state. All are under ~/.reasonix/remote.
type StatePaths struct {
	Dir       string // ~/.reasonix/remote
	StateJSON string
	TokenFile string
	LogFile   string
	PortFile  string
	PidFile   string
}

// shellQuote wraps s in single quotes safe for POSIX sh, escaping embedded
// single quotes as '\”. This is the only quoting used for remote command
// operands; every interpolated path/workspace passes through it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// LaunchCommand builds the `sh -c` script that starts a detached serve in
// workspace, writing the port/pid files and appending output to the log. The
// binary path and every operand are single-quote-escaped so hostile paths
// (spaces, quotes, `; rm -rf ~`) cannot break out.
//
// It echoes the shell's $! so the caller can record the pid immediately, and
// redirects stdin from /dev/null so the process fully detaches.
func LaunchCommand(bin, workspace string, p StatePaths) string {
	return fmt.Sprintf(
		"mkdir -p %s && cd %s && setsid nohup %s serve --addr 127.0.0.1:0 --auth token --token-file %s --port-file %s --pid-file %s </dev/null >>%s 2>&1 & echo $!",
		shellQuote(p.Dir),
		shellQuote(workspace),
		shellQuote(bin),
		shellQuote(p.TokenFile),
		shellQuote(p.PortFile),
		shellQuote(p.PidFile),
		shellQuote(p.LogFile),
	)
}

// StopCommand builds a script that TERMs the pid, waits up to ~5s, then KILLs
// if still alive. pid is validated numeric by the caller.
func StopCommand(pid int) string {
	return fmt.Sprintf(
		"kill -TERM %d 2>/dev/null; for i in 1 2 3 4 5; do kill -0 %d 2>/dev/null || exit 0; sleep 1; done; kill -KILL %d 2>/dev/null; exit 0",
		pid, pid, pid,
	)
}

// AliveCommand reports whether pid is running (prints "1" if alive).
func AliveCommand(pid int) string {
	return fmt.Sprintf("kill -0 %d 2>/dev/null && echo 1 || echo 0", pid)
}

// LogsCommand tails n lines of the log file (n<=0 => 200).
func LogsCommand(logFile string, n int) string {
	if n <= 0 {
		n = 200
	}
	return fmt.Sprintf("tail -n %d %s 2>/dev/null || true", n, shellQuote(logFile))
}

// LocateCommand probes for a usable reasonix binary and its version. It prints
// two lines: the resolved path (or empty) and the `--version` output.
func LocateCommand(uploadedBin string) string {
	return fmt.Sprintf(
		"BIN=\"$(command -v reasonix 2>/dev/null)\"; "+
			"if [ -z \"$BIN\" ] && [ -x %s ]; then BIN=%s; fi; "+
			"if [ -z \"$BIN\" ]; then P=\"$(npm prefix -g 2>/dev/null)\"; if [ -n \"$P\" ] && [ -x \"$P/bin/reasonix\" ]; then BIN=\"$P/bin/reasonix\"; fi; fi; "+
			"echo \"$BIN\"; if [ -n \"$BIN\" ]; then \"$BIN\" --version 2>/dev/null; fi",
		shellQuote(uploadedBin), shellQuote(uploadedBin),
	)
}
