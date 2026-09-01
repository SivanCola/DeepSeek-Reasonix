package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/provider"
)

type takeoverRecordSink struct {
	mu     sync.Mutex
	events []event.Event
}

func (s *takeoverRecordSink) Emit(e event.Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func TestCLITakeoverManagerRetriesSameFrameBatchInOrder(t *testing.T) {
	var mu sync.Mutex
	var requests [][]eventwire.Event
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
		requests = append(requests, append([]eventwire.Event(nil), body.Frames...))
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"reclaimRequested": false})
	}))
	defer srv.Close()

	m := newCLITakeoverManager(&takeoverRecordSink{}, nil)
	m.binding = &cliTakeoverBinding{
		path: "session.jsonl", record: cliServeRecord{base: srv.URL}, client: srv.Client(),
		grant: cliTakeoverGrant{MirrorID: "mirror-1"},
	}
	m.Emit(event.Event{Kind: event.Text, Text: "first"})
	m.Emit(event.Event{Kind: event.Text, Text: "second"})
	if !m.push(false) || !m.push(false) {
		t.Fatal("frame push stopped unexpectedly")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || len(requests[0]) != 2 || len(requests[1]) != 2 {
		t.Fatalf("request batches = %+v, want two complete two-frame batches", requests)
	}
	for i := range requests[0] {
		if requests[0][i].Text != requests[1][i].Text {
			t.Fatalf("retry reordered frame %d: %q != %q", i, requests[0][i].Text, requests[1][i].Text)
		}
	}
}

func TestCLITakeoverManagerChunksWithoutDroppingFrames(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Frames []eventwire.Event `json:"frames"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Frames) > cliTakeoverMaxFrames {
			t.Fatalf("batch size = %d", len(body.Frames))
		}
		for _, frame := range body.Frames {
			got = append(got, frame.Text)
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"reclaimRequested": false})
	}))
	defer srv.Close()
	m := newCLITakeoverManager(&takeoverRecordSink{}, nil)
	m.binding = &cliTakeoverBinding{
		path: "session.jsonl", record: cliServeRecord{base: srv.URL}, client: srv.Client(),
		grant: cliTakeoverGrant{MirrorID: "mirror-1"},
	}
	for i := 0; i < cliTakeoverMaxFrames+37; i++ {
		m.Emit(event.Event{Kind: event.Text, Text: strconv.Itoa(i)})
	}
	if !m.push(false) || !m.push(false) {
		t.Fatal("chunked frame push stopped unexpectedly")
	}
	if len(got) != cliTakeoverMaxFrames+37 {
		t.Fatalf("received %d frames", len(got))
	}
	for i, text := range got {
		if text != strconv.Itoa(i) {
			t.Fatalf("frame %d = %q", i, text)
		}
	}
}

func TestCLITakeoverManagerHeartbeatSendsEmptyFrameBatch(t *testing.T) {
	got := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Frames []eventwire.Event `json:"frames"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		got <- len(body.Frames)
		_ = json.NewEncoder(w).Encode(map[string]bool{"reclaimRequested": false})
	}))
	defer srv.Close()
	m := newCLITakeoverManager(&takeoverRecordSink{}, nil)
	m.binding = &cliTakeoverBinding{
		path: "session.jsonl", record: cliServeRecord{base: srv.URL}, client: srv.Client(),
		grant: cliTakeoverGrant{MirrorID: "mirror-1"},
	}
	if !m.push(true) {
		t.Fatal("heartbeat stopped the manager")
	}
	if frames := <-got; frames != 0 {
		t.Fatalf("heartbeat frames = %d, want 0", frames)
	}
}

