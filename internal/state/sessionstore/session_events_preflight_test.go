package sessionstore

import (
	"bytes"
	"errors"
	"runtime"
	"testing"
)

func TestSessionEventPreflightLargeStringsBoundedAllocations(t *testing.T) {
	// Include escaped quotes and structural bytes so content cannot be mistaken
	// for JSON structure while checking the replay budgets.
	content := bytes.Repeat([]byte(`text\"\\[]{}:,`), 512<<10)
	raw := append([]byte(`[{"role":"assistant","content":"`), content...)
	raw = append(raw, []byte(`","tool_calls":[{}]}]`)...)

	t.Run("successful preflight", func(t *testing.T) {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		messages, collections, err := preflightSessionEventMessages("bounded.events.jsonl", raw, 0, 0, sessionReplayLimits{
			maxMessages: 1, maxCollectionItems: 1,
		})
		runtime.ReadMemStats(&after)
		if err != nil || messages != 1 || collections != 1 {
			t.Fatalf("preflight = messages:%d collections:%d err:%v", messages, collections, err)
		}
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 256<<10 {
			t.Fatalf("preflight allocated %d bytes for %d encoded bytes, want at most 256 KiB", allocated, len(raw))
		}
	})

	t.Run("consumer rejects before typed decoding", func(t *testing.T) {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		messages, collections, err := decodeSessionEventMessages("bounded.events.jsonl", raw, 0, 0, sessionReplayLimits{
			maxMessages: 1, maxCollectionItems: 0,
		})
		runtime.ReadMemStats(&after)
		var limitErr *SessionReplayLimitError
		if !errors.As(err, &limitErr) || limitErr.Resource != "message_collection_items" || limitErr.Value != 1 {
			t.Fatalf("decode error = %v, want first collection item rejected", err)
		}
		if messages != nil || collections != 0 {
			t.Fatalf("decode = messages:%v collections:%d, want no partial result", messages, collections)
		}
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 256<<10 {
			t.Fatalf("rejected decode allocated %d bytes for %d encoded bytes, want at most 256 KiB", allocated, len(raw))
		}
	})
}

func TestSessionEventPreflightBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                string
		raw                 string
		existingMessages    int
		existingCollections int
		maxMessages         int
		maxCollections      int
		messages            int
		collections         int
		resource            string
		value               int64
		invalid             bool
	}{
		{name: "empty at limits", raw: `[]`, existingMessages: 2, existingCollections: 3, maxMessages: 2, maxCollections: 3, collections: 3},
		{name: "aggregate nested arrays", raw: `[{"unknown":[[],{"nested":[null,true,false,1,-2.5e+3]}],"other":[{}]}]`, existingMessages: 1, existingCollections: 2, maxMessages: 2, maxCollections: 10, messages: 1, collections: 10},
		{name: "escaped key and string", raw: `[{"escaped\"key":"\\\"[,]{}","images":["one"]}]`, maxMessages: 1, maxCollections: 1, messages: 1, collections: 1},
		{name: "message before invalid typed value", raw: `[{},42]`, existingMessages: 1, maxMessages: 2, maxCollections: 5, resource: "messages", value: 3},
		{name: "collection before invalid typed value", raw: `[{"tool_calls":[{},42]}]`, existingCollections: 2, maxMessages: 1, maxCollections: 3, resource: "message_collection_items", value: 4},
		{name: "object values are not collection items", raw: `[{"a":{},"b":null,"c":42}]`, maxMessages: 1, maxCollections: 0, messages: 1},
		{name: "non array", raw: `{}`, maxMessages: 5, maxCollections: 5, invalid: true},
		{name: "truncated string", raw: `[{"content":"unterminated`, maxMessages: 5, maxCollections: 5, invalid: true},
		{name: "invalid escape", raw: `[{"content":"\q"}]`, maxMessages: 5, maxCollections: 5, invalid: true},
		{name: "invalid number", raw: `[{"unknown":01}]`, maxMessages: 5, maxCollections: 5, invalid: true},
		{name: "missing object comma", raw: `[{"a":1 "b":2}]`, maxMessages: 5, maxCollections: 5, invalid: true},
		{name: "missing array comma", raw: `[{"unknown":[1 2]}]`, maxMessages: 5, maxCollections: 5, invalid: true},
		{name: "mismatched delimiter", raw: `[{"unknown":[1}}]`, maxMessages: 5, maxCollections: 5, invalid: true},
		{name: "trailing comma", raw: `[{},]`, maxMessages: 5, maxCollections: 5, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages, collections, err := preflightSessionEventMessages("boundaries.events.jsonl", []byte(tc.raw), tc.existingMessages, tc.existingCollections, sessionReplayLimits{
				maxMessages: tc.maxMessages, maxCollectionItems: tc.maxCollections,
			})
			if tc.resource != "" {
				var limitErr *SessionReplayLimitError
				if !errors.Is(err, ErrSessionReplayLimitExceeded) || !errors.As(err, &limitErr) || limitErr.Resource != tc.resource || limitErr.Value != tc.value {
					t.Fatalf("preflight error = %v, want %s=%d", err, tc.resource, tc.value)
				}
			} else if tc.invalid {
				if err == nil || errors.Is(err, ErrSessionReplayLimitExceeded) {
					t.Fatalf("preflight error = %v, want malformed JSON error", err)
				}
			} else {
				if err != nil || messages != tc.messages || collections != tc.collections {
					t.Fatalf("preflight = messages:%d collections:%d err:%v, want messages:%d collections:%d", messages, collections, err, tc.messages, tc.collections)
				}
				return
			}
			if messages != 0 || collections != tc.existingCollections {
				t.Fatalf("failed preflight = messages:%d collections:%d, want unchanged counts", messages, collections)
			}
		})
	}
}
