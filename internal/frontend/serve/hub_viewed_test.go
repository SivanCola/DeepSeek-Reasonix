package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/state/sessionstore"
)

func TestMarkSessionViewedClearsOnlyListedUnreadSessions(t *testing.T) {
	t.Setenv("REASONIX_HOME", testenv.TempDir(t))
	root := testenv.TempDir(t)
	h := NewHub(HubOptions{})
	hubRuntime(t, h, root)
	srv := httptest.NewServer(operatorHandler(h))
	defer srv.Close()

	unread := func(p string) bool {
		m, ok, _ := sessionstore.LoadBranchMeta(p)
		return ok && m.Unread()
	}
	listed := filepath.Join(SessionDirFor(root), "listed.jsonl")
	writeSessionAt(t, listed)
	outside := filepath.Join(testenv.TempDir(t), "outside.jsonl")
	writeSessionAt(t, outside)
	for _, p := range []string{listed, outside} {
		if err := sessionstore.RecordSessionFinished(p, time.Now(), true); err != nil {
			t.Fatal(err)
		}
		if !unread(p) {
			t.Fatalf("%s did not start unread", p)
		}
	}
	post := func(p string) int {
		body, _ := json.Marshal(map[string]string{"path": p})
		resp, err := http.Post(srv.URL+"/tree/sessions/viewed", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(listed); code != http.StatusNoContent {
		t.Fatalf("listed: %d", code)
	}
	if unread(listed) {
		t.Error("a listed session is still unread")
	}
	if code := post(listed); code != http.StatusNoContent {
		t.Fatalf("listed again: %d, want 204 (idempotent)", code)
	}
	if code := post(outside); code != http.StatusForbidden {
		t.Fatalf("outside: %d, want 403", code)
	}
	if !unread(outside) {
		t.Error("a session outside every workspace was marked read")
	}
}
