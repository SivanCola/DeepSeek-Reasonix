package memory

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/base/filelock"
	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/config"
)

// TestMain isolates the user state the default lock root resolves under, so no
// test creates or touches a lock in the real one.
func TestMain(m *testing.M) {
	cleanup, err := testenv.IsolateUserState()
	if err != nil {
		panic(err)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// testLockRoot is the isolated lock root every mutation lock file lands in.
func testLockRoot(t *testing.T) string {
	t.Helper()
	root := config.MemoryLockDir()
	if root == "" {
		t.Fatal("isolated user state has no lock root")
	}
	return root
}

func withMemoryLockWait(t *testing.T, wait time.Duration) {
	t.Helper()
	previous := memoryLockWait
	memoryLockWait = wait
	t.Cleanup(func() { memoryLockWait = previous })
}

const (
	lockChildEnv     = "REASONIX_MEMORY_LOCK_CHILD"
	lockChildPathEnv = "REASONIX_MEMORY_LOCK_CHILD_PATH"
)

// TestMemoryLockChildProcess is the holder in TestMemoryLockIsCrossProcess. It
// is a real second process: its file lock is not this process's local registry.
func TestMemoryLockChildProcess(t *testing.T) {
	if os.Getenv(lockChildEnv) != "1" {
		return
	}
	release, err := filelock.Acquire(context.Background(), os.Getenv(lockChildPathEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, "lock child: acquire:", err)
		os.Exit(2)
	}
	defer release()
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// startLockChild runs this test binary as the holder of lockPath and waits for
// it to report the lock. The returned stop releases it by ending the process.
func startLockChild(t *testing.T, lockPath string) (stop func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMemoryLockChildProcess$")
	cmd.Env = append(os.Environ(), lockChildEnv+"=1", lockChildPathEnv+"="+lockPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lock child: %v", err)
	}
	var once sync.Once
	wait := func() {
		once.Do(func() {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	}
	t.Cleanup(wait)
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		wait()
		t.Fatalf("lock child did not report readiness: %v", err)
	}
	if strings.TrimSpace(line) != "locked" {
		wait()
		t.Fatalf("lock child reported %q, want locked", strings.TrimSpace(line))
	}
	return wait
}

// TestMemoryLockIsCrossProcess holds the store's lock from a real child process
// and requires a second Store value over the same directory to fail with the
// typed, retryable error inside the bounded wait. Two Store values in one
// process would only prove the local registry, so the holder is a separate OS
// process.
func TestMemoryLockIsCrossProcess(t *testing.T) {
	root := testLockRoot(t)
	withMemoryLockWait(t, 150*time.Millisecond)
	dir := testenv.TempDir(t)
	holder := Store{Dir: dir}
	saved, err := holder.SaveWithOptions(Memory{Name: "alpha", Body: "one"}, SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	key := canonicalLockDir(dir)
	stop := startLockChild(t, memoryLockPath(root, key))

	start := time.Now()
	_, err = (Store{Dir: dir}).SaveWithOptions(Memory{ID: saved.Memory.ID, Name: "alpha", Body: "two"}, SaveOptions{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("the second store saved while a child process held the mutation lock")
	}
	if !errors.Is(err, ErrStoreLocked) {
		t.Fatalf("save error = %v, want ErrStoreLocked", err)
	}
	var locked *StoreLockedError
	if !errors.As(err, &locked) {
		t.Fatalf("save error = %v, want a *StoreLockedError", err)
	}
	if locked.Dir != key {
		t.Errorf("locked directory = %q, want %q", locked.Dir, key)
	}
	if elapsed > time.Second {
		t.Errorf("save waited %s for a %s deadline", elapsed, memoryLockWait)
	}

	stop()
	if _, err := (Store{Dir: dir}).SaveWithOptions(Memory{ID: saved.Memory.ID, Name: "alpha", Body: "two"}, SaveOptions{}); err != nil {
		t.Fatalf("save after the child released the lock: %v", err)
	}
	active, ok, err := holder.Read("alpha")
	if err != nil || !ok || active.Body != "two" {
		t.Fatalf("read after release: ok=%v body=%q err=%v", ok, active.Body, err)
	}
}

// TestMutationLockOrderIsDeterministic covers the two-directory store: a store
// whose Dir and GlobalDir are swapped with another store's must request the same
// lock order, or the two could each hold one and wait for the other. The
// in-process mutex serialises both stores here, so the equal sorted key
// sequences are the guarantee and the concurrent saves are the control.
func TestMutationLockOrderIsDeterministic(t *testing.T) {
	withMemoryLockWait(t, 500*time.Millisecond)
	parent := testenv.TempDir(t)
	x := filepath.Join(parent, "x")
	y := filepath.Join(parent, "y")
	a := Store{Dir: x, GlobalDir: y}
	b := Store{Dir: y, GlobalDir: x}

	keysA, keysB := a.mutationLockKeys(), b.mutationLockKeys()
	if len(keysA) != 2 {
		t.Fatalf("store locks %v, want both of its directories", keysA)
	}
	if !slices.IsSorted(keysA) {
		t.Fatalf("store locks %v are not sorted", keysA)
	}
	if !slices.Equal(keysA, keysB) {
		t.Fatalf("stores with swapped Dir and GlobalDir request different lock orders: %v vs %v", keysA, keysB)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, s := range []Store{a, b} {
		wg.Go(func() {
			name := "fact-in-" + filepath.Base(s.Dir)
			for i := range 5 {
				if _, err := s.SaveWithOptions(Memory{Name: name, Scope: FactScopeProject, Body: fmt.Sprintf("v%d", i)}, SaveOptions{}); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("a store with swapped directories failed to save: %v", err)
	}
}

// TestSingleProcessMutationsStillWork is the control: save, rename, archive and
// restore behave as before and the store's lock file is named by its directory.
func TestSingleProcessMutationsStillWork(t *testing.T) {
	root := testLockRoot(t)
	dir := testenv.TempDir(t)
	s := Store{Dir: dir}
	saved, err := s.SaveWithOptions(Memory{Name: "alpha", Body: "one"}, SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(memoryLockPath(root, canonicalLockDir(dir))); err != nil {
		t.Fatalf("the store's lock file is missing: %v", err)
	}
	if _, err := s.SaveWithOptions(Memory{ID: saved.Memory.ID, Name: "beta", Body: "two"}, SaveOptions{}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	active, ok, err := s.Read("beta")
	if err != nil || !ok || active.Body != "two" {
		t.Fatalf("read after rename: ok=%v body=%q err=%v", ok, active.Body, err)
	}
	archivePath, err := s.Archive("beta")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive path %s: %v", archivePath, err)
	}
	restored, err := s.RestoreArchived(archivePath)
	if err != nil {
		t.Fatalf("restore archived: %v", err)
	}
	if restored.Memory.Body != "two" {
		t.Fatalf("restored body = %q, want %q", restored.Memory.Body, "two")
	}

	for i := range 20 {
		if _, err := s.SaveWithOptions(Memory{ID: restored.Memory.ID, Name: "beta", Body: fmt.Sprintf("v%d", i+3)}, SaveOptions{}); err != nil {
			t.Fatalf("plain save %d: %v", i, err)
		}
	}
}
