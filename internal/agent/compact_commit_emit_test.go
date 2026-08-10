package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// reentrantSnapshotSink re-enters ContextMaintenanceSnapshot on every emit,
// which takes compactionMu. commitSummaryProjection must unlock before Emit.
type reentrantSnapshotSink struct {
	agent *Agent
	mu    sync.Mutex
	n     int
}

func (s *reentrantSnapshotSink) Emit(e event.Event) {
	if e.Kind != event.ContextMaintenanceEvent {
		return
	}
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	if s.agent != nil {
		_ = s.agent.ContextMaintenanceSnapshot()
	}
}

func TestCommitSummaryEmitsOutsideCompactionLock(t *testing.T) {
	prov := &fakeProvider{reply: "digest for reentrant emit"}
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("work line\n", 800)},
		{Role: provider.RoleUser, Content: "continue"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("more work\n", 800)},
		{Role: provider.RoleUser, Content: "tail"},
		{Role: provider.RoleAssistant, Content: "ok"},
	}}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	sink := &reentrantSnapshotSink{}
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 20_000, CompactRatio: 0.5, RecentKeep: 2,
		SessionPath: path, WorkspaceID: "ws", ModelRef: "p/m",
	}, sink)
	sink.agent = a
	if err := a.CompactNow(context.Background(), ""); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	sink.mu.Lock()
	n := sink.n
	sink.mu.Unlock()
	if n == 0 {
		t.Fatal("expected context_maintenance emit after checkpoint install")
	}
	if got := a.currentProjectionVersion(); got != 1 {
		t.Fatalf("projection version = %d, want 1", got)
	}
}

func TestLoadProjectionSidecarDoesNotRewriteExactKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "u"},
	}
	hash := coveredPrefixHash(msgs, len(msgs))
	key := promptCacheKey("ws", BranchID(path), "p/m")
	st := CompactionState{
		SchemaVersion:     compactionStateSchemaCurrent,
		TranscriptVersion: 0,
		PromptCacheKey:    key,
		Projection: ContextProjection{
			Messages: msgs, CoveredCount: len(msgs), CoveredPrefixHash: hash,
			ProjectionVersion: 3, TranscriptVersion: 0,
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := SaveCompactionState(path, st); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ContextStatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	a := New(nil, tool.NewRegistry(), &Session{Messages: append([]provider.Message(nil), msgs...)}, Options{
		SessionPath: path, WorkspaceID: "ws", ModelRef: "p/m",
	}, event.Discard)
	a.LoadProjectionSidecar(path)
	if a.currentProjectionVersion() != 3 {
		t.Fatalf("version = %d, want 3", a.currentProjectionVersion())
	}
	after, err := os.ReadFile(ContextStatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("exact-key restore rewrote sidecar (%d -> %d bytes)", len(before), len(after))
	}
}

func TestLoadProjectionSidecarNormalizesNativeKeyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "u"},
	}
	hash := coveredPrefixHash(msgs, len(msgs))
	key := promptCacheKey("ws", BranchID(path), "p/m")
	st := CompactionState{
		SchemaVersion:     compactionStateSchemaCurrent,
		TranscriptVersion: 0,
		PromptCacheKey:    key + "|context-editing-native-anthropic",
		Projection: ContextProjection{
			Messages: msgs, CoveredCount: len(msgs), CoveredPrefixHash: hash,
			ProjectionVersion: 2, TranscriptVersion: 0,
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := SaveCompactionState(path, st); err != nil {
		t.Fatal(err)
	}
	a := New(nil, tool.NewRegistry(), &Session{Messages: append([]provider.Message(nil), msgs...)}, Options{
		SessionPath: path, WorkspaceID: "ws", ModelRef: "p/m",
	}, event.Discard)
	a.LoadProjectionSidecar(path)
	if a.currentProjectionVersion() != 2 {
		t.Fatalf("version = %d, want 2", a.currentProjectionVersion())
	}
	loaded, ok, err := LoadCompactionState(path)
	if err != nil || !ok {
		t.Fatalf("reload: ok=%v err=%v", ok, err)
	}
	if loaded.PromptCacheKey != key {
		t.Fatalf("PromptCacheKey = %q, want normalized %q", loaded.PromptCacheKey, key)
	}
	before, err := os.ReadFile(ContextStatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	a2 := New(nil, tool.NewRegistry(), &Session{Messages: append([]provider.Message(nil), msgs...)}, Options{
		SessionPath: path, WorkspaceID: "ws", ModelRef: "p/m",
	}, event.Discard)
	a2.LoadProjectionSidecar(path)
	after, err := os.ReadFile(ContextStatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("second restore rewrote already-normalized sidecar")
	}
}
