package sessionstore

import (
	"io"
	"os"
	"strings"
	"testing"

	"reasonix/internal/state/store"
)

func TestDAGMultiTargetRedactionLoadsEachPayloadOnce(t *testing.T) {
	var lines []string
	targets := make(map[string]any)
	parent := ""
	for i := range 256 {
		id := fmtMessageID(i)
		lines = append(lines, dagMsg(t, id, parent, "main", map[string]any{"role": "user", "content": "original"}))
		targets[id] = []any{map[string]any{"role": "user", "content": strings.Repeat("r", 1024)}}
		parent = id
	}
	lines = append(lines, dagLine(t, map[string]any{"type": "redact", "targets": targets}))
	path := writeDAGSession(t, lines...)
	info, err := os.Stat(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := replaySessionDAG(f, path, defaultSessionReplayLimits)
	if err != nil {
		t.Fatal(err)
	}
	reads := &countingReaderAt{r: f}
	loaded, err := st.materialize(reads, st.selectedHead(), defaultSessionReplayLimits)
	if err != nil {
		t.Fatal(err)
	}
	// Each target reads its own payload, so the bytes read stay near the log's size; a shared
	// redaction record reloaded per target would read it 256 times over.
	if limit := info.Size() * 2; reads.n > limit {
		t.Fatalf("materializing %d encoded bytes read %d, limit %d; redaction records must not be reloaded per target", info.Size(), reads.n, limit)
	}
	msgs := loaded
	if len(msgs) != 256 {
		t.Fatalf("message count = %d", len(msgs))
	}
	for i, msg := range msgs {
		if msg.Content != strings.Repeat("r", 1024) {
			t.Fatalf("message %d did not use its redaction", i)
		}
	}
}

type countingReaderAt struct {
	r io.ReaderAt
	n int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.n += int64(n)
	return n, err
}
