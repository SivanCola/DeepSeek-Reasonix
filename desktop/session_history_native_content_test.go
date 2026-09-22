package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/session"
	"reasonix/internal/store"
)

func TestNativeHistorySchemaOneColdContentStaysOnBinding(t *testing.T) {
	a := historySliceTestApp(t)
	t.Cleanup(a.closeHistoryReaders)
	tab := newColdHistoryTab(t, a)
	dir := tabSessionDir(tab)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	tab.SessionPath = filepath.Join(dir, "schema1.jsonl")
	checkpoint := []byte("{\"role\":\"user\",\"content\":\"obsolete checkpoint\"}\n")
	if err := os.WriteFile(tab.SessionPath, checkpoint, 0600); err != nil {
		t.Fatal(err)
	}
	answer := strings.Repeat("原始内容🧭", 60000)
	event, err := json.Marshal(map[string]any{"schema_version": 1, "type": "replace", "messages": []provider.Message{historySliceUser(0, "event question"), historySliceAssistant(0, answer)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.SessionEventLog(tab.SessionPath), event, 0600); err != nil {
		t.Fatal(err)
	}
	handle, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := a.ReadSessionHistorySlice(handle.ID, HistorySliceRequest{Entries: 2})
	if err != nil || page.Status != "ready" || page.Page.Source != "event-log" || len(page.Page.Entries) != 2 {
		t.Fatalf("cold event slice: %+v %v", page, err)
	}
	outline, err := a.ReadSessionHistoryOutline(handle.ID, session.HistoryOutlineRequest{})
	if err != nil || outline.Status != "ready" || len(outline.Entries) != 1 || outline.Entries[0].Prompt != "event question" {
		t.Fatalf("cold event outline: %+v %v", outline, err)
	}
	var ref HistoryContentRef
	for _, entry := range page.Page.Entries {
		for _, candidate := range entry.Refs {
			if candidate.Field == "content" {
				ref = candidate
			}
		}
	}
	if ref.ReadHandleID != handle.ID {
		t.Fatalf("content lost its read owner: %+v", ref)
	}
	var full strings.Builder
	for i := 0; ; i++ {
		chunk := a.HistoryContentForTab(tab.ID, ref, i)
		if chunk.Stale {
			t.Fatalf("bound content went stale at chunk %d", i)
		}
		full.WriteString(chunk.Data)
		if chunk.Done {
			break
		}
	}
	if full.String() != answer {
		t.Fatal("cold event content was truncated or read from the obsolete checkpoint")
	}
	if chunk := a.HistoryContentForTab("other-tab", ref, 0); !chunk.Stale || chunk.Data != "" {
		t.Fatal("another navigation accepted this content ref")
	}
	if chunk, err := a.HistoryContentForTarget(SessionSelector{}, ref, 0); err != nil || !chunk.Stale {
		t.Fatalf("bound ref fell back to a management target: %+v %v", chunk, err)
	}
	a.ReleaseSessionHistoryRead(handle.ID)
	// Even reopening the same physical source does not rebind an old ref.
	next, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer a.ReleaseSessionHistoryRead(next.ID)
	if chunk := a.HistoryContentForTab(tab.ID, ref, 0); !chunk.Stale || chunk.Data != "" {
		t.Fatal("released content ref adopted the successor's reader")
	}
	after, _ := os.ReadFile(tab.SessionPath)
	afterEvent, _ := os.ReadFile(store.SessionEventLog(tab.SessionPath))
	if string(after) != string(checkpoint) || string(afterEvent) != string(event) || tab.Ctrl != nil {
		t.Fatal("cold content rewrote source storage or created a controller")
	}
	if _, err := os.Stat(store.SessionDisplayIndex(tab.SessionPath)); !os.IsNotExist(err) {
		t.Fatal("cold content triggered compatibility-sidecar repair")
	}
}
