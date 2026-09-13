package sessionv3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/provider"
)

func reviewRuntime(t *testing.T) (*Service, *Runtime) {
	t.Helper()
	service, err := NewService("local", NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions-v3")))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), CreateOptions{SessionID: "review"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.close(context.Background()) })
	return service, runtime
}

func TestStateSnapshotOmitsHistory(t *testing.T) {
	_, runtime := reviewRuntime(t)
	payload, err := json.Marshal(map[string]any{"message": provider.Message{ID: "visible", Role: provider.RoleUser, Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), "message", []Event{{Kind: "message/complete", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	state := runtime.StateSnapshot().Session
	if state.EventSequence != 1 || len(state.Projection.Messages) != 0 || len(state.Projection.ModelMessages) != 0 {
		t.Fatalf("activity snapshot includes history or loses its sequence: %+v", state)
	}
	if len(runtime.Snapshot().Session.Projection.Messages) != 1 {
		t.Fatal("state snapshot changed the stored history")
	}
}



func TestSessionIdentityRejectsPathsWithoutCreatingFiles(t *testing.T) {
	persistence := NewFilesystemPersistence(t.TempDir())
	for _, id := range []string{"..", "../escape", "a/b", `a\b`, "/absolute", ".", ""} {
		if _, err := persistence.Open(id, ReadWrite); err == nil {
			t.Fatalf("accepted path as identity: %q", id)
		}
	}
	entries, err := os.ReadDir(persistence.Root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid opens changed the root: %v, %v", entries, err)
	}
}

func TestCancelReceiptDoesNotWaitForSessionProjection(t *testing.T) {
	service, runtime := reviewRuntime(t)
	ctx, activity, err := runtime.BeginOwnedActivity(t.Context(), "model")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { activity.Finish(nil) })
	store := runtime.Session().Handle.(*Store)
	store.mu.Lock()
	defer store.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := service.CancelSession(runtime.Ref()); done <- err }()
	select {
	case err := <-done:
		if err != nil || !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("cancel = %v, context = %v", err, ctx.Err())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel receipt waits for the projection lock")
	}
}

func TestUnknownRequiredPrefixDoesNotTruncateTail(t *testing.T) {
	_, runtime := reviewRuntime(t)
	store := runtime.Session().Handle.(*Store)
	if _, err := store.Append(t.Context(), Batch{OperationID: "known", Events: []Event{{Kind: "diagnostic"}}}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.close(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.dir, "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"kind":"diagnostic"`), []byte(`"kind":"future/required"`), 1)
	data = append(data, []byte(`{"torn":`)...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(store.dir, store.SessionID()); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("open = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatalf("unsupported log was changed: %v", err)
	}
}

func TestSnapshotCannotMutateAcceptedMessageMetadata(t *testing.T) {
	_, runtime := reviewRuntime(t)
	payload, err := json.Marshal(map[string]any{"message": provider.Message{ID: "m1", Role: provider.RoleUser, Images: []string{"original"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), "input", []Event{{Kind: "message/complete", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.Session().Snapshot()
	snapshot.Projection.Messages[0].Images[0] = "changed"
	snapshot.Projection.ModelMessages[0].Images[0] = "changed-again"
	if got := runtime.Session().Snapshot().Projection.Messages[0].Images[0]; got != "original" {
		t.Fatalf("observer mutated accepted message: %q", got)
	}
}

func TestOldRuntimeDisposerCannotCloseSuccessor(t *testing.T) {
	service, old := reviewRuntime(t)
	if err := service.CloseRuntime(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	next, err := service.Open(t.Context(), old.Ref())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseRuntime(context.Background(), next) })
	if err := service.CloseRuntime(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	if got, ok := service.Runtime(next.Ref()); !ok || got != next {
		t.Fatal("old disposer removed successor")
	}
	if _, err := next.Session().AppendBatch(t.Context(), "still-open", []Event{{Kind: "diagnostic", Optional: true}}); err != nil {
		t.Fatal(err)
	}
}

func TestTitleCanReturnToEarlierValue(t *testing.T) {
	service, runtime := reviewRuntime(t)
	for _, title := range []string{"A", "B", "A"} {
		if err := service.SetTitle(t.Context(), runtime.Ref(), title); err != nil {
			t.Fatal(err)
		}
		if got := runtime.Session().Snapshot().Projection.Title; got != title {
			t.Fatalf("title = %q, want %q", got, title)
		}
	}
}

func TestCloseFailureUnregistersReleasedWriter(t *testing.T) {
	service, runtime := reviewRuntime(t)
	store := runtime.Session().Handle.(*Store)
	failure := errors.New("disk unavailable")
	store.writeFn = func(context.Context, io.Writer, []byte) error { return failure }
	if _, err := runtime.Session().AppendBatch(t.Context(), "pending", []Event{{Kind: "diagnostic", Optional: true}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), runtime.Ref()); !errors.Is(err, failure) {
		t.Fatalf("close = %v", err)
	}
	if _, ok := service.Runtime(runtime.Ref()); ok {
		t.Fatal("closed writer remains attachable")
	}
	if err := service.Close(t.Context(), runtime.Ref()); !errors.Is(err, failure) {
		t.Fatalf("repeat close = %v", err)
	}
	next, err := service.Open(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background(), next.Ref()) })
	if next == runtime {
		t.Fatal("open returned released writer")
	}
}

func TestRewindBeforeFirstTurnPreservesInitialization(t *testing.T) {
	service, runtime := reviewRuntime(t)
	if _, err := runtime.Session().AppendBatch(t.Context(), "config", []Event{{Kind: "session/config", Payload: []byte(`{"modelRef":"test/model"}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Handle.Append(t.Context(), Batch{OperationID: "input", TurnID: "first", Events: []Event{{Kind: "turn/start"}, {Kind: "turn/end", Payload: []byte(`{"status":"completed"}`)}}}); err != nil {
		t.Fatal(err)
	}
	child, err := service.Rewind(t.Context(), runtime.Ref(), "first", "rewound")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background(), child.Ref()) })
	if got := child.Session().Snapshot().Projection.ModelRef; got != "test/model" {
		t.Fatalf("model = %q", got)
	}
}

func TestFinishedActivityCancelsItsContext(t *testing.T) {
	_, runtime := reviewRuntime(t)
	ctx, activity, err := runtime.BeginOwnedActivity(t.Context(), "model")
	if err != nil {
		t.Fatal(err)
	}
	activity.Finish(nil)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("finished activity retains live cancellation context")
	}
}

func TestForkCopiesOwnedAttachments(t *testing.T) {
	service, runtime := reviewRuntime(t)
	dir := runtime.Session().Handle.(*Store).dir
	asset := filepath.Join(dir, "attachments", "input.txt")
	if err := os.MkdirAll(filepath.Dir(asset), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, []byte("owned context"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Handle.Append(t.Context(), Batch{OperationID: "input", TurnID: "first", Events: []Event{{Kind: "turn/start"}, {Kind: "turn/end", Payload: []byte(`{"status":"completed"}`)}}}); err != nil {
		t.Fatal(err)
	}
	child, err := service.Fork(t.Context(), runtime.Ref(), "first", "child")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background(), child.Ref()) })
	if err := service.Delete(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(child.Session().Handle.(*Store).dir, "attachments", "input.txt"))
	if err != nil || string(data) != "owned context" {
		t.Fatalf("child attachment = %q, %v", data, err)
	}
}
