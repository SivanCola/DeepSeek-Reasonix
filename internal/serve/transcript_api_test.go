package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/provider"
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
	openResponse, err := http.Get(server.URL + "/session/open?sessionId=canonical")
	if err != nil {
		t.Fatal(err)
	}
	defer openResponse.Body.Close()
	var openView canonical.SessionOpenView
	if err := json.NewDecoder(openResponse.Body).Decode(&openView); err != nil || openResponse.StatusCode != http.StatusOK || len(openView.Recent.Entries) != 1 {
		t.Fatalf("open status=%d view=%+v err=%v", openResponse.StatusCode, openView, err)
	}
	var page canonical.MessageHistoryPage
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		response, requestErr := http.Get(server.URL + "/session-history/page?sessionId=canonical&limit=10")
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&page)
		_ = response.Body.Close()
		if decodeErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("history status=%d page=%+v err=%v", response.StatusCode, page, decodeErr)
		}
		if page.Status == "ready" {
			break
		}
		if page.Status != "preparing" || time.Now().After(deadline) {
			t.Fatalf("history preparation = %+v", page)
		}
	}
	if len(page.Messages) != 1 || page.Messages[0].ContentRef == nil {
		t.Fatalf("history page=%+v", page)
	}
	locationResponse, err := http.Get(server.URL + "/session-history/locate?sessionId=canonical&messageId=large&snapshot=" + fmt.Sprint(page.SnapshotSequence))
	if err != nil {
		t.Fatal(err)
	}
	defer locationResponse.Body.Close()
	var location canonical.MessageLocation
	if err := json.NewDecoder(locationResponse.Body).Decode(&location); err != nil || locationResponse.StatusCode != http.StatusOK || location.Status != "ready" || location.Cursor == "" {
		t.Fatalf("location status=%d response=%+v err=%v", locationResponse.StatusCode, location, err)
	}
	var search canonical.SearchHistoryPage
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		response, requestErr := http.Get(server.URL + "/session-history/search?sessionId=canonical&q=range&limit=10")
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&search)
		_ = response.Body.Close()
		if decodeErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("search status=%d page=%+v err=%v", response.StatusCode, search, decodeErr)
		}
		if search.Status == "ready" {
			break
		}
		if search.Status != "preparing" || time.Now().After(deadline) {
			t.Fatalf("search preparation = %+v", search)
		}
	}
	if len(search.Hits) != 1 || search.Hits[0].MessageID != "large" {
		t.Fatalf("search page=%+v", search)
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
