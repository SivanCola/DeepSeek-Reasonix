package sessionstore

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"reasonix/internal/state/store"
)

func writeReplayMemoryFixture(t *testing.T, count, bytesPerMessage int, rewind bool) string {
	t.Helper()
	path := writeDAGSession(t)
	f, err := os.Create(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	emit := func(record map[string]any) {
		t.Helper()
		record["schema_version"] = 2
		record["at"] = "2026-10-21T15:00:00Z"
		if err := enc.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	message := func(id, parent, content string) {
		emit(map[string]any{"type": "message", "id": id, "parent": parent, "head": "main", "msgs": []any{map[string]any{"role": "user", "content": content}}})
	}
	message("root", "", "root")
	body := strings.Repeat("x", bytesPerMessage)
	parent := "root"
	for i := range count {
		id := fmt.Sprintf("message-%d", i)
		message(id, parent, body)
		parent = id
	}
	if rewind {
		emit(map[string]any{"type": "rewind", "head": "main", "to": "root"})
		parent = "root"
	}
	message("end", parent, "done")
	return path
}

func TestDAGReplayDoesNotMaterializeDiscardedHistory(t *testing.T) {
	path := writeReplayMemoryFixture(t, 16, 512<<10, true)
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
	if got := strings.Join(contents(s.Snapshot()), "|"); got != "user:root|user:done" {
		t.Fatalf("selected transcript = %q", got)
	}
	if allocated, budget := after.TotalAlloc-before.TotalAlloc, uint64(info.Size())*7; allocated > budget {
		t.Fatalf("loading %d encoded bytes with 8 selected content bytes allocated %d bytes, budget %d", info.Size(), allocated, budget)
	}
}

func TestDAGReplayRefusesOversizedSelectedPayloadWithoutFallback(t *testing.T) {
	path := writeReplayMemoryFixture(t, 65, 1<<20, false)
	checkpoint := []byte("{\"role\":\"user\",\"content\":\"stale checkpoint\"}\n")
	if err := os.WriteFile(path, checkpoint, 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := store.SessionEventLog(path)
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSum := sha256.Sum256(before)
	runtime.GC()
	loaded, err := LoadSession(path)
	var limitErr *SessionReplayLimitError
	if !errors.As(err, &limitErr) || limitErr.Resource != "message_payload_bytes" || limitErr.Limit != 64<<20 {
		t.Fatalf("load = %v, %v; want typed selected-payload refusal", loaded != nil, err)
	}
	if loaded != nil {
		t.Fatal("a refused event log fell back to the older checkpoint")
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(after) != beforeSum {
		t.Fatal("refused replay changed the event log")
	}
	afterCheckpoint, err := os.ReadFile(path)
	if err != nil || string(afterCheckpoint) != string(checkpoint) {
		t.Fatalf("checkpoint changed: %q, %v", afterCheckpoint, err)
	}
}