func TestCLITakeoverManagerReclaimReturnsLeaseAndSignalsExit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "active.jsonl")
	session := agent.NewSession("system")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	if err := session.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, nil, loaded, agent.Options{}, &takeoverRecordSink{})
	ctrl := control.New(control.Options{Executor: exec, SessionDir: dir, SessionPath: path})
	defer ctrl.Close()
	leases := control.NewSessionLeaseKeeper()
	defer leases.Release()
	if err := leases.Rebind(path); err != nil {
		t.Fatal(err)
	}
	if err := leases.BindControllerAuthority(ctrl); err != nil {
		t.Fatal(err)
	}

	var mirrorEnded atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/external/frames":
			_ = json.NewEncoder(w).Encode(map[string]any{"reclaimRequested": true, "reclaimMode": "wait"})
		case "/mirror-end":
			mirrorEnded.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	exited := make(chan struct{}, 1)
	m := newCLITakeoverManager(&takeoverRecordSink{}, leases)
	m.AttachController(ctrl)
	m.SetYieldCallback(func() { exited <- struct{}{} })
	m.Activate(&cliTakeoverBinding{
		path: path, record: cliServeRecord{base: srv.URL}, client: srv.Client(),
		grant: cliTakeoverGrant{
			MirrorID: "mirror-1", SourceWriterID: "serve-writer", ReturnHandoffID: "return-1",
		},
	})
	m.Emit(event.Event{Kind: event.Text, Text: "answer"})
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("reclaim did not return the lease")
	}
	if !m.Returned() || !mirrorEnded.Load() {
		t.Fatalf("returned=%v mirrorEnded=%v", m.Returned(), mirrorEnded.Load())
	}
	info, err := agent.LoadSessionLeaseInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.HandoffTo != "serve-writer" || info.HandoffID != "return-1" {
		t.Fatalf("reverse reservation = %+v", info)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCLITakeoverManagerTransitionsBetweenMirrorsAtomically(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current.jsonl")
	nextPath := filepath.Join(dir, "next.jsonl")
	leases := control.NewSessionLeaseKeeper()
	defer leases.Release()
	if err := leases.Rebind(current); err != nil {
		t.Fatal(err)
	}
	nextSource, err := agent.TryAcquireSessionLease(nextPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := nextSource.ReleaseForHandoff(agent.SessionWriterID(), "forward-next"); err != nil {
		t.Fatal(err)
	}

	var oldEnded atomic.Bool
	oldServe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mirror-end" {
			oldEnded.Store(true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"reclaimRequested": false})
	}))
	defer oldServe.Close()
	m := newCLITakeoverManager(&takeoverRecordSink{}, leases)
	m.binding = &cliTakeoverBinding{
		path: current, record: cliServeRecord{base: oldServe.URL}, client: oldServe.Client(),
		grant: cliTakeoverGrant{MirrorID: "old-mirror", SourceWriterID: "old-serve", ReturnHandoffID: "return-old"},
	}
	next := &cliTakeoverBinding{
		path:  nextPath,
		grant: cliTakeoverGrant{MirrorID: "next-mirror", SourceWriterID: agent.SessionWriterID(), HandoffID: "forward-next"},
	}
	next.previous, err = leases.RebindDetachingWithHandoff(nextPath, agent.SessionWriterID(), "forward-next")
	if err != nil {
		t.Fatal(err)
	}
	next.priorMirror = m.binding
	if err := next.commitPrevious(m); err != nil {
		t.Fatalf("commitPrevious: %v", err)
	}
	if got := leases.HeldPath(); got != agent.CanonicalSessionPath(nextPath) {
		t.Fatalf("held path = %q, want next", got)
	}
	if !oldEnded.Load() {
		t.Fatal("old mirror was not ended after the atomic keeper transition")
	}
	info, err := agent.LoadSessionLeaseInfo(current)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.HandoffTo != "old-serve" || info.HandoffID != "return-old" {
		t.Fatalf("old reverse reservation = %+v", info)
	}
}

func TestCLIFailedTakeoverRestoresSourceAndReturnsTarget(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current.jsonl")
	target := filepath.Join(dir, "target.jsonl")
	leases := control.NewSessionLeaseKeeper()
	defer leases.Release()
	if err := leases.Rebind(current); err != nil {
		t.Fatal(err)
	}
	targetSource, err := agent.TryAcquireSessionLease(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := targetSource.ReleaseForHandoff(agent.SessionWriterID(), "forward-target"); err != nil {
		t.Fatal(err)
	}
	previous, err := leases.RebindDetachingWithHandoff(target, agent.SessionWriterID(), "forward-target")
	if err != nil {
		t.Fatal(err)
	}
	var mirrorEnded atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mirror-end" {
			mirrorEnded.Store(true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	binding := &cliTakeoverBinding{
		path: target, previous: previous, record: cliServeRecord{base: srv.URL}, client: srv.Client(),
		grant: cliTakeoverGrant{
			MirrorID: "target-mirror", SourceWriterID: "target-serve", ReturnHandoffID: "return-target",
		},
	}
	cliReturnFailedTakeover(binding, leases)
	if got := leases.HeldPath(); got != agent.CanonicalSessionPath(current) {
		t.Fatalf("held path = %q, want restored source", got)
	}
	info, err := agent.LoadSessionLeaseInfo(target)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.HandoffTo != "target-serve" || info.HandoffID != "return-target" {
		t.Fatalf("target reverse reservation = %+v", info)
	}
	if !mirrorEnded.Load() {
		t.Fatal("failed target mirror was not ended")
	}
}
