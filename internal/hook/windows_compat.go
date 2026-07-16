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
	if !isBarePOSIXShellWord(command) || !hasCommandStringFlag(args) {
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
	if !ok || len(fields) < 3 || !isBarePOSIXShellWord(fields[0]) || !hasCommandStringFlag(fields[1:]) {
		return "", nil, false, nil
	}
	path, err := resolve()
	if err != nil {
		return "", nil, true, err
	}
	return path, append([]string(nil), fields[1:]...), true, nil
}

func isBarePOSIXShellWord(word string) bool {
	word = strings.TrimSpace(word)
	if strings.ContainsAny(word, `/\:`) {
		return false
	}
	word = strings.ToLower(word)
	return word == "sh" || word == "sh.exe" || word == "bash" || word == "bash.exe"
}

func hasCommandStringFlag(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-" || arg == "--" || !strings.HasPrefix(arg, "-") {
			return false
		}
		if strings.HasPrefix(arg, "--") {
			name, _, hasInlineValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
			if !hasInlineValue && bashLongOptionNeedsOperand(name) {
				if i+1 >= len(args) {
					return false
				}
				i++
			}
			continue
		}
		options := strings.TrimPrefix(arg, "-")
		for optionIndex := 0; optionIndex < len(options); optionIndex++ {
			switch options[optionIndex] {
			case 'c':
				return i+1 < len(args)
			case 'o', 'O':
				// -o/-O consume an option name. Any remaining bytes in this
				// argument are that operand, not more single-letter flags.
				if optionIndex+1 == len(options) {
					if i+1 >= len(args) {
						return false
					}
					i++
				}
				optionIndex = len(options)
			}
		}
	}
	return false
}

func bashLongOptionNeedsOperand(name string) bool {
	return name == "init-file" || name == "rcfile"
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
func decodeHookOutput(raw []byte, truncated bool) string {
	if len(raw) == 0 {
		return ""
	}
	decoded := raw
	if !utf8.Valid(raw) {
		if prefix, ok := truncatedUTF8Prefix(raw, truncated); ok {
			decoded = prefix
		} else {
			decoded = fileencoding.DecodeToUTF8(raw)
		}
	}
	return strings.TrimSpace(strings.ToValidUTF8(string(decoded), "\uFFFD"))
}

func truncatedUTF8Prefix(raw []byte, truncated bool) ([]byte, bool) {
	if !truncated {
		return nil, false
	}
	for suffixLen := 1; suffixLen < utf8.UTFMax && suffixLen <= len(raw); suffixLen++ {
		prefix := raw[:len(raw)-suffixLen]
		suffix := raw[len(raw)-suffixLen:]
		if utf8.Valid(prefix) && !utf8.FullRune(suffix) {
			return prefix, true
		}
	}
	return nil, false
}
