package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/provider"
	"reasonix/internal/servecontract"
	canonical "reasonix/internal/session"
	"reasonix/internal/transcript"
)

func TestTranscriptHTTPBindsSessionAndImmutableContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	session := agent.NewSession("system")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "question"})
	session.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("body", 20000)})
	if err := session.Save(path); err != nil {
		t.Fatal(err)
	}
	bc := NewBroadcaster()
	executor := agent.New(nil, nil, session, agent.Options{}, bc)
	ctrl := control.New(control.Options{Executor: executor, SessionDir: dir, SessionPath: path, Sink: bc})
	defer ctrl.Close()
	server := httptest.NewServer(New(ctrl, bc, config.ServeConfig{}).Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/transcript/snapshot?session=" + url.QueryEscape(path))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("snapshot status=%d cache=%q", response.StatusCode, response.Header.Get("Cache-Control"))
	}
	var snapshot transcript.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ProtocolVersion != 1 || len(snapshot.Records) != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	ref := snapshot.Records[1].Refs[0]
	encoded, _ := json.Marshal(transcript.ContentRequest{ContentRef: ref})
	contentResponse, err := http.Get(server.URL + "/transcript/content?session=" + url.QueryEscape(path) + "&request=" + url.QueryEscape(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	defer contentResponse.Body.Close()
	var content transcript.ContentChunk
	if err := json.NewDecoder(contentResponse.Body).Decode(&content); err != nil || len(content.Data) != 64<<10 {
		t.Fatalf("content bytes=%d err=%v", len(content.Data), err)
	}
	wrong, err := http.Get(server.URL + "/transcript/snapshot?session=" + url.QueryEscape(filepath.Join(dir, "different.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusConflict {
		t.Fatalf("wrong-session status=%d", wrong.StatusCode)
	}
	malformed, err := http.Get(server.URL + "/transcript/page?request=%7B")
	if err != nil {
		t.Fatal(err)
	}
	malformed.Body.Close()
	if malformed.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed status=%d", malformed.StatusCode)
	}
}

// outlineLessController embeds the interface, not the concrete controller, so
// its method set is exactly SessionAPI and the optional outline capability is
// genuinely absent.
type outlineLessController struct{ control.SessionAPI }

func TestTranscriptOutlineHTTPPaginatesAndAdvertisesCapability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	session := agent.NewSession("system")
	for i := range 4 {
		session.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("question %d", i)})
		session.Add(provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("answer %d", i)})
	}
	if err := session.Save(path); err != nil {
		t.Fatal(err)
	}
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Executor: agent.New(nil, nil, session, agent.Options{}, bc), SessionDir: dir, SessionPath: path, Sink: bc})
	defer ctrl.Close()
	srv := New(ctrl, bc, config.ServeConfig{})
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	if !slices.Contains(srv.capabilities(), servecontract.TranscriptOutlineV1) {
		t.Fatalf("serve does not advertise the outline capability: %v", srv.capabilities())
	}

	read := func(request transcript.OutlineRequest) transcript.OutlinePage {
		t.Helper()
		encoded, _ := json.Marshal(request)
		response, err := http.Get(server.URL + "/transcript/outline?session=" + url.QueryEscape(path) + "&request=" + url.QueryEscape(string(encoded)))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("outline status=%d cache=%q", response.StatusCode, response.Header.Get("Cache-Control"))
		}
		var page transcript.OutlinePage
		if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
		return page
	}

	first := read(transcript.OutlineRequest{Entries: 3})
	if first.ProtocolVersion != transcript.ProtocolVersion || first.Total != 4 || len(first.Entries) != 3 || first.Done {
		t.Fatalf("first outline page = %+v", first)
	}
	if first.Entries[0].Prompt != "question 0" || first.Entries[0].Answer != "answer 0" || first.Entries[0].Turn != 1 {
		t.Fatalf("first entry = %+v", first.Entries[0])
	}
	second := read(transcript.OutlineRequest{SnapshotID: first.SnapshotID, Offset: first.NextOffset, Entries: 3})
	if second.SnapshotID != first.SnapshotID || len(second.Entries) != 1 || !second.Done || second.Entries[0].Turn != 4 {
		t.Fatalf("second outline page = %+v", second)
	}

	// An evicted or unknown cut reports staleness instead of repositioning.
	if stale := read(transcript.OutlineRequest{SnapshotID: "evicted"}); !stale.Stale {
		t.Fatalf("unknown cut was answered as current: %+v", stale)
	}

	// Session binding failures stay conflicts, not empty outlines.
	wrong, err := http.Get(server.URL + "/transcript/outline?session=" + url.QueryEscape(filepath.Join(dir, "different.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusConflict {
		t.Fatalf("wrong-session status=%d", wrong.StatusCode)
	}

	// A controller without the optional capability declines the route so a
	// client can fall back to its loaded-turn rail.
	plain := httptest.NewServer(New(outlineLessController{ctrl}, bc, config.ServeConfig{}).Handler())
	defer plain.Close()
	unsupported, err := http.Get(plain.URL + "/transcript/outline?session=" + url.QueryEscape(path))
	if err != nil {
		t.Fatal(err)
	}
	unsupported.Body.Close()
	if unsupported.StatusCode != http.StatusNotImplemented {
		t.Fatalf("unsupported status=%d", unsupported.StatusCode)
	}
}

func TestCanonicalSessionHistoryHTTPUsesAuthorizedContentRanges(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions-v4")
	service, err := canonical.NewService("serve", canonical.NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), canonical.CreateOptions{SessionID: "canonical"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"message": provider.Message{ID: "large", Role: provider.RoleUser, Content: strings.Repeat("range", 20_000)}})
	if _, err := runtime.Session().AppendBatch(t.Context(), "message", []canonical.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{SessionService: service, SessionRuntime: runtime, ExclusiveSession: true, Sink: bc})
	defer ctrl.Close()
	server := httptest.NewServer(New(ctrl, bc, config.ServeConfig{}).Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/session-history/page?sessionId=canonical&limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var page canonical.MessageHistoryPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil || response.StatusCode != http.StatusOK || len(page.Messages) != 1 || page.Messages[0].ContentRef == nil {
		t.Fatalf("history status=%d page=%+v err=%v", response.StatusCode, page, err)
	}
	searchResponse, err := http.Get(server.URL + "/session-history/search?sessionId=canonical&q=range&limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer searchResponse.Body.Close()
	var search canonical.SearchHistoryPage
	if err := json.NewDecoder(searchResponse.Body).Decode(&search); err != nil || searchResponse.StatusCode != http.StatusOK || len(search.Hits) != 1 || search.Hits[0].MessageID != "large" {
		t.Fatalf("search status=%d page=%+v err=%v", searchResponse.StatusCode, search, err)
	}
	request, _ := json.Marshal(sessionHistoryContentRequest{Ref: *page.Messages[0].ContentRef, Offset: 0, Length: 32})
	contentResponse, err := http.Get(server.URL + "/session-history/content?sessionId=canonical&request=" + url.QueryEscape(string(request)))
	if err != nil {
		t.Fatal(err)
	}
	defer contentResponse.Body.Close()
	var content sessionHistoryContentResponse
	if err := json.NewDecoder(contentResponse.Body).Decode(&content); err != nil || contentResponse.StatusCode != http.StatusOK || content.Data == "" || content.NextOffset != 32 {
		t.Fatalf("content status=%d response=%+v err=%v", contentResponse.StatusCode, content, err)
	}
	wrong, err := http.Get(server.URL + "/session-history/page?sessionId=other")
	if err != nil {
		t.Fatal(err)
	}
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusConflict {
		t.Fatalf("wrong session status=%d", wrong.StatusCode)
	}
}
