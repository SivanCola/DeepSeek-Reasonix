// Package resourcebudget admits workloads against a user-wide cooperative
// CPU/memory budget. OS locks own lifetimes; FIFO tickets own admission order.
package resourcebudget

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/syntax"

	"reasonix/internal/filelock"
	"reasonix/internal/shellparse"
)

const (
	ownerEnv = "REASONIX_RESOURCE_LEASE"
	scopeEnv = "REASONIX_RESOURCE_SCOPE"
)

var workerVars = []string{"GOMAXPROCS", "CARGO_BUILD_JOBS", "CMAKE_BUILD_PARALLEL_LEVEL", "CTEST_PARALLEL_LEVEL", "VITEST_MAX_WORKERS", "VITEST_MAX_THREADS", "VITEST_MAX_FORKS"}

type directoryKey struct{}
type waitKey struct{}

// WithWaitNotice reports admission waits without turning queue time into
// captured command output or blocking a UI mutex.
func WithWaitNotice(ctx context.Context, notice func()) context.Context {
	return context.WithValue(ctx, waitKey{}, notice)
}

// WithDirectory isolates simulations and tests from the real user's budget.
func WithDirectory(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, directoryKey{}, dir)
}

// InheritDirectory preserves a caller's isolated scheduling scope when jobs
// transfer execution to a session-lifetime context.
func InheritDirectory(target, source context.Context) context.Context {
	if r, ok := source.Value(requestKey{}).(Resources); ok {
		target = WithRequest(target, r)
	}
	if dir, _ := source.Value(directoryKey{}).(string); dir != "" {
		return WithDirectory(target, dir)
	}
	return target
}

func directory(ctx context.Context) (string, error) {
	if dir, _ := ctx.Value(directoryKey{}).(string); dir != "" {
		return dir, nil
	}
	// Sandboxed children receive private cache directories. Their inherited
	// resource scope must still point to the physical host's shared owner.
	if dir := os.Getenv(scopeEnv); filepath.IsAbs(dir) {
		return filepath.Clean(dir), nil
	}
	// Deliberately independent of REASONIX_HOME, cwd, session and worktree.
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "reasonix", "resource-budget-v1"), nil
}

// Heavy conservatively schedules unknown scripts too: a wrapper can start an
// arbitrarily large worker pool. Simple diagnostics and recognized service
// dependencies bypass the batch lane.
func Heavy(command string) bool {
	file, err := shellparse.ParseBash(command)
	if err != nil || len(file.Stmts) == 0 {
		return true
	}
	heavy := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if heavy {
			return false
		}
		switch n := node.(type) {
		case *syntax.WhileClause, *syntax.ForClause, *syntax.FuncDecl, *syntax.CmdSubst, *syntax.ProcSubst:
			heavy = true
		case *syntax.CallExpr:
			var args []string
			for _, arg := range n.Args {
				word, ok := shellparse.StaticWord(arg)
				if !ok {
					heavy = true
					return false
				}
				args = append(args, word)
			}
			if len(args) > 0 {
				heavy = heavyCall(args)
			}
		}
		return !heavy
	})
	return heavy
}

func heavyCall(args []string) bool {
	// Development servers are dependencies of checks and may live for hours;
	// they must not occupy the batch lane for their entire service lifetime.
	if service(args) {
		return false
	}
	switch args[0] {
	case "pwd", "cd", "echo", "printf", "true", "false", "sleep", "rg", "cat", "head", "tail", "ls", "wc":
		return false
	case "git":
		if len(args) < 2 {
			return true
		}
		switch args[1] {
		case "status", "diff", "show", "log", "rev-parse", "ls-files":
			for _, arg := range args[2:] {
				if arg == "--ext-diff" || arg == "--textconv" || strings.HasPrefix(arg, "--exec") {
					return true
				}
			}
			return false
		}
	}
	return true
}

func service(args []string) bool {
	name := strings.ToLower(filepath.Base(args[0]))
	name = strings.TrimSuffix(strings.TrimSuffix(name, ".cmd"), ".exe")
	if name == "npm" || name == "pnpm" || name == "yarn" || name == "bun" {
		for i := 1; i < len(args); i++ {
			if args[i] == "--dir" || args[i] == "-C" || args[i] == "--prefix" || args[i] == "--cwd" {
				i++
				continue
			}
			if strings.HasPrefix(args[i], "-") || args[i] == "run" {
				continue
			}
			return args[i] == "dev" || args[i] == "start" || args[i] == "serve"
		}
	}
	return name == "vite" && (len(args) == 1 || args[1] != "build" && args[1] != "optimize") || name == "uvicorn" || name == "http-server"
}

