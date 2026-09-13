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
	"time"
)

func (s *Store) scheduleDrainLocked() {
	s.timer = s.afterFunc(LiveBatchDelay, func() { _ = s.drain(context.Background(), false) })
}

func (s *Store) Flush(ctx context.Context) (DurableReceipt, error) {
	if s == nil {
		return DurableReceipt{}, fmt.Errorf("sessionv3: nil store")
	}
	if err := ctx.Err(); err != nil {
		return DurableReceipt{}, err
	}
	// Once a physical append has begun, one caller abandoning its wait cannot
	// safely cancel that write or make another waiter guess whether bytes reached
	// disk. drainMu merges concurrent callers onto the same ordered write chain;
	// each caller may still stop waiting through its own context.
	done := make(chan error, 1)
	go func() { done <- s.drain(context.Background(), true) }()
	select {
	case err := <-done:
		s.mu.Lock()
		receipt := DurableReceipt{DurableSequence: s.durable}
		s.mu.Unlock()
		return receipt, err
	case <-ctx.Done():
		s.mu.Lock()
		receipt := DurableReceipt{DurableSequence: s.durable}
		s.mu.Unlock()
		return receipt, ctx.Err()
	}
}

func (s *Store) drain(ctx context.Context, explicit bool) error {
	s.drainMu.Lock()
	defer s.drainMu.Unlock()
	for {
		s.mu.Lock()
		if s.closed || s.file == nil {
			s.mu.Unlock()
			return os.ErrClosed
		}
		if s.timer != nil {
			s.timer.Stop()
			s.timer = nil
		}
		if len(s.pending) == 0 {
			s.draining = false
			s.mu.Unlock()
			return nil
		}
		uncertain := cloneUncertainWrite(s.uncertain)
		if uncertain != nil && !explicit {
			err := s.writeErr
			s.draining = false
			s.mu.Unlock()
			return err
		}
		s.draining = true
		pending := cloneCommits(s.pending)
		file := s.file
		s.mu.Unlock()

		alreadyPersisted := false
		var err error
		if uncertain != nil {
			alreadyPersisted, err = s.reconcileUncertain(ctx, file, *uncertain)
		}
		if err == nil && alreadyPersisted {
			err = s.rebuildWriterIndex(file)
		}
		if err == nil && alreadyPersisted {
			confirmed := uncertain.commitCount
			if confirmed <= 0 || confirmed > len(pending) {
				err = fmt.Errorf("%w: uncertain batch commit count %d exceeds pending prefix %d", ErrDamagedStore, confirmed, len(pending))
			} else {
				s.mu.Lock()
				if len(s.pending) < confirmed || !sameCommitPrefix(s.pending, pending[:confirmed]) {
					err = fmt.Errorf("%w: uncertain batch no longer matches pending prefix", ErrDamagedStore)
					s.writeErr = err
					s.autoPaused = true
					s.draining = false
					s.mu.Unlock()
					return err
				}
				s.pending = s.pending[confirmed:]
				s.durable = pending[confirmed-1].LastSequence()
				s.writeErr = nil
				s.uncertain = nil
				s.autoPaused = false
				if len(s.pending) == 0 {
					s.draining = false
					s.mu.Unlock()
					return nil
				}
				s.mu.Unlock()
				continue
			}
		}
		if err == nil && !alreadyPersisted {
			err = s.persist(ctx, file, pending)
		}
		s.mu.Lock()
		if err != nil {
			s.draining = false
			s.writeErr = err
			var uncertainErr *uncertainAppendError
			if errors.As(err, &uncertainErr) {
				copy := uncertainErr.write
				copy.data = append([]byte(nil), copy.data...)
				s.uncertain = &copy
			}
			s.autoPaused = true
			s.mu.Unlock()
			return err
		}
		if len(s.pending) < len(pending) || !sameCommitPrefix(s.pending, pending) {
			s.draining = false
			s.writeErr = fmt.Errorf("%w: pending batch order changed", ErrDamagedStore)
			s.autoPaused = true
			err := s.writeErr
			s.mu.Unlock()
			return err
		}
		s.pending = s.pending[len(pending):]
		s.durable = pending[len(pending)-1].LastSequence()
		s.writeErr = nil
		s.uncertain = nil
		s.autoPaused = false
		if len(s.pending) == 0 {
			s.draining = false
			s.mu.Unlock()
			return nil
		}
		// Match DSH's drain chain: once a batch starts writing, events accepted
		// during that write are drained immediately in the next physical batch.
		// The fixed 200ms window applies only to the first pending batch.
		s.mu.Unlock()
	}
}

func (s *Store) persist(ctx context.Context, file *os.File, commits []Commit) error {
	written, lengths, err := encodeCommitLines(commits)
	if err != nil {
		return err
	}
	start, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if err := s.writeFn(ctx, file, written); err != nil {
		end, statErr := file.Seek(0, io.SeekEnd)
		if statErr == nil && end == start {
			return err
		}
		if statErr == nil && end == start+int64(len(written)) {
			if syncErr := s.syncFn(file); syncErr == nil {
				s.recordPersistedIndex(file, start, commits, lengths)
				return nil
			}
		}
		return &uncertainAppendError{cause: err, write: uncertainWrite{start: start, data: append([]byte(nil), written...), commitCount: len(commits)}}
	}
	if err := s.syncFn(file); err != nil {
		return &uncertainAppendError{cause: fmt.Errorf("fsync: %w", err), write: uncertainWrite{start: start, data: append([]byte(nil), written...), commitCount: len(commits)}}
	}
	s.recordPersistedIndex(file, start, commits, lengths)
	return nil
}

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

// reconcileUncertain proves whether the prior append completed before retrying.
// The writer lease and drainMu make this a single-owner repair operation. A
// partial tail is preserved byte-for-byte before truncation; a mismatching or
// unexpectedly extended tail remains recovery-required rather than guessed.
func (s *Store) reconcileUncertain(ctx context.Context, file *os.File, uncertain uncertainWrite) (bool, error) {
	if err := ctx.Err(); err != nil {
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
		if err := s.syncFn(file); err != nil {
			return false, &uncertainAppendError{cause: fmt.Errorf("fsync verified append: %w", err), write: uncertain}
		}
		return true, nil
	}
	if tailLen < int64(len(uncertain.data)) && bytes.Equal(tail, uncertain.data[:len(tail)]) {
		backup := filepath.Join(s.dir, fmt.Sprintf("events.uncertain-%d.tail", time.Now().UTC().UnixNano()))
		if err := os.WriteFile(backup, tail, 0o600); err != nil {
			return false, fmt.Errorf("%w: preserve partial tail: %w", ErrPersistenceUncertain, err)
		}
		if err := file.Truncate(uncertain.start); err != nil {
			return false, fmt.Errorf("%w: truncate preserved partial tail: %w", ErrPersistenceUncertain, err)
		}
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return false, fmt.Errorf("%w: seek repaired log: %w", ErrPersistenceUncertain, err)
		}
		if err := s.syncFn(file); err != nil {
			return false, fmt.Errorf("%w: sync repaired log: %w", ErrPersistenceUncertain, err)
		}
		return false, nil
	}
	return false, fmt.Errorf("%w: on-disk tail does not match batch at offset %d", ErrPersistenceUncertain, uncertain.start)
}

func cloneUncertainWrite(in *uncertainWrite) *uncertainWrite {
	if in == nil {
		return nil
	}
	out := *in
	out.data = append([]byte(nil), in.data...)
	return &out
}
