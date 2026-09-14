package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/servecontract"
	"reasonix/internal/session"
	"reasonix/internal/sessioncontent"
	"reasonix/internal/transcript"
)

func remoteTranscriptFixture(server *httptest.Server) (*App, *remoteTab) {
	tab := &remoteTab{id: "remote", state: "ready", client: server.Client(), base: server.URL, gen: 1,
		routing: remoteTabSessionRouting{currentPath: "/session.jsonl"}}
	return &App{remoteTabs: map[string]*remoteTab{tab.id: tab}}, tab
}

func TestRemoteTranscriptNegotiatesOldServeWithoutMutation(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/transcript/snapshot" || r.URL.Query().Get("session") != "/session.jsonl" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("<html>old Serve index</html>"))
			}))
			defer server.Close()
			app, tab := remoteTranscriptFixture(server)
			result, err := app.RemoteTranscriptSnapshotForTab(tab.id, transcript.PageRequest{})
			if err != nil || result.Supported || result.Snapshot != nil {
				t.Fatalf("negotiation = %+v, %v", result, err)
			}
			if tab.state != "ready" || tab.gen != 1 {
				t.Fatal("capability probe changed the connection")
			}
		})
	}
}

// A Serve that does not advertise the outline capability must never be probed:
// the client keeps its loaded-turn rail instead of spending a round trip.
func TestRemoteTranscriptOutlineRequiresAdvertisedCapability(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>old Serve index</html>"))
	}))
	defer server.Close()
	app, tab := remoteTranscriptFixture(server)

	page, err := app.RemoteTranscriptOutlineForTab(tab.id, transcript.OutlineRequest{SnapshotID: "cut"})
	if err == nil || page.SnapshotID != "" {
		t.Fatalf("unadvertised outline = %+v, %v", page, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("an unadvertised capability issued %d requests", requests.Load())
	}

	// An advertised capability that answers with something other than protocol
	// data is a real error, not a silent downgrade to "unsupported".
	tab.capabilities = map[string]bool{servecontract.TranscriptOutlineV1: true}
	if _, err := app.RemoteTranscriptOutlineForTab(tab.id, transcript.OutlineRequest{SnapshotID: "cut"}); err == nil {
		t.Fatal("an HTML homepage response was accepted as an outline")
	}
	if requests.Load() != 1 {
		t.Fatalf("advertised capability issued %d requests, want 1", requests.Load())
	}
}

func TestRemoteTranscriptOutlineReadsAdvertisedEndpoint(t *testing.T) {
	want := transcript.OutlinePage{
		Boundary: transcript.Boundary{ProtocolVersion: transcript.ProtocolVersion, SnapshotID: "cut"},
		Entries:  []transcript.OutlineEntry{{ID: "m:2", MessageID: "2", Turn: 2, Order: 4, Prompt: "second", Answer: "answer"}},
		Total:    2, NextOffset: 2, Done: true,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transcript/outline" || r.URL.Query().Get("session") != "/session.jsonl" {
			t.Errorf("unexpected request %s", r.URL.String())
		}
		var request transcript.OutlineRequest
		if err := json.Unmarshal([]byte(r.URL.Query().Get("request")), &request); err != nil || request.SnapshotID != "cut" {
			t.Errorf("request = %+v, %v", request, err)
		}
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer server.Close()
	app, tab := remoteTranscriptFixture(server)
	tab.capabilities = map[string]bool{servecontract.TranscriptOutlineV1: true}

	page, err := app.RemoteTranscriptOutlineForTab(tab.id, transcript.OutlineRequest{SnapshotID: "cut"})
	if err != nil || page.Total != 2 || len(page.Entries) != 1 || page.Entries[0].ID != "m:2" {
		t.Fatalf("outline = %+v, %v", page, err)
	}
}

func TestRemoteTranscriptRejectsLateSessionResponse(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_ = json.NewEncoder(w).Encode(transcript.Snapshot{Boundary: transcript.Boundary{ProtocolVersion: 1, SnapshotID: "old"}})
	}))
	defer server.Close()
	app, tab := remoteTranscriptFixture(server)
	done := make(chan error, 1)
	go func() { _, err := app.RemoteTranscriptSnapshotForTab(tab.id, transcript.PageRequest{}); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	app.remoteTabMu.Lock()
	tab.routing.currentPath = "/new-session.jsonl"
	app.remoteTabMu.Unlock()
	close(release)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "replaced session") {
		t.Fatalf("late response accepted: %v", err)
	}
}