// Lease owns both admission and a unique inherited token. Descendant runners
// may reuse a live token, but a stale token cannot bypass a new owner's lane.
type Lease struct {
	cores   int
	token   string
	dir     string
	release func()
	once    sync.Once
}

func Acquire(ctx context.Context, command string, env []string) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !Heavy(command) {
		return &Lease{}, nil
	}
	dir, err := directory(ctx)
	if err != nil {
		return nil, err
	}
	if env == nil {
		env = os.Environ()
	}
	if token := value(env, ownerEnv); liveToken(dir, token) {
		return inheritedLease(dir, token)
	}
	// Explicit/filtered child environments must not erase the current host's
	// own inherited admission and deadlock nested check runners.
	if token := os.Getenv(ownerEnv); liveToken(dir, token) {
		return inheritedLease(dir, token)
	}
	return acquireQueued(ctx, dir, env)
}

func liveToken(dir, token string) bool {
	if len(token) != 32 {
		return false
	}
	if _, err := hex.DecodeString(token); err != nil {
		return false
	}
	path := filepath.Join(dir, token+".lease")
	held, err := filelock.ExclusiveHeld(path)
	return err == nil && held
}

func (l *Lease) Release() {
	if l != nil {
		l.once.Do(func() {
			if l.release != nil {
				l.release()
			}
		})
	}
}

// Env bounds cooperative worker pools without increasing explicit smaller
// limits or discarding unrelated flags. This is admission, not an OS CPU quota.
func (l *Lease) Env(env []string) []string {
	if l == nil || l.cores == 0 {
		return env
	}
	if env == nil {
		env = os.Environ()
	}
	env = append([]string(nil), env...)
	flags := value(env, "GOFLAGS")
	goJobs := min(2, l.cores)
	appendParallel := true
	if last := goPackageLimit(flags); last > 0 {
		if last > 0 && last <= goJobs {
			goJobs = last
			appendParallel = false
		}
	}
	for _, name := range workerVars {
		n := l.cores
		// Go starts independent test binaries/compilers: package parallelism
		// multiplied by per-process GOMAXPROCS must fit the same budget.
		if name == "GOMAXPROCS" {
			n = max(1, l.cores/goJobs)
		}
		if old := workerLimit(value(env, name)); old > 0 {
			n = min(n, old)
		}
		env = set(env, name, strconv.Itoa(n))
	}
	for _, pool := range []string{"THREADS", "FORKS"} {
		n := 1
		if old := workerLimit(value(env, "VITEST_MIN_"+pool)); old > 0 {
			n = min(old, workerLimit(value(env, "VITEST_MAX_"+pool)))
		}
		env = set(env, "VITEST_MIN_"+pool, strconv.Itoa(n))
	}
	if appendParallel {
		env = set(env, "GOFLAGS", strings.TrimSpace(flags+" -p="+strconv.Itoa(goJobs)))
	}
	if l.token != "" {
		env = set(env, ownerEnv, l.token)
	}
	if l.dir != "" {
		env = set(env, scopeEnv, l.dir)
	}
	return env
}

// GOFLAGS uses quoted fields. A regexp could mistake a linker argument inside
// -ldflags for the global package limit and silently omit admission's cap.
func goPackageLimit(flags string) int {
	fields, reason := shellparse.StaticFields("go " + flags)
	if reason != "" {
		return 0
	}
	last := 0
	for i := 1; i < len(fields); i++ {
		if raw, ok := strings.CutPrefix(fields[i], "-p="); ok {
			last, _ = strconv.Atoi(raw)
		}
		if fields[i] == "-p" && i+1 < len(fields) {
			i++
			last, _ = strconv.Atoi(fields[i])
		}
	}
	return last
}

