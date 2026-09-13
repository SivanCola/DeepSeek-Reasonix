package sessionv3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PersistenceBinding delivers accepted commits to one physical SessionHandle.
//
// It holds only the write-behind prefix of the same event sequence that Session
// owns; it is not a second source of business state. Delivery into the queue is
// a pure memory operation, so a slow disk can never extend the session commit
// lock or block cancellation.
type PersistenceBinding struct {
	handle SessionHandle
	dir    string

	// metadataSource rebuilds the list projection for the catalog cache once
	// the accepted prefix is fully durable. Session supplies it so the binding
	// never holds a projection of its own.
	metadataSource func(durable uint64) (catalogMetadata, bool)

	mu         sync.Mutex
	queue      []Commit
	durable    uint64
	timer      timerHandle
	draining   bool
	autoPaused bool
	accepting  bool
	closed     bool
	writeErr   error
	uncertain  *uncertainWrite

	drainMu   sync.Mutex
	closeOnce sync.Once
	closeErr  error

	afterFunc func(time.Duration, func()) timerHandle
	writeFn   func(context.Context, io.Writer, []byte) error
	syncFn    func(*os.File) error
}

func newPersistenceBinding(handle SessionHandle, dir string, durable uint64, opts OpenOptions) *PersistenceBinding {
	after := opts.AfterFunc
	if after == nil {
		after = func(delay time.Duration, fire func()) timerHandle { return time.AfterFunc(delay, fire) }
	}
	writeFn := opts.Write
	if writeFn == nil {
		writeFn = writeAllContext
	}
	syncFn := opts.Sync
	if syncFn == nil {
		syncFn = func(file *os.File) error { return file.Sync() }
	}
	return &PersistenceBinding{
		handle: handle, dir: dir, durable: durable, accepting: true,
		afterFunc: after, writeFn: writeFn, syncFn: syncFn,
	}
}

// accept atomically adds an immutable commit to the write-behind queue and
// publishes it to Session through accepted. It performs no file I/O and calls
// accepted exactly once while the queue is locked. Keeping these two memory
// mutations in one boundary prevents a closed binding from rejecting a commit
// after Session has already exposed it, and prevents Flush from persisting a
// commit before Session exposes it.
func (b *PersistenceBinding) accept(commit Commit, accepted func()) error {
	if b == nil {
		return osClosedError()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.accepting || b.closed {
		return osClosedError()
	}
	b.queue = append(b.queue, cloneCommit(commit))
	accepted()
	if !b.autoPaused && !b.draining && b.timer == nil {
		b.scheduleDrainLocked()
	}
	return nil
}

func (b *PersistenceBinding) stopAccepting() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.accepting = false
	b.mu.Unlock()
}

func (b *PersistenceBinding) scheduleDrainLocked() {
	b.timer = b.afterFunc(LiveBatchDelay, func() { _ = b.drain(context.Background(), false) })
}

// progress reports the durable watermark and whether the accepted prefix is
// fully persisted.
func (b *PersistenceBinding) progress() (uint64, PersistenceStatus, string) {
	if b == nil {
		return 0, PersistenceReady, ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	status := PersistenceReady
	if b.writeErr != nil {
		status = PersistenceFailed
		if errors.Is(b.writeErr, ErrPersistenceUncertain) {
			status = PersistenceUncertain
		}
	} else if len(b.queue) > 0 || b.draining {
		status = PersistencePending
	}
	return b.durable, status, errorString(b.writeErr)
}

// Flush drains the queue and reports the durable sequence. Cancelling one
// caller's wait never cancels the shared physical write.
func (b *PersistenceBinding) Flush(ctx context.Context) (DurableReceipt, error) {
	if b == nil {
		return DurableReceipt{}, fmt.Errorf("sessionv3: nil persistence binding")
	}
	if err := ctx.Err(); err != nil {
		return DurableReceipt{}, err
	}
	// Once an append starts, one caller cannot cancel the shared physical write.
	// drainMu merges callers onto the ordered write chain while each caller can
	// still stop waiting through its own context.
	done := make(chan error, 1)
	go func() { done <- b.drain(context.Background(), true) }()
	select {
	case err := <-done:
		return DurableReceipt{DurableSequence: b.durableSequence()}, err
	case <-ctx.Done():
		return DurableReceipt{DurableSequence: b.durableSequence()}, ctx.Err()
	}
}

func (b *PersistenceBinding) durableSequence() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.durable
}

