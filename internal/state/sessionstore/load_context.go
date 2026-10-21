package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reasonix/internal/contract/provider"
	"sync"
	"time"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (l sessionReplayLimits) err() error {
	if l.ctx == nil {
		return nil
	}
	return l.ctx.Err()
}

func (l sessionReplayLimits) reader(r io.Reader) io.Reader {
	if l.ctx == nil {
		return r
	}
	return contextReader{ctx: l.ctx, reader: r}
}

// LoadSessionContext preserves normal resume semantics while allowing a projection to yield.
func LoadSessionContext(ctx context.Context, path string) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := PrepareSessionPath(path); err != nil {
		return nil, err
	}
	v, _ := sessionSaveLocks.LoadOrStore(canonicalSessionSavePath(path), &sync.Mutex{})
	mu := v.(*sync.Mutex)
	for !mu.TryLock() {
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	defer mu.Unlock()
	return loadSessionUnlockedContext(ctx, path)
}

func loadSessionMessagesFromJSONL(path string) ([]provider.Message, error) {
	return loadSessionMessagesFromJSONLWithLimits(path, defaultSessionReplayLimits)
}

func loadSessionMessagesFromJSONLWithLimits(path string, limits sessionReplayLimits) ([]provider.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var msgs []provider.Message
	dec := json.NewDecoder(limits.reader(f))
	for {
		if err := limits.err(); err != nil {
			return nil, err
		}
		var m provider.Message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}