// Command scopes per-command admission metadata to eval instead of the PTY
// startup environment. Changing startup Env would rebuild the shell and lose
// cwd/exports on every lease change. The user's command still runs in that
// same shell, while expired tokens never remain in its environment.
func (l *Lease) Command(command string) string {
	if l == nil || l.cores == 0 {
		return command
	}
	// Persistent shells keep their identity and user exports across changing
	// reservations. Clamp current values in place, never raise a smaller user
	// limit, and avoid temporary assignments that undo an export inside eval.
	var prefix strings.Builder
	for _, name := range workerVars {
		// Vitest accepts percentage limits. Convert their integer part
		// conservatively (round down), then apply the task's absolute cap.
		fmt.Fprintf(&prefix, "case ${%s-} in *%%) __reasonix_pct=${%s%%?}; __reasonix_pct=${__reasonix_pct%%%%.*}; case $__reasonix_pct in ''|*[!0-9]*) ;; *) while [ \"${__reasonix_pct#0}\" != \"$__reasonix_pct\" ]; do __reasonix_pct=${__reasonix_pct#0}; done; export %s=$((%d * ${__reasonix_pct:-0} / 100));; esac;; esac; ", name, name, name, runtime.NumCPU())
		fmt.Fprintf(&prefix, "case ${%s-} in ''|*[!0-9]*) export %s=%d;; *) if [ \"$%s\" -gt %d ]; then export %s=%d; elif [ \"$%s\" -lt 1 ]; then export %s=1; fi;; esac; ", name, name, l.cores, name, l.cores, name, l.cores, name, name)
	}
	for _, pool := range []string{"THREADS", "FORKS"} {
		name, upper := "VITEST_MIN_"+pool, "VITEST_MAX_"+pool
		fmt.Fprintf(&prefix, "case ${%s-} in ''|*[!0-9]*) export %s=1;; *) if [ \"$%s\" -gt \"$%s\" ] || [ \"$%s\" -lt 1 ]; then export %s=1; fi;; esac; ", name, name, name, upper, name, name)
	}
	// Package workers multiply per-process Go parallelism. A persistent shell
	// uses one package at a time so later exported GOFLAGS cannot multiply it.
	prefix.WriteString("unset __reasonix_pct; case ${GOFLAGS-} in *' -p=1'|'-p=1') ;; *) export GOFLAGS=\"${GOFLAGS-} -p=1\";; esac; ")
	return prefix.String() + ownerEnv + "=" + quote(l.token) + " " + scopeEnv + "=" + quote(l.dir) + " eval -- " + quote(command)
}

func workerCores() int { return max(1, runtime.NumCPU()/2) }

// PersistentEnv is a stable host-sized baseline, independent of lease tokens
// and policy edits. Command lowers it to the admitted request. Starting at the
// host ceiling permits configured budgets above the default half-core budget;
// smaller explicit user values still survive.
func PersistentEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	bounded := (&Lease{cores: runtime.NumCPU()}).Env(env)
	cpu := runtime.NumCPU()
	if n := workerLimit(value(env, "GOMAXPROCS")); n > 0 {
		cpu = min(cpu, n)
	}
	bounded = set(bounded, "GOMAXPROCS", strconv.Itoa(cpu))
	return set(set(bounded, ownerEnv, ""), scopeEnv, "")
}

// Overrides returns only budget-owned variables for host terminal adapters.
func (l *Lease) Overrides(env []string) map[string]string {
	if l == nil || l.cores == 0 {
		return nil
	}
	bounded := l.Env(env)
	result := make(map[string]string)
	for _, name := range append([]string{ownerEnv, scopeEnv, "GOFLAGS", "VITEST_MIN_THREADS", "VITEST_MIN_FORKS"}, workerVars...) {
		result[name] = value(bounded, name)
	}
	return result
}

func workerLimit(raw string) int {
	if percentText, ok := strings.CutSuffix(raw, "%"); ok {
		percent, err := strconv.ParseFloat(percentText, 64)
		if err != nil || math.IsNaN(percent) || math.IsInf(percent, 0) || percent <= 0 || percent > 100 {
			return 0
		}
		return max(1, int(float64(runtime.NumCPU())*percent/100))
	}
	n, _ := strconv.Atoi(raw)
	return n
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func value(env []string, name string) string {
	for _, item := range slices.Backward(env) {
		if k, v, ok := strings.Cut(item, "="); ok && strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func set(env []string, name, val string) []string {
	result := env[:0]
	for _, entry := range env {
		k, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(k, name) {
			result = append(result, entry)
		}
	}
	return append(result, name+"="+val)
}
