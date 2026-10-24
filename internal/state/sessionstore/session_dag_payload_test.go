package sessionstore

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/state/store"
)

func TestDAGPayloadReferencesPreserveOverlayAndHeadSemantics(t *testing.T) {
	lines := baseDAG(t)
	lines = append(lines,
		dagLine(t, map[string]any{"type": "system", "head": "main", "msgs": []any{map[string]any{"role": "system", "content": "inherited"}}}),
		dagLine(t, map[string]any{"type": "fork", "head": "main", "new_head": "fork", "from": "m4"}),
		dagLine(t, map[string]any{"type": "system", "head": "main", "msgs": []any{map[string]any{"role": "system", "content": "main only"}}}),
		dagLine(t, map[string]any{"type": "patch", "target": "m4", "msgs": []any{map[string]any{"role": "assistant", "content": "patch one"}}}),
		dagLine(t, map[string]any{"type": "redact", "targets": map[string]any{"m4": []any{map[string]any{"role": "assistant", "content": "redacted"}}}}),
		dagLine(t, map[string]any{"type": "patch", "target": "m4", "msgs": []any{map[string]any{"role": "assistant", "content": "patch two"}}}),
		dagMsg(t, "m4", "m1", "main", map[string]any{"role": "assistant", "content": "duplicate"}),
		dagLine(t, map[string]any{"type": "select", "head": "fork"}),
	)
	s, err := LoadSession(writeDAGSession(t, lines...))
	if err != nil {
		t.Fatal(err)
	}
	want := "system:inherited|user:<session-context>|user:hello|assistant:redacted"
	if got := strings.Join(contents(s.Snapshot()), "|"); got != want {
		t.Fatalf("selected transcript = %q, want %q", got, want)
	}
}

func TestDAGPayloadReferencesKeepHistoricalValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entry   map[string]any
		damaged bool
	}{
		{"malformed off-path message", map[string]any{"type": "message", "id": "bad", "head": "other", "msgs": []any{map[string]any{"role": "user", "content": 42}}}, true},
		{"malformed duplicate", map[string]any{"type": "message", "id": "m4", "head": "other", "msgs": []any{map[string]any{"role": "assistant", "content": 42}}}, true},
		{"unknown patch target", map[string]any{"type": "patch", "target": "unknown", "msgs": []any{map[string]any{"role": "user", "content": 42}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeDAGSession(t, append(baseDAG(t), dagLine(t, tc.entry))...)
			msgs, damaged, err := loadSessionDAGMessages(path, defaultSessionReplayLimits)
			if err != nil || damaged != tc.damaged {
				t.Fatalf("damaged=%v, err=%v, want damaged=%v", damaged, err, tc.damaged)
			}
			if got := strings.Join(contents(msgs), "|"); got != "system:sys|user:<session-context>|user:hello|assistant:hi" {
				t.Fatalf("historical validation changed the prefix: %q", got)
			}
		})
	}
}

func TestDAGPayloadReferencesRejectTruncationBetweenPasses(t *testing.T) {
	path := writeDAGSession(t, baseDAG(t)...)
	logPath := store.SessionEventLog(path)
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	state, err := replaySessionDAG(f, logPath, defaultSessionReplayLimits)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(logPath, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := state.materialize(f, state.selectedHead(), defaultSessionReplayLimits); err == nil {
		t.Fatal("truncated historical payload was returned as a transcript")
	}
}

func TestDAGPayloadReferencesIgnoreOverwrittenLargeBodies(t *testing.T) {
	path := writeReplayMemoryFixture(t, 65, 1<<20, false)
	f, err := os.OpenFile(store.SessionEventLog(path), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 65 {
		line := dagLine(t, map[string]any{"type": "redact", "targets": map[string]any{fmtMessageID(i): []any{map[string]any{"role": "user", "content": "removed"}}}})
		if _, err := f.WriteString(line + "\n"); err != nil {
			f.Close()
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.Snapshot()
	if len(msgs) != 67 || msgs[1].Content != "removed" || msgs[65].Content != "removed" || msgs[66].Content != "done" {
		t.Fatalf("redactions were not materialized: count=%d", len(msgs))
	}
}

func fmtMessageID(i int) string {
	return "message-" + strconv.Itoa(i)
}
