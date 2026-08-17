package agent

import (
	"path/filepath"
	"reflect"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestCoveredPrefixHashIgnoresLocalToolRawContent(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read", Arguments: `{}`}}},
		{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read", Content: "bounded", RawContent: "full result A"},
	}
	first := coveredPrefixHash(msgs, len(msgs))
	msgs[1].RawContent = "full result B"
	if second := coveredPrefixHash(msgs, len(msgs)); second != first {
		t.Fatal("local RawContent edit changed the provider-visible covered-prefix hash")
	}
	msgs[1].Content = "different bounded result"
	if second := coveredPrefixHash(msgs, len(msgs)); second == first {
		t.Fatal("provider-visible Content edit did not invalidate the covered-prefix hash")
	}
}

func TestLoadProjectionSidecarMigratesPromotedV3ToolHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read", Arguments: `{}`}}},
		{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read", Content: "bounded", RawContent: "full result"},
	}
	legacyHash := promotedCoveredPrefixHash(msgs, len(msgs))
	if legacyHash == coveredPrefixHash(msgs, len(msgs)) {
		t.Fatal("fixture does not distinguish promoted and bounded hashes")
	}
	if err := SaveCompactionState(path, CompactionState{
		SchemaVersion:  compactionStateSchemaV3,
		PromptCacheKey: promptCacheKey("ws", BranchID(path), "model"),
		LastReceipt: &ContextMaintenanceReceipt{
			Status: "applied", Action: "prune", CoveredPrefixHash: legacyHash,
		},
		Projection: ContextProjection{
			Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "system"}, formatSummaryMessage("summary")},
			CoveredCount:      len(msgs),
			CoveredPrefixHash: legacyHash,
		},
	}); err != nil {
		t.Fatal(err)
	}
	sess := &Session{Messages: msgs}
	a := New(nil, nil, sess, Options{SessionPath: path, WorkspaceID: "ws", ModelRef: "model"}, event.Discard)
	want := coveredPrefixHash(msgs, len(msgs))
	if got := a.sess.compactionState.Projection.CoveredPrefixHash; got != want {
		t.Fatalf("loaded hash = %q, want migrated %q", got, want)
	}
	disk, ok, err := LoadCompactionState(path)
	if err != nil || !ok {
		t.Fatalf("load migrated sidecar: ok=%v err=%v", ok, err)
	}
	if got := disk.Projection.CoveredPrefixHash; got != want {
		t.Fatalf("persisted hash = %q, want %q", got, want)
	}
	if disk.LastReceipt == nil || disk.LastReceipt.CoveredPrefixHash != want {
		t.Fatalf("receipt hash was not normalized: %+v", disk.LastReceipt)
	}
	mutated := append([]provider.Message(nil), msgs...)
	mutated[len(mutated)-1].RawContent = "changed after migration"
	if !projectionValid(a.sess.compactionState, mutated, a.currentPromptCacheKey()) {
		t.Fatal("local RawContent edit invalidated a bounded provider projection")
	}
}

func TestLoadProjectionSidecarDropsUnverifiableBodyButKeepsReceipt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "system"}, {Role: provider.RoleUser, Content: "task"}}
	if err := SaveCompactionState(path, CompactionState{
		SchemaVersion:  compactionStateSchemaV3,
		PromptCacheKey: promptCacheKey("ws", BranchID(path), "model"),
		Projection: ContextProjection{
			Messages:     []provider.Message{{Role: provider.RoleSystem, Content: "system"}, formatSummaryMessage("summary")},
			CoveredCount: len(msgs), CoveredPrefixHash: "unrelated-stale-hash",
		},
		LastReceipt: &ContextMaintenanceReceipt{Status: "applied", Action: "summary", CoveredPrefixHash: "unrelated-stale-hash"},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(nil, nil, &Session{Messages: msgs}, Options{SessionPath: path, WorkspaceID: "ws", ModelRef: "model"}, event.Discard)
	if len(a.sess.compactionState.Projection.Messages) != 0 {
		t.Fatal("unverifiable projection body survived load")
	}
	if a.sess.compactionState.LastReceipt == nil || a.sess.compactionState.LastReceipt.Action != "summary" {
		t.Fatalf("maintenance receipt was lost: %+v", a.sess.compactionState.LastReceipt)
	}
	if got := a.Session().Snapshot(); !reflect.DeepEqual(got, msgs) {
		t.Fatalf("canonical transcript changed: got=%+v want=%+v", got, msgs)
	}
}
