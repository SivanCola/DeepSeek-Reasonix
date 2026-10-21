//go:build linux

package checkenv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"reasonix/internal/base/proc"
)

type namespaceScope struct {
	mu        sync.Mutex
	info      *os.File
	infoWrite *os.File
	block     *os.File
	unblock   *os.File
	pidfd     int
	cancelled bool
	started   bool
	confirmed bool
	done      chan struct{}
	monitor   *os.Process
	err       error
	bind      func(int, uint64, *os.Process) (int, error)
	ctx       context.Context
	signal    func(int) error
}

func newNamespaceScope() *namespaceScope {
	return &namespaceScope{pidfd: -1, done: make(chan struct{}), bind: bindNamespacePID,
		signal: func(fd int) error { return unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0) }}
}

func (s *namespaceScope) prepare() error {
	probe, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return errors.Join(ErrUnsupported, err)
	}
	unix.Close(probe)
	s.info, s.infoWrite, err = os.Pipe()
	if err == nil {
		s.block, s.unblock, err = os.Pipe()
	}
	if err != nil {
		s.closeFiles()
	}
	return err
}

func (i *Invocation) Started(cmd *exec.Cmd) {
	s := i.scope
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.monitor = cmd.Process
	s.infoWrite.Close()
	s.block.Close()
	s.mu.Unlock()
	go s.observe()
}

func (i *Invocation) Cancel() error {
	s := i.scope
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = true
	if s.pidfd >= 0 {
		return s.cancelLocked()
	}
	return nil
}

func (i *Invocation) WaitScope() error { return i.scope.wait() }

func (s *namespaceScope) wait() error {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if started {
		<-s.done
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if started && !s.confirmed {
		return errors.Join(s.err, proc.ErrScopeCompletionUnknown)
	}
	return s.err
}

func (s *namespaceScope) completed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.confirmed
}

// PID1 cannot execute its payload until its pidfd and namespace identity agree
// with the monitor's setup report. Cancellation preserves the waitpid owner.
func (s *namespaceScope) observe() {
	defer close(s.done)
	var info struct {
		PID       int    `json:"child-pid"`
		Namespace uint64 `json:"pid-namespace"`
	}
	err := json.NewDecoder(io.LimitReader(s.info, 1<<20)).Decode(&info)
	s.mu.Lock()
	if err == nil && info.PID > 0 {
		s.pidfd, err = s.bind(info.PID, info.Namespace, s.monitor)
	}
	fd := s.pidfd
	s.cancelled = s.cancelled || s.ctx.Err() != nil
	if err != nil || fd < 0 {
		s.err = errors.Join(ErrUnsupported, err)
		if fd < 0 {
			_ = s.monitor.Kill()
			s.mu.Unlock()
			return
		}
		if err := s.cancelLocked(); err != nil {
			s.mu.Unlock()
			return
		}
	} else if !s.cancelled || s.cancelLocked() == nil {
		s.unblock.Close()
	}
	s.mu.Unlock()
	for {
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		_, err := unix.Poll(poll, -1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		s.mu.Lock()
		s.confirmed = err == nil && poll[0].Revents&unix.POLLIN != 0
		if !s.confirmed {
			s.err = errors.Join(s.err, ErrUnsupported, err)
		}
		unix.Close(fd)
		s.pidfd = -1
		s.mu.Unlock()
		return
	}
}

func (s *namespaceScope) cancelLocked() error {
	err := s.signal(s.pidfd)
	if err != nil && !errors.Is(err, unix.ESRCH) {
		s.err = errors.Join(s.err, err)
		_ = s.monitor.Kill()
		return err
	}
	return nil
}

func bindNamespacePID(pid int, namespace uint64, monitor *os.Process) (int, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return -1, err
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	separator := strings.LastIndex(string(stat), ") ")
	var fields []string
	if separator >= 0 {
		fields = strings.Fields(string(stat[separator+2:]))
	}
	if err != nil || monitor == nil || len(fields) < 2 || fields[1] != strconv.Itoa(monitor.Pid) {
		unix.Close(fd)
		return -1, errors.Join(ErrUnsupported, err)
	}
	if err := monitor.Signal(unix.Signal(0)); err != nil {
		unix.Close(fd)
		return -1, errors.Join(ErrUnsupported, err)
	}
	link, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/ns/pid")
	if err != nil || namespace == 0 || link != "pid:["+strconv.FormatUint(namespace, 10)+"]" {
		return fd, errors.Join(ErrUnsupported, err)
	}
	return fd, nil
}

func (s *namespaceScope) closeFiles() {
	for _, f := range []*os.File{s.info, s.infoWrite, s.block, s.unblock} {
		if f != nil {
			_ = f.Close()
		}
	}
}
