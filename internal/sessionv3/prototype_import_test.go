package sessionv3

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/fileutil"
	"reasonix/internal/provider"
)

func writePrototypeStore(t *testing.T, dir string, events []Event, torn string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{SchemaVersion: SchemaVersion, Codec: PrototypeCodec, SessionID: "prototype", CreatedAt: time.Now().UTC(), WriterGeneration: 1}
	manifestBytes, _ := json.Marshal(manifest)
	if err := fileutil.AtomicWriteFileStrict(filepath.Join(dir, "manifest.json"), append(manifestBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	var log []byte
	if len(events) > 0 {
		for i := range events {
			events[i].ID = "event-" + string(rune('a'+i))
			events[i].Sequence = uint64(i + 1)
		}
		hash, err := hashOperation("prototype", "", events)
		if err != nil {
			t.Fatal(err)
		}
		commit := Commit{SchemaVersion: SchemaVersion, Codec: PrototypeCodec, RecordType: "commit", ID: "prototype-commit", OperationID: "prototype-operation", OperationHash: hash, FirstSequence: 1, EventCount: len(events), WriterGeneration: 1, CreatedAt: time.Now().UTC(), Events: events}
		line, _ := json.Marshal(commit)
		log = append(line, '\n')
	}
	log = append(log, torn...)
	if err := fileutil.AtomicWriteFileStrict(filepath.Join(dir, "events.jsonl"), log, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrototypeRequiresExplicitImportAndPreservesTornTail(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "prototype")
	payload, _ := json.Marshal(map[string]any{"messages": []provider.Message{{ID: "message-1", Role: provider.RoleUser, Content: "hello"}}, "reason": "prototype"})
	writePrototypeStore(t, source, []Event{{Kind: "context/replace", Payload: payload}}, `{"torn":`)
	if _, err := Open(source, "prototype"); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("direct prototype Open error = %v", err)
	}

	result, err := ImportPrototype(t.Context(), source, filepath.Join(root, "final"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ImportedEvents != 1 {
		t.Fatalf("imported events = %d", result.ImportedEvents)
	}
	store, err := Open(result.TargetDir, result.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	projection := store.Snapshot().Projection
	if len(projection.Messages) != 1 || projection.Messages[0].ID != "message-1" || len(projection.ModelMessages) != 1 {
		t.Fatalf("projection = %+v", projection)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	frozen, err := os.ReadFile(filepath.Join(result.TargetDir, "legacy", "prototype", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(frozen[len(frozen)-len(`{"torn":`):]) != `{"torn":` {
		t.Fatalf("prototype tail was not preserved: %q", frozen)
	}

	reused, err := ImportPrototype(t.Context(), source, filepath.Join(root, "final"))
	if err != nil || !reused.Reused || reused.TargetID != result.TargetID {
		t.Fatalf("reused import = %+v, %v", reused, err)
	}
}

func TestPrototypeImportRejectsUnknownRequiredEvent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "prototype")
	writePrototypeStore(t, source, []Event{{Kind: "future/required"}}, "")
	if _, err := ImportPrototype(t.Context(), source, filepath.Join(root, "final")); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("unknown required import error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "final"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name()[0] != '.' {
			t.Fatalf("failed import published %q", entry.Name())
		}
	}
}
