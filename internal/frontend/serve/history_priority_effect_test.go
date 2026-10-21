package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"reasonix/internal/assembly/boot"
	"reasonix/internal/base/backgroundwork"
	"reasonix/internal/contract/config"
	"reasonix/internal/contract/provider"
	"reasonix/internal/state/sessionstore"
)

func TestResumePreemptsProjectionButTreeRefreshDoesNot(t *testing.T) {
	f := newUnreadHarness(t, "plain")
	path := filepath.Join(SessionDirFor(f.root), "history.jsonl")
	session := sessionstore.NewSession("system")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "saved history"})
	if err := session.Save(path); err != nil {
		t.Fatal(err)
	}
	bc := NewBroadcaster()
	ctrl, err := boot.Build(t.Context(), boot.Options{Sink: bc, Home: f.home, WorkspaceRoot: f.root, SessionDir: SessionDirFor(f.root)})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub(HubOptions{})
	rt, err := h.Adopt(New(ctrl, bc, config.ServeConfig{}), bc)
	if err != nil {
		ctrl.Close()
		t.Fatal(err)
	}
	t.Cleanup(h.Shutdown)
	job, release, err := backgroundwork.Shared.Acquire(context.Background(), backgroundwork.Rebuild)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	h.tree(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/tree?progressive=1", nil))
	if job.Err() != nil {
		t.Fatal("tree refresh preempted its producer")
	}
	if _, err := rt.Server.resumeInto(path); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(context.Cause(job), backgroundwork.ErrPreempted) {
		t.Fatalf("cause=%v", context.Cause(job))
	}
	if got := rt.Server.Controller().History(); len(got) < 2 || got[len(got)-1].Content != "saved history" {
		t.Fatalf("history=%v", got)
	}
	w := httptest.NewRecorder()
	rt.Server.historyPage(w, httptest.NewRequest(http.MethodGet, "/history/page?limit=1", nil))
	var page historyPageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || len(page.Messages) != 1 || page.Messages[0].Content != "saved history" || page.Messages[0].MsgIndex != page.Start || !page.HasMoreBefore || page.NextCursor == "" {
		t.Fatalf("paged resume=%d %+v", w.Code, page)
	}
}
