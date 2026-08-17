package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

type anchorAuditSink struct {
	audits []event.AnchorSafetyAudit
}

func (s *anchorAuditSink) Emit(event.Event) {}

func (s *anchorAuditSink) RecordAnchorSafetyAudit(a event.AnchorSafetyAudit) {
	s.audits = append(s.audits, a)
}

func TestDeleteRangeRequiresReadAfterSameTurnWrite(t *testing.T) {
	var deleteCalls int32
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "delete_range", readOnly: false, calls: &deleteCalls})

	args := `{"path":"src/map.html","start_anchor":"before","end_anchor":"after"}`
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "delete_range", args),
			toolCallChunk("c2", "delete_range", args),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "edit the map"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&deleteCalls); got != 1 {
		t.Fatalf("delete_range executed %d times, want only the first call", got)
	}
	results := toolResults(a.sess.conversation, "delete_range")
	if len(results) != 2 {
		t.Fatalf("tool results = %d, want 2", len(results))
	}
	last := results[len(results)-1]
	for _, want := range []string{"[fresh read required]", "read_file", "multi_edit"} {
		if !strings.Contains(last, want) {
			t.Fatalf("blocked result should mention %q, got %q", want, last)
		}
	}
}

func TestEditFileAllowedAfterSameTurnWriteWithoutFreshRead(t *testing.T) {
	var editCalls int32
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "edit_file", readOnly: false, calls: &editCalls})

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "edit_file", `{"path":"src/map.html","old_string":"before","new_string":"after"}`),
			toolCallChunk("c2", "edit_file", `{"path":"src/map.html","old_string":"other","new_string":"updated"}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "edit two independent regions"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&editCalls); got != 2 {
		t.Fatalf("edit_file executed %d times, want both optimistic exact-match edits", got)
	}
	if last := lastToolResult(a.sess.conversation, "edit_file"); strings.Contains(last, "[fresh read required]") {
		t.Fatalf("edit_file should rely on its current-file uniqueness check, got %q", last)
	}
}

func TestDeleteRangeAllowedAfterFreshRead(t *testing.T) {
	var deleteCalls int32
	var readCalls int32
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "delete_range", readOnly: false, calls: &deleteCalls})
	reg.Add(fakeTool{name: "read_file", readOnly: true, calls: &readCalls})

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "delete_range", `{"path":"src/map.html","start_anchor":"before","end_anchor":"after"}`),
			toolCallChunk("c2", "read_file", `{"path":"src/map.html"}`),
			toolCallChunk("c3", "delete_range", `{"path":"src/map.html","start_anchor":"current","end_anchor":"final"}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "edit the map with a read between edits"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&readCalls); got != 1 {
		t.Fatalf("read_file executed %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&deleteCalls); got != 2 {
		t.Fatalf("delete_range executed %d times, want 2 after fresh read", got)
	}
	if last := lastToolResult(a.sess.conversation, "delete_range"); strings.Contains(last, "[fresh read required]") {
		t.Fatalf("fresh read should allow the second edit, got %q", last)
	}
}

