package mirror

import (
	"testing"

	"reasonix/internal/remote/protocol"
)

func TestCompareCheckpoint(t *testing.T) {
	local := SessionMirror{SessionID: "s1", Revision: 3, Digest: "aaa"}
	if got := CompareCheckpoint(local, protocol.CheckpointEvent{SessionID: "s1", Revision: 3, Digest: "aaa"}); got != ResultSynced {
		t.Fatalf("got %s", got)
	}
	if got := CompareCheckpoint(local, protocol.CheckpointEvent{SessionID: "s1", Revision: 4, Digest: "bbb"}); got != ResultUpdated {
		t.Fatalf("got %s", got)
	}
	if got := CompareCheckpoint(local, protocol.CheckpointEvent{SessionID: "s1", Revision: 2, Digest: "ccc"}); got != ResultFork {
		t.Fatalf("got %s", got)
	}
	if got := CompareCheckpoint(local, protocol.CheckpointEvent{SessionID: "s1", Revision: 3, Digest: "zzz"}); got != ResultFork {
		t.Fatalf("got %s", got)
	}
	if got := CompareCheckpoint(SessionMirror{}, protocol.CheckpointEvent{SessionID: "s1", Revision: 1, Digest: "x"}); got != ResultCreated {
		t.Fatalf("got %s", got)
	}
}

func TestApplyCheckpointAtomicAndFork(t *testing.T) {
	store := Store{Base: t.TempDir()}
	fp, ws := "SHA256:fp", "/home/u/work"
	artifacts := map[string][]byte{"session.jsonl": []byte(`{"id":"s1"}`)}
	digest := DigestArtifacts(artifacts)
	man := protocol.CheckpointManifest{SessionID: "s1", Revision: 1, Digest: digest, Label: "one"}

	got, err := store.ApplyCheckpoint(fp, ws, man, artifacts)
	if err != nil || got != ResultCreated {
		t.Fatalf("create: %v %s", err, got)
	}

	// Same revision+digest: still updates files but Compare says synced → Apply treats as synced path.
	// ApplyCheckpoint re-writes; Compare on existing equal → ResultSynced.
	got, err = store.ApplyCheckpoint(fp, ws, man, artifacts)
	if err != nil || got != ResultSynced {
		t.Fatalf("synced: %v %s", err, got)
	}

	// Newer remote revision.
	artifacts2 := map[string][]byte{"session.jsonl": []byte(`{"id":"s1","r":2}`)}
	digest2 := DigestArtifacts(artifacts2)
	man2 := protocol.CheckpointManifest{SessionID: "s1", Revision: 2, Digest: digest2}
	got, err = store.ApplyCheckpoint(fp, ws, man2, artifacts2)
	if err != nil || got != ResultUpdated {
		t.Fatalf("update: %v %s", err, got)
	}

	// Fork: local has higher revision after we try to apply older.
	manOld := protocol.CheckpointManifest{SessionID: "s1", Revision: 1, Digest: digest}
	got, err = store.ApplyCheckpoint(fp, ws, manOld, artifacts)
	if err == nil || got != ResultFork {
		t.Fatalf("fork: %v %s", err, got)
	}

	// Digest mismatch rejects.
	bad := protocol.CheckpointManifest{SessionID: "s1", Revision: 3, Digest: "deadbeef"}
	if _, err := store.ApplyCheckpoint(fp, ws, bad, artifacts2); err == nil {
		t.Fatal("expected digest mismatch")
	}

	// Read back.
	loaded, arts, err := store.ReadCheckpoint(fp, ws, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 2 {
		t.Fatalf("revision = %d", loaded.Revision)
	}
	if string(arts["session.jsonl"]) != `{"id":"s1","r":2}` {
		t.Fatalf("artifact = %q", arts["session.jsonl"])
	}
}

func TestPathTraversalRejected(t *testing.T) {
	store := Store{Base: t.TempDir()}
	arts := map[string][]byte{"../escape": []byte("x")}
	man := protocol.CheckpointManifest{SessionID: "s", Revision: 1, Digest: DigestArtifacts(arts)}
	if _, err := store.ApplyCheckpoint("fp", "/w", man, arts); err == nil {
		t.Fatal("expected path rejection")
	}
}