func TestRemoteTabMetadataDoesNotRequestHistory(t *testing.T) {
	var historyReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/history" {
			historyReads.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/status" {
			_, _ = w.Write([]byte(`{"sessionPath":"/session.jsonl","running":false}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	app, tab := remoteTranscriptFixture(server)
	metadata, err := app.RemoteTabMetadata(tab.id)
	if err != nil || historyReads.Load() != 0 || len(metadata.History) != 0 {
		t.Fatalf("metadata requested history: count=%d error=%v", historyReads.Load(), err)
	}
}

func TestRemoteCanonicalSessionHistoryUsesNegotiatedIdentity(t *testing.T) {
	ref := sessioncontent.Ref{Digest: strings.Repeat("a", 64), Bytes: 3, MediaType: "text/plain"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("sessionId"); got != "canonical" {
			t.Errorf("sessionId = %q", got)
		}
		switch r.URL.Path {
		case "/session/open":
			_ = json.NewEncoder(w).Encode(session.SessionOpenView{SnapshotSequence: 9, Recent: session.RecentSnapshot{Entries: []session.PersistentMessage{{MessageID: "m1", Role: "user"}}}})
		case "/session-history/page":
			if r.URL.Query().Get("cursor") != "next" || r.URL.Query().Get("limit") != "7" {
				t.Errorf("page query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(session.MessageHistoryPage{Messages: []session.PersistentMessage{{MessageID: "m1", Role: "user", ContentRef: &ref}}, SnapshotSequence: 9})
		case "/session-history/locate":
			if r.URL.Query().Get("messageId") != "m1" || r.URL.Query().Get("snapshot") != "9" {
				t.Errorf("locate query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(session.MessageLocation{Status: "ready", MessageID: "m1", SnapshotSequence: 9, Cursor: "located"})
		case "/session-history/content":
			var request struct {
				Ref    sessioncontent.Ref `json:"ref"`
				Offset int64              `json:"offset"`
				Length int64              `json:"length"`
			}
			if err := json.Unmarshal([]byte(r.URL.Query().Get("request")), &request); err != nil {
				t.Fatal(err)
			}
			if request.Ref.Digest != ref.Digest || request.Offset != 0 || request.Length != 3 {
				t.Errorf("content request = %+v", request)
			}
			_ = json.NewEncoder(w).Encode(SessionHistoryContentChunk{Data: base64.StdEncoding.EncodeToString([]byte("big")), NextOffset: 3, Done: true})
		case "/session-history/search":
			if r.URL.Query().Get("q") != "needle" || r.URL.Query().Get("cursor") != "older" || r.URL.Query().Get("limit") != "5" {
				t.Errorf("search query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(session.SearchHistoryPage{Hits: []session.SearchHistoryHit{{MessageID: "m1", Preview: "needle"}}, SnapshotSequence: 9})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	app, tab := remoteTranscriptFixture(server)
	tab.capabilities = map[string]bool{serveCapabilitySessionContentV1: true, serveCapabilitySessionReadV2: true}
	tab.session.sessionID = "canonical"
	view, err := app.RemoteSessionOpenForTab(tab.id)
	if err != nil || view.SnapshotSequence != 9 || len(view.Recent.Entries) != 1 {
		t.Fatalf("open = %+v, %v", view, err)
	}
	page, err := app.RemoteSessionHistoryPageForTab(tab.id, "next", 7)
	if err != nil || page.SnapshotSequence != 9 || len(page.Messages) != 1 {
		t.Fatalf("page = %+v, %v", page, err)
	}
	location, err := app.RemoteLocateSessionMessageForTab(tab.id, "m1", 9)
	if err != nil || location.Status != "ready" || location.Cursor != "located" {
		t.Fatalf("location = %+v, %v", location, err)
	}
	chunk, err := app.RemoteSessionHistoryContentForTab(tab.id, ref, 0)
	if err != nil || chunk.Data != base64.StdEncoding.EncodeToString([]byte("big")) || !chunk.Done {
		t.Fatalf("chunk = %+v, %v", chunk, err)
	}
	search, err := app.RemoteSearchSessionHistoryForTab(tab.id, "needle", "older", 5)
	if err != nil || len(search.Hits) != 1 || search.Hits[0].MessageID != "m1" {
		t.Fatalf("search = %+v, %v", search, err)
	}
}

func TestRemoteCanonicalSessionHistoryRequiresCapability(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reads.Add(1) }))
	defer server.Close()
	app, tab := remoteTranscriptFixture(server)
	if _, err := app.RemoteSessionHistoryPageForTab(tab.id, "", 0); err == nil {
		t.Fatal("canonical history unexpectedly enabled")
	}
	if reads.Load() != 0 {
		t.Fatalf("network reads = %d", reads.Load())
	}
}
