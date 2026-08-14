package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func TestStableRecoveryPathPeelsNestedNames(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "chat.jsonl")
	nested := filepath.Join(dir, "chat-recovery-aaaaaaaaaaaaaaaa-recovery-bbbbbbbbbbbbbbbb.jsonl")
	gen := "gen-7"
	if got, want := stableRecoverySessionPath(nested, gen), stableRecoverySessionPath(root, gen); got != want {
		t.Fatalf("nested path %q, want %q", got, want)
	}
}

func TestSaveRecoveryBranchStampsStableRootDepth(t *testing.T) {
	dir := t.TempDir()

	path, stale := divergedSessionPair(t, dir, "session.jsonl")
	info, err := stale.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: path})
	if err != nil {
		t.Fatalf("SaveRecoveryBranch: %v", err)
	}
	if info.Meta.RecoveryDepth != 1 {
		t.Fatalf("first fork depth = %d, want 1", info.Meta.RecoveryDepth)
	}
	if info.Meta.ParentID != BranchID(path) {
		t.Fatalf("parent = %q, want root %q", info.Meta.ParentID, BranchID(path))
	}

	deeper, staleDeeper := divergedSessionPair(t, dir, "deeper.jsonl")
	stampRecoveryMeta(t, deeper, 1)
	info, err = staleDeeper.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: deeper})
	if err != nil {
		t.Fatalf("SaveRecoveryBranch from recovered file: %v", err)
	}
	if info.Meta.RecoveryDepth != 1 {
		t.Fatalf("stable fork depth = %d, want 1", info.Meta.RecoveryDepth)
	}
	if info.Meta.ParentID != recoveryRootID(deeper) {
		t.Fatalf("parent = %q, want root %q", info.Meta.ParentID, recoveryRootID(deeper))
	}

	capped, staleCapped := divergedSessionPair(t, dir, "capped.jsonl")
	stampRecoveryMeta(t, capped, SessionRecoveryMaxDepth)
	info, err = staleCapped.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: capped})
	if err != nil {
		t.Fatalf("SaveRecoveryBranch at historical cap: %v", err)
	}
	if info.Path == "" || info.Path == capped {
		t.Fatalf("capped parent did not write a stable recovery file: %q", info.Path)
	}
	forks, err := filepath.Glob(filepath.Join(dir, "capped-recovery-*.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var recovered []string
	for _, fork := range forks {
		if !strings.HasSuffix(fork, ".events.jsonl") {
			recovered = append(recovered, fork)
		}
	}
	if len(recovered) != 1 {
		t.Fatalf("stable recovery copies = %v, want 1", recovered)
	}
}

func TestRepeatedDivergenceRewritesOneRecoveryBranch(t *testing.T) {
	dir := t.TempDir()
	path, stale := divergedSessionPair(t, dir, "session.jsonl")
	first, err := stale.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: path})
	if err != nil {
		t.Fatalf("first SaveRecoveryBranch: %v", err)
	}
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "more local"})
	second, err := stale.SaveRecoveryBranch(RecoveryBranchOptions{OriginalPath: first.Path})
	if err != nil {
		t.Fatalf("second SaveRecoveryBranch: %v", err)
	}
	if second.Path != first.Path {
		t.Fatalf("recovery path rotated %q -> %q", first.Path, second.Path)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*-recovery-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, m := range matches {
		if !strings.HasSuffix(m, ".events.jsonl") {
			files = append(files, m)
		}
	}
	if len(files) != 1 {
		t.Fatalf("recovery files = %v, want 1", files)
	}
}
