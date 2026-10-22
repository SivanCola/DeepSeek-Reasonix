package sessionstore

import (
	"os"
	"runtime"
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
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	s, err := LoadSession(path)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.Snapshot()
	if len(msgs) != 256 {
		t.Fatalf("message count = %d", len(msgs))
	}
	for i, msg := range msgs {
		if msg.Content != strings.Repeat("r", 1024) {
			t.Fatalf("message %d did not use its redaction", i)
		}
	}
	if allocated, budget := after.TotalAlloc-before.TotalAlloc, uint64(info.Size())*10+8<<20; allocated > budget {
		t.Fatalf("materializing %d encoded bytes allocated %d, budget %d; redaction records must not be reloaded per target", info.Size(), allocated, budget)
	}
}
