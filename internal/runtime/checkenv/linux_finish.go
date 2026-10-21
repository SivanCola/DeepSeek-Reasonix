//go:build linux

package checkenv

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"reasonix/internal/base/fileutil"
	"reasonix/internal/safety/evidence"
)

func (i *Invocation) finish() {
	i.scope.wait()
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state.cmd != nil && i.state.cmd.Process != nil && !i.scope.completed() {
		return
	}
	i.state.active = false
	defer func() {
		if i.state.closed {
			i.remove()
		}
	}()
	cmd, ctx := i.state.cmd, i.state.ctx
	if cmd == nil || cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		return
	}
	i.state.input.Completed = true
	if ctx.Err() != nil || cmd.Path != i.state.argv[0] || !reflect.DeepEqual(cmd.Args, i.state.argv) || cmd.Dir != "/" ||
		digest(cmd.Environ()) != i.state.input.EnvironmentHash ||
		!reflect.DeepEqual(cmd.ExtraFiles, []*os.File{i.state.status, i.scope.infoWrite, i.scope.block}) ||
		!namespaceCompleted(i.state.status, cmd.ProcessState.ExitCode()) {
		return
	}
	i.state.input.After = evidence.CaptureContentSnapshot(ctx, i.spec.SourceRoot, 50_000)
	if !i.state.input.After.Complete || i.state.input.Before.Digest != i.state.input.After.Digest {
		return
	}
	runtimeEntries, err := scanTree(ctx, i.state.runtimeRoot, treePolicy{})
	if err != nil || digest(runtimeEntries) != i.state.runtimeDigest {
		return
	}
	copied, err := scanTree(ctx, i.state.sourceRoot, treePolicy{})
	if err != nil || digest(copied) != i.state.sourceDigest {
		return
	}
	original, err := scanTree(ctx, i.spec.SourceRoot, i.state.sourcePolicy)
	if err != nil || digest(original) != i.state.originalDigest {
		return
	}
	launcher, err := fileutil.ReadContentVersion(ctx, i.state.argv[0])
	if err != nil || launcher != i.state.launcher {
		return
	}
	i.state.input.Stable, i.state.input.Gaps = true, nil
	i.recordReceipt()
}

func namespaceCompleted(status *os.File, exitCode int) bool {
	if status == nil {
		return false
	}
	if _, err := status.Seek(0, io.SeekStart); err != nil {
		return false
	}
	decoder := json.NewDecoder(io.LimitReader(status, 1<<20))
	child, finished := false, false
	for {
		var message struct {
			ChildPID *int `json:"child-pid"`
			ExitCode *int `json:"exit-code"`
		}
		if err := decoder.Decode(&message); err != nil {
			return errors.Is(err, io.EOF) && child && finished
		}
		if message.ChildPID != nil {
			child = *message.ChildPID > 0
		}
		if message.ExitCode != nil {
			finished = *message.ExitCode == exitCode
		}
	}
}

// Resolve links using the image's root, including absolute links such as
// /bin -> /usr/bin. Host path resolution must never supply the interpreter.
func virtualFile(rootPath, path string) (string, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", err
	}
	defer root.Close()
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/")
	for links := 0; links < 40; links++ {
		current, changed := ".", false
		for index, part := range parts {
			current = filepath.Join(current, part)
			if !filepath.IsLocal(current) {
				return "", ErrUnsupported
			}
			info, err := root.Lstat(current)
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, err := root.Readlink(current)
			if err != nil {
				return "", err
			}
			if filepath.IsAbs(target) {
				current = strings.TrimPrefix(filepath.Clean(target), "/")
			} else {
				current = filepath.Join(filepath.Dir(current), target)
			}
			remaining := append([]string{current}, parts[index+1:]...)
			path = filepath.Join(remaining...)
			if !filepath.IsLocal(path) {
				return "", ErrUnsupported
			}
			parts, changed = slices.Clone(strings.Split(path, "/")), true
			break
		}
		if !changed {
			return filepath.Join(rootPath, current), nil
		}
	}
	return "", errors.New("verification interpreter link cycle")
}