func (b *PersistenceBinding) drain(ctx context.Context, explicit bool) error {
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	for {
		b.mu.Lock()
		if b.closed || b.handle == nil {
			b.mu.Unlock()
			return os.ErrClosed
		}
		if b.timer != nil {
			b.timer.Stop()
			b.timer = nil
		}
		if len(b.queue) == 0 {
			b.draining = false
			b.mu.Unlock()
			b.refreshCatalogMetadata()
			return nil
		}
		uncertain := cloneUncertainWrite(b.uncertain)
		if uncertain != nil && !explicit {
			err := b.writeErr
			b.draining = false
			b.mu.Unlock()
			return err
		}
		b.draining = true
		pending := cloneCommits(b.queue)
		handle := b.handle
		b.mu.Unlock()

		alreadyPersisted := false
		var err error
		if uncertain != nil {
			alreadyPersisted, err = b.reconcileUncertain(ctx, handle, *uncertain)
		}
		if err == nil && alreadyPersisted {
			confirmed := uncertain.commitCount
			if confirmed <= 0 || confirmed > len(pending) {
				err = fmt.Errorf("%w: uncertain batch commit count %d exceeds pending prefix %d", ErrDamagedStore, confirmed, len(pending))
			} else {
				b.mu.Lock()
				if len(b.queue) < confirmed || !sameCommitPrefix(b.queue, pending[:confirmed]) {
					err = fmt.Errorf("%w: uncertain batch no longer matches pending prefix", ErrDamagedStore)
					b.writeErr = err
					b.autoPaused = true
					b.draining = false
					b.mu.Unlock()
					return err
				}
				b.queue = b.queue[confirmed:]
				b.durable = pending[confirmed-1].LastSequence()
				b.writeErr = nil
				b.uncertain = nil
				b.autoPaused = false
				if len(b.queue) == 0 {
					b.draining = false
					b.mu.Unlock()
					b.refreshCatalogMetadata()
					return nil
				}
				b.mu.Unlock()
				continue
			}
		}
		if err == nil && !alreadyPersisted {
			err = b.persist(ctx, handle, pending)
		}
		b.mu.Lock()
		if err != nil {
			b.draining = false
			b.writeErr = err
			var uncertainErr *uncertainAppendError
			if errors.As(err, &uncertainErr) {
				copy := uncertainErr.write
				copy.data = append([]byte(nil), copy.data...)
				b.uncertain = &copy
			}
			b.autoPaused = true
			b.mu.Unlock()
			return err
		}
		if len(b.queue) < len(pending) || !sameCommitPrefix(b.queue, pending) {
			b.draining = false
			b.writeErr = fmt.Errorf("%w: pending batch order changed", ErrDamagedStore)
			b.autoPaused = true
			err := b.writeErr
			b.mu.Unlock()
			return err
		}
		b.queue = b.queue[len(pending):]
		b.durable = pending[len(pending)-1].LastSequence()
		b.writeErr = nil
		b.uncertain = nil
		b.autoPaused = false
		if len(b.queue) == 0 {
			b.draining = false
			b.mu.Unlock()
			b.refreshCatalogMetadata()
			return nil
		}
		// Match DSH's drain chain: once a batch starts writing, events accepted
		// during that write are drained immediately in the next physical batch.
		// The fixed 200ms window applies only to the first pending batch.
		b.mu.Unlock()
	}
}

func (b *PersistenceBinding) refreshCatalogMetadata() {
	if b == nil || b.metadataSource == nil {
		return
	}
	b.mu.Lock()
	if b.closed || len(b.queue) != 0 {
		b.mu.Unlock()
		return
	}
	durable := b.durable
	b.mu.Unlock()
	metadata, ok := b.metadataSource(durable)
	if !ok {
		return
	}
	_ = writeCatalogMetadataForSession(filepath.Join(filepath.Dir(b.dir), ".query-cache", filepath.Base(b.dir)), b.dir, metadata)
}

func (b *PersistenceBinding) persist(ctx context.Context, handle SessionHandle, commits []Commit) error {
	return handle.Append(ctx, commits)
}

