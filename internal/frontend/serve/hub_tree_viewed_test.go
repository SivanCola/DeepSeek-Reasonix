package serve

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/state/sessionstore"
)

func TestTreeSendsWhereTheNewAnswersBeginOnlyForUnreadRows(t *testing.T) {
	t.Setenv("REASONIX_HOME", testenv.TempDir(t))
	root := testenv.TempDir(t)
	h := NewHub(HubOptions{})
	hubRuntime(t, h, root)
	srv := httptest.NewServer(operatorHandler(h))
	defer srv.Close()

	unread := filepath.Join(SessionDirFor(root), "unread.jsonl")
	seen := filepath.Join(SessionDirFor(root), "seen.jsonl")
	writeSessionAt(t, unread)
	writeSessionAt(t, seen)
	viewed := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	for _, p := range []string{unread, seen} {
		if err := sessionstore.RecordSessionFinished(p, viewed, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := sessionstore.RecordSessionFinished(unread, time.Now(), true); err != nil {
		t.Fatal(err)
	}

	rows := map[string]treeSession{}
	for _, s := range treeFor(t, srv, "").Sessions {
		rows[filepath.Base(s.Path)] = s
	}
	if got := rows["unread.jsonl"]; !got.Unread || got.ViewedAt != viewed.UnixMilli() {
		t.Errorf("unread row: unread=%v viewedAt=%d, want true and %d", got.Unread, got.ViewedAt, viewed.UnixMilli())
	}
	if got := rows["seen.jsonl"]; got.Unread || got.ViewedAt != 0 {
		t.Errorf("seen row: unread=%v viewedAt=%d, want false and 0", got.Unread, got.ViewedAt)
	}
}
