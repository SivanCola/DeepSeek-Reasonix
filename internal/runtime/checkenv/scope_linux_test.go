//go:build linux

package checkenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"reasonix/internal/contract/tool"
	"reasonix/internal/tools/shellrun"
)

func prepareScopeTest(t *testing.T, command string) (*Invocation, []string) {
	t.Helper()
	spec := runtimeFixture(t)
	i := New(spec, command, "execution", "call")
	t.Cleanup(i.Close)
	argv, err := i.Prepare(t.Context(), tool.ControlledShellRequest{Command: command, Directory: spec.Directory})
	if err != nil {
		t.Fatal(err)
	}
	return i, argv
}

func TestLinuxFailedIdentityHandshakeNeverStartsPayload(t *testing.T) {
	const command = "printf PAYLOAD-RAN"
	i, argv := prepareScopeTest(t, command)
	var observed int
	i.scope.bind = func(pid int, namespace uint64, monitor *os.Process) (int, error) {
		fd, err := bindNamespacePID(pid, namespace, monitor)
		if err != nil {
			t.Error(err)
			return -1, err
		}
		observed, err = unix.PidfdOpen(pid, 0)
		if err != nil {
			t.Error(err)
		}
		return fd, errors.New("injected namespace identity mismatch")
	}
	result := shellrun.RunForeground(tool.WithControlledShell(t.Context(), i), shellrun.Request{Argv: argv, Dir: i.spec.Directory, Command: command, Track: true})
	if !errors.Is(result.Err, ErrUnsupported) || strings.Contains(result.Combined, "PAYLOAD-RAN") {
		t.Fatalf("failed setup executed payload or lost error: %+v", result)
	}
	select {
	case <-result.Tracked.WaitCompleted():
	default:
		t.Fatal("confirmed namespace failure did not release ownership")
	}
	i.Close()
	if i.state.directory != "" {
		t.Fatal("confirmed terminated environment was retained")
	}
	if observed <= 0 {
		t.Fatal("test did not capture the real namespace identity")
	}
	defer unix.Close(observed)
	poll := []unix.PollFd{{Fd: int32(observed), Events: unix.POLLIN}}
	if count, err := unix.Poll(poll, 5000); err != nil || count != 1 || poll[0].Revents&unix.POLLIN == 0 {
		t.Fatalf("failed namespace survived monitor termination: %v %v", poll, err)
	}
}

func TestLinuxRetiredMonitorIdentityCannotAuthorizeReusedParentPID(t *testing.T) {
	retired := exec.Command("/bin/true")
	if err := retired.Run(); err != nil {
		t.Fatal(err)
	}
	retired.Process.Pid = os.Getpid()
	child := exec.Command("/bin/bash", "-c", "while :; do :; done")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	fd, err := bindNamespacePID(child.Process.Pid, 0, retired.Process)
	if fd >= 0 {
		unix.Close(fd)
		t.Fatal("retired monitor's reused numeric PID authorized an unrelated child")
	}
	if !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("monitor handle was not checked: %v", err)
	}
}

func TestLinuxFailedCancellationSignalWaitsForNamespace(t *testing.T) {
	const command = "printf ready; while :; do :; done"
	i, argv := prepareScopeTest(t, command)
	i.scope.signal = func(int) error { return unix.EPERM }
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := shellrun.RunForeground(tool.WithControlledShell(ctx, i), shellrun.Request{Argv: argv, Dir: i.spec.Directory, Command: command, Track: true,
		Progress: func(chunk string) {
			if strings.Contains(chunk, "ready") {
				cancel()
			}
		}})
	if result.Err == nil || !i.scope.completed() {
		t.Fatalf("failed signal did not await namespace termination: %+v", result)
	}
	for _, input := range i.Inputs() {
		if Verify(input, "execution") {
			t.Fatal("failed cancellation minted a receipt")
		}
	}
	i.Close()
	if i.state.directory != "" {
		t.Fatal("proven terminated environment was not cleaned")
	}
}
