package hook

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	fileencoding "reasonix/internal/fileutil/encoding"
	"reasonix/internal/sandbox"
)

var windowsHookBash struct {
	sync.Once
	path string
	err  error
}

// windowsPOSIXShellInvocation preserves explicit `sh -c` / `bash -c` hook
// contracts on Windows. Git for Windows normally ships a real Bash outside the
// cmd.exe PATH, so reuse the same hardened discovery path as the shell tool
// instead of asking cmd.exe to find an executable it cannot see.
func windowsPOSIXShellInvocation(command string) (string, []string, bool, error) {
	return windowsPOSIXShellInvocationWith(command, cachedWindowsHookBash)
}

func windowsPOSIXShellArgvInvocation(command string, args []string) (string, []string, bool, error) {
	return windowsPOSIXShellArgvInvocationWith(command, args, cachedWindowsHookBash)
}

func windowsPOSIXShellArgvInvocationWith(command string, args []string, resolve func() (string, error)) (string, []string, bool, error) {
	if !isPOSIXShellWord(command) || !hasCommandStringFlag(args) {
		return "", nil, false, nil
	}
	path, err := resolve()
	if err != nil {
		return "", nil, true, err
	}
	return path, append([]string(nil), args...), true, nil
}

func windowsPOSIXShellInvocationWith(command string, resolve func() (string, error)) (string, []string, bool, error) {
	fields, _, _, ok := parseSimpleHookCommandFields(command)
	if !ok || len(fields) < 3 || !isPOSIXShellWord(fields[0]) || !hasCommandStringFlag(fields[1:]) {
		return "", nil, false, nil
	}
	path, err := resolve()
	if err != nil {
		return "", nil, true, err
	}
	return path, append([]string(nil), fields[1:]...), true, nil
}

func isPOSIXShellWord(word string) bool {
	if i := strings.LastIndexAny(word, `/\`); i >= 0 {
		word = word[i+1:]
	}
	word = strings.ToLower(strings.TrimSpace(word))
	return word == "sh" || word == "sh.exe" || word == "bash" || word == "bash.exe"
}

func hasCommandStringFlag(args []string) bool {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
			return false
		}
		if strings.Contains(strings.TrimLeft(arg, "-"), "c") {
			return true
		}
	}
	return false
}

func cachedWindowsHookBash() (string, error) {
	windowsHookBash.Do(func() {
		shell := sandbox.ResolveShell("bash", "", nil)
		if shell.Kind != sandbox.ShellBash {
			windowsHookBash.err = missingWindowsHookBashError()
			return
		}
		path := strings.TrimSpace(shell.Path)
		if path == "" {
			windowsHookBash.err = missingWindowsHookBashError()
			return
		}
		if resolved, err := exec.LookPath(path); err == nil {
			windowsHookBash.path = resolved
			return
		}
		if filepath.IsAbs(path) {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				windowsHookBash.path = path
				return
			}
		}
		windowsHookBash.err = missingWindowsHookBashError()
	})
	return windowsHookBash.path, windowsHookBash.err
}

func missingWindowsHookBashError() error {
	return errors.New("hook requires a POSIX shell on Windows, but no usable Git Bash was found; install Git for Windows or replace the POSIX shell hook with a native portable command")
}

// decodeHookOutput keeps UTF-8-native runtimes such as Node byte-for-byte,
// while recovering legacy Windows cmd.exe output (notably CP936/GB18030) before
// it reaches the desktop renderer. Hook stdout/stderr are text contracts, so a
// final valid-UTF-8 guard is safer than surfacing raw invalid bytes.
func decodeHookOutput(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	decoded := raw
	if !utf8.Valid(raw) {
		decoded = fileencoding.DecodeToUTF8(raw)
	}
	return strings.TrimSpace(strings.ToValidUTF8(string(decoded), "\uFFFD"))
}