func TestDeleteRangeStillRequiresReadAfterWindowedRead(t *testing.T) {
	var deleteCalls int32
	var readCalls int32
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "delete_range", readOnly: false, calls: &deleteCalls})
	reg.Add(fakeTool{name: "read_file", readOnly: true, calls: &readCalls})

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "delete_range", `{"path":"src/map.html","start_anchor":"before","end_anchor":"after"}`),
			toolCallChunk("c2", "read_file", `{"path":"src/map.html","offset":400,"limit":20}`),
			toolCallChunk("c3", "delete_range", `{"path":"src/map.html","start_anchor":"current","end_anchor":"final"}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "edit the map with a narrow read between edits"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&readCalls); got != 1 {
		t.Fatalf("read_file executed %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&deleteCalls); got != 1 {
		t.Fatalf("delete_range executed %d times, want only the first call", got)
	}
	if last := lastToolResult(a.sess.conversation, "delete_range"); !strings.Contains(last, "[fresh read required]") {
		t.Fatalf("windowed read should not allow the second edit, got %q", last)
	}
}

func TestDeleteRangeShadowRejectsSameBatchRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	sink := &anchorAuditSink{}
	reg := tool.NewRegistry()
	for _, tl := range (builtin.Workspace{Dir: dir, WriteRoots: []string{dir}}).Tools("write_file", "read_file", "delete_range") {
		reg.Add(tl)
	}
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("w", "write_file", `{"path":"sample.txt","content":"prefix\nstart\nmiddle\nend\nsuffix\n"}`),
			toolCallChunk("r", "read_file", `{"path":"sample.txt"}`),
			toolCallChunk("d", "delete_range", `{"path":"sample.txt","start_anchor":"start","end_anchor":"end"}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, sink)
	if err := a.Run(withNoClosedLoop(context.Background()), "delete the middle range"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sink.audits) != 1 {
		t.Fatalf("anchor audits = %+v, want one", sink.audits)
	}
	audit := sink.audits[0]
	if audit.Reason != anchorReasonSameBatchRead || !audit.SameBatchReadRejected || audit.ShadowAllowed || !audit.LegacyAllowed {
		t.Fatalf("same-batch audit = %+v, want legacy allow and shadow block", audit)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "prefix\nsuffix\n" {
		t.Fatalf("delete result = %q, want middle range removed", got)
	}
}

func TestDeleteRangeShadowMatchesAfterInsertionAboveTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	sink := &anchorAuditSink{}
	reg := tool.NewRegistry()
	for _, tl := range (builtin.Workspace{Dir: dir, WriteRoots: []string{dir}}).Tools("write_file", "read_file", "delete_range") {
		reg.Add(tl)
	}
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("w1", "write_file", `{"path":"sample.txt","content":"start\nmiddle\nend\nsuffix\n"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("r", "read_file", `{"path":"sample.txt"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("w2", "write_file", `{"path":"sample.txt","content":"prefix\nstart\nmiddle\nend\nsuffix\n"}`), {Type: provider.ChunkDone}},
		{toolCallChunk("d", "delete_range", `{"path":"sample.txt","start_anchor":"start","end_anchor":"end"}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, sink)
	if err := a.Run(withNoClosedLoop(context.Background()), "delete the middle range after an unrelated insertion"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sink.audits) != 1 {
		t.Fatalf("anchor audits = %+v, want one", sink.audits)
	}
	audit := sink.audits[0]
	if audit.Reason != anchorReasonExactMatch || !audit.ShadowAllowed || audit.LegacyAllowed {
		t.Fatalf("insertion audit = %+v, want shadow allow and legacy block", audit)
	}
	if got := lastToolResult(a.Session(), "delete_range"); !strings.Contains(got, "[fresh read required]") {
		t.Fatalf("shadow-only delete should retain legacy block, got %q", got)
	}
	if got, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if string(got) != "prefix\nstart\nmiddle\nend\nsuffix\n" {
		t.Fatalf("blocked delete changed file: %q", got)
	}
}

func TestMultiEditAllowedAfterSameTurnWrite(t *testing.T) {
	var editCalls int32
	var multiCalls int32
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "edit_file", readOnly: false, calls: &editCalls})
	reg.Add(fakeTool{name: "multi_edit", readOnly: false, calls: &multiCalls})

	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "edit_file", `{"path":"src/map.html","old_string":"before","new_string":"after"}`),
			toolCallChunk("c2", "multi_edit", `{"path":"src/map.html","edits":[{"old_string":"current","new_string":"final"}]}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(withNoClosedLoop(context.Background()), "edit the map atomically"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&editCalls); got != 1 {
		t.Fatalf("edit_file executed %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&multiCalls); got != 1 {
		t.Fatalf("multi_edit executed %d times, want 1", got)
	}
}