// reconcileUncertain proves whether the prior append completed before retrying.
// The writer lease and drainMu make this a single-owner repair operation. A
// partial tail is preserved byte-for-byte before truncation; a mismatching or
// unexpectedly extended tail remains recovery-required rather than guessed.
func (b *PersistenceBinding) reconcileUncertain(ctx context.Context, handle SessionHandle, uncertain uncertainWrite) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	physical, ok := handle.(*Store)
	if !ok || physical == nil {
		return false, fmt.Errorf("%w: handle cannot reconcile an uncertain append", ErrPersistenceUncertain)
	}
	file, err := physical.writableFile()
	if err != nil {
		return false, err
	}
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() < uncertain.start {
		return false, fmt.Errorf("%w: log shrank below uncertain offset %d", ErrPersistenceUncertain, uncertain.start)
	}
	tailLen := info.Size() - uncertain.start
	if tailLen == 0 {
		return false, nil
	}
	tail := make([]byte, tailLen)
	if _, err := file.ReadAt(tail, uncertain.start); err != nil {
		return false, fmt.Errorf("%w: inspect uncertain tail: %w", ErrPersistenceUncertain, err)
	}
	if int64(len(uncertain.data)) == tailLen && bytes.Equal(tail, uncertain.data) {
		if err := b.syncFn(file); err != nil {
			return false, &uncertainAppendError{cause: fmt.Errorf("fsync verified append: %w", err), write: uncertain}
		}
		// The uncertain fsync did not advance the physical index. Rebuild its
		// durable cursor before validating a queued successor, or the successor
		// would appear to start after a gap.
		if err := physical.rebuildWriterIndex(file); err != nil {
			return false, fmt.Errorf("%w: rebuild index after verified append: %w", ErrPersistenceUncertain, err)
		}
		return true, nil
	}
	if tailLen < int64(len(uncertain.data)) && bytes.Equal(tail, uncertain.data[:len(tail)]) {
		backup := filepath.Join(b.dir, fmt.Sprintf("events.uncertain-%d.tail", time.Now().UTC().UnixNano()))
		if err := os.WriteFile(backup, tail, 0o600); err != nil {
			return false, fmt.Errorf("%w: preserve partial tail: %w", ErrPersistenceUncertain, err)
		}
		if err := file.Truncate(uncertain.start); err != nil {
			return false, fmt.Errorf("%w: truncate preserved partial tail: %w", ErrPersistenceUncertain, err)
		}
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return false, fmt.Errorf("%w: seek repaired log: %w", ErrPersistenceUncertain, err)
		}
		if err := b.syncFn(file); err != nil {
			return false, fmt.Errorf("%w: sync repaired log: %w", ErrPersistenceUncertain, err)
		}
		return false, nil
	}
	return false, fmt.Errorf("%w: on-disk tail does not match batch at offset %d", ErrPersistenceUncertain, uncertain.start)
}

// freezePhysical runs fn while the physical write chain is idle. Export uses it
// so the copied bytes cannot advance between the manifest and the log.
func (b *PersistenceBinding) freezePhysical(fn func() error) error {
	if b == nil || fn == nil {
		return nil
	}
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	return fn()
}

// Close drains the queue and closes the physical handle. It is deliberately
// uncancellable and idempotent: every caller observes the same result.
func (b *PersistenceBinding) Close(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.accepting = false
		b.mu.Unlock()
		_, flushErr := b.Flush(context.Background())
		b.drainMu.Lock()
		defer b.drainMu.Unlock()
		b.mu.Lock()
		if b.timer != nil {
			b.timer.Stop()
			b.timer = nil
		}
		handle := b.handle
		b.handle = nil
		b.closed = true
		b.mu.Unlock()
		var closeErr error
		if handle != nil {
			closeErr = handle.Close(context.Background())
		}
		b.closeErr = errors.Join(flushErr, closeErr)
	})
	return b.closeErr
}

// encodeCommitLines serializes a physical batch in commit order.
func encodeCommitLines(commits []Commit) ([]byte, []int, error) {
	var data bytes.Buffer
	lengths := make([]int, 0, len(commits))
	for _, commit := range commits {
		line, err := json.Marshal(commit)
		if err != nil {
			return nil, nil, err
		}
		data.Write(line)
		data.WriteByte('\n')
		lengths = append(lengths, len(line)+1)
	}
	return data.Bytes(), lengths, nil
}

func cloneUncertainWrite(in *uncertainWrite) *uncertainWrite {
	if in == nil {
		return nil
	}
	out := *in
	out.data = append([]byte(nil), in.data...)
	return &out
}
