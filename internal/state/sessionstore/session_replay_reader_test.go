package sessionstore

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"reasonix/internal/state/store"
)

func TestSessionRecordReaderFramesPrettyAndAdjacentRecords(t *testing.T) {
	first := "{\n\"value\": \"quoted \\\" } [ text\",\n\"nested\": [1, {\"a\": true}]\n}"
	second := `{"schema_version":1}`
	input := " \n" + first + "\t" + second
	r := newSessionRecordReader(strings.NewReader(input), "fixture", int64(len(input)))
	for i, want := range []string{first, second} {
		raw, start, err := r.next()
		if err != nil || string(raw) != want || input[start:r.offset] != want {
			t.Fatalf("record %d: %q at %d, err=%v", i, raw, start, err)
		}
	}
	if _, _, err := r.next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end = %v, want EOF", err)
	}
}

func TestSessionRecordReaderRefusesGrowthPastEncodedBudget(t *testing.T) {
	input := `{"one":1}{"two":2}`
	r := newSessionRecordReader(strings.NewReader(input), "fixture", 9)
	if _, _, err := r.next(); err != nil {
		t.Fatal(err)
	}
	_, _, err := r.next()
	var limitErr *SessionReplayLimitError
	if !errors.As(err, &limitErr) || limitErr.Resource != "encoded_bytes" || limitErr.Value != 10 {
		t.Fatalf("growth error = %v", err)
	}
}

func TestSessionRecordReaderBoundsOneLargeRecord(t *testing.T) {
	input := io.MultiReader(strings.NewReader(`{"content":"`), io.LimitReader(replayPayloadReader{}, 64<<20), strings.NewReader(`"}`))
	r := newSessionRecordReader(input, "fixture", 512<<20)
	_, _, err := r.next()
	var limitErr *SessionReplayLimitError
	if !errors.As(err, &limitErr) || limitErr.Resource != "event_record_bytes" || limitErr.Value != (64<<20)+1 {
		t.Fatalf("record bound error = %v", err)
	}
}

type replayPayloadReader struct{}

func (replayPayloadReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestNativeReplayBoundsAggregateMessagePayload(t *testing.T) {
	path := writeDAGSession(t)
	f, err := os.Create(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	body := strings.Repeat("x", 1<<20)
	for i := range 65 {
		if err := enc.Encode(map[string]any{"schema_version": 1, "type": "append", "message_index": i, "messages": []any{map[string]any{"role": "user", "content": body}}}); err != nil {
			f.Close()
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	msgs, fromEvents, damaged, err := loadSessionMessages(path)
	var limitErr *SessionReplayLimitError
	if !errors.As(err, &limitErr) || limitErr.Resource != "message_payload_bytes" || msgs != nil || !fromEvents || damaged {
		t.Fatalf("native load = %d messages, events=%v, damaged=%v, err=%v", len(msgs), fromEvents, damaged, err)
	}
}

func TestNullEventKeepsUnsupportedSchemaRefusal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
	}{
		{"native", []string{`{"schema_version":1,"type":"replace","messages":[{"role":"user","content":"prefix"}]}`, "null"}},
		{"dag", append(baseDAG(t), "null")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeDAGSession(t, tc.lines...)
			s, err := LoadSession(path)
			if err == nil || s != nil {
				t.Fatalf("unsupported null record returned a recoverable transcript: session=%v err=%v", s != nil, err)
			}
		})
	}
}
