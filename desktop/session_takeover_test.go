package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/eventwire"
)

func TestTakeoverOwnershipEncodesOpaqueSessionPath(t *testing.T) {
	want := `C:\Users\测试 User\sessions\a&b%20.jsonl`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("session"); got != want {
			t.Errorf("session query = %q, want %q", got, want)
		}
		_ = json.NewEncoder(w).Encode(SessionTakeoverView{Holder: "serve"})
	}))
	defer srv.Close()
	if _, err := takeoverOwnership(context.Background(), srv.Client(), srv.URL, want); err != nil {
		t.Fatal(err)
	}
}

func TestTakeoverMirrorRetriesSameDrainedFramesInOrder(t *testing.T) {
	var mu sync.Mutex
	var batches [][]eventwire.Event
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Frames []eventwire.Event `json:"frames"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		batches = append(batches, append([]eventwire.Event(nil), body.Frames...))
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"reclaimRequested": false})
	}))
	defer srv.Close()

	m := &takeoverMirror{
		sessionPath: "session.jsonl", client: srv.Client(),
		record: takeoverServeRecord{base: srv.URL}, grant: takeoverGrant{MirrorID: "mirror-1"},
		wake: make(chan struct{}, 1),
	}
	m.forwardEvent(event.Event{Kind: event.Text, Text: "first"})
	m.forwardEvent(event.Event{Kind: event.Text, Text: "second"})
	if !m.pushOnce(false) || !m.pushOnce(false) {
		t.Fatal("forwarder stopped unexpectedly")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 2 || len(batches[0]) != 2 || len(batches[1]) != 2 {
		t.Fatalf("batches = %+v, want two complete batches", batches)
	}
	for i := range batches[0] {
		if batches[0][i].Text != batches[1][i].Text {
			t.Fatalf("retry reordered frame %d: %q != %q", i, batches[0][i].Text, batches[1][i].Text)
		}
	}
}

func TestTakeoverMirrorChunksWithoutDroppingFrames(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Frames []eventwire.Event `json:"frames"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Frames) > takeoverMirrorMaxQueue {
			t.Fatalf("batch size = %d", len(body.Frames))
		}
		for _, frame := range body.Frames {
			got = append(got, frame.Text)
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"reclaimRequested": false})
	}))
	defer srv.Close()
	m := &takeoverMirror{
		sessionPath: "session.jsonl", client: srv.Client(), record: takeoverServeRecord{base: srv.URL},
		grant: takeoverGrant{MirrorID: "mirror-1"}, wake: make(chan struct{}, 1),
	}
	for i := 0; i < takeoverMirrorMaxQueue+37; i++ {
		m.forwardEvent(event.Event{Kind: event.Text, Text: strconv.Itoa(i)})
	}
	if !m.pushOnce(false) || !m.pushOnce(false) {
		t.Fatal("chunked frame push stopped unexpectedly")
	}
	if len(got) != takeoverMirrorMaxQueue+37 {
		t.Fatalf("received %d frames", len(got))
	}
	for i, text := range got {
		if text != strconv.Itoa(i) {
			t.Fatalf("frame %d = %q", i, text)
		}
	}
}

func TestRebindWithTakeoverMirrorDoesNotReenterAppLock(t *testing.T) {
	app, tab, _, _, targetPath, loaded := newAtomicRebindTestApp(t)
	key := sessionRuntimeKey(targetPath)
	app.takeoverMu.Lock()
	if app.takeoverMirrors == nil {
		app.takeoverMirrors = map[string]*takeoverMirror{}
	}
	app.takeoverMirrors[key] = &takeoverMirror{app: app, key: key, sessionPath: targetPath}
	app.takeoverMu.Unlock()

	done := make(chan error, 1)
	go func() { done <- app.rebindTabToLoadedSessionPath(tab, targetPath, loaded) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rebind deadlocked while reconnecting takeover mirror")
	}
}

func TestOwnershipProbeFailurePreservesSpectatorPin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporary failure", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	app := NewApp()
	app.remoteTabs = map[string]*remoteTab{}
	tab := &remoteTab{
		id: "remote-1", gen: 4, client: srv.Client(), selectionRevision: 9,
		routing: remoteTabSessionRouting{currentPath: "/sessions/a.jsonl"},
		session: remoteTabSessionState{takenOver: true},
	}
	app.remoteTabs[tab.id] = tab
	app.markRemoteTabSpectatorIfLocalOwned(context.Background(), tab.id, tab.client, srv.URL, tab.gen)
	if !tab.session.takenOver {
		t.Fatal("failed ownership probe cleared the spectator pin")
	}
}

func TestLateOwnershipProbeCannotChangeNewSelection(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_ = json.NewEncoder(w).Encode(SessionTakeoverView{Holder: "external", Mirrored: true})
	}))
	defer srv.Close()
	app := NewApp()
	app.remoteTabs = map[string]*remoteTab{}
	tab := &remoteTab{
		id: "remote-1", gen: 4, client: srv.Client(), selectionRevision: 9,
		routing: remoteTabSessionRouting{currentPath: "/sessions/old.jsonl"},
	}
	app.remoteTabs[tab.id] = tab
	done := make(chan struct{})
	go func() {
		app.markRemoteTabSpectatorIfLocalOwned(context.Background(), tab.id, tab.client, srv.URL, tab.gen)
		close(done)
	}()
	<-started
	app.remoteTabMu.Lock()
	tab.selectionRevision++
	tab.routing.currentPath = "/sessions/new.jsonl"
	app.remoteTabMu.Unlock()
	close(release)
	<-done
	if tab.session.takenOver {
		t.Fatal("late ownership probe marked the newer selection read-only")
	}
}

func TestLateReclaimSuccessCannotUnlockNewSelection(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/reclaim" {
			close(started)
			<-release
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "not available", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	app := NewApp()
	app.remoteTabs = map[string]*remoteTab{}
	tab := &remoteTab{
		id: "remote-1", state: "ready", gen: 4, client: srv.Client(), base: srv.URL, selectionRevision: 9,
		routing: remoteTabSessionRouting{currentPath: "/sessions/old.jsonl"},
		session: remoteTabSessionState{takenOver: true},
	}
	app.remoteTabs[tab.id] = tab
	done := make(chan error, 1)
	go func() { done <- app.ReclaimRemoteTabSession(tab.id) }()
	<-started
	app.remoteTabMu.Lock()
	tab.selectionRevision++
	tab.routing.currentPath = "/sessions/new.jsonl"
	tab.session.takenOver = true
	app.remoteTabMu.Unlock()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !tab.session.takenOver {
		t.Fatal("late reclaim response unlocked the newer selection")
	}
}
