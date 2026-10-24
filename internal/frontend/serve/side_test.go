package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/config"
	"reasonix/internal/contract/event"
	"reasonix/internal/session/control"
)

type echoSide struct{ prompts []string }

func (e *echoSide) Ask(_ context.Context, prompt string, _ event.Sink) (string, error) {
	e.prompts = append(e.prompts, prompt)
	return "echo: " + prompt, nil
}

func sideServer(t *testing.T, factory control.SideFactory) *httptest.Server {
	t.Helper()
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, Label: "side", ModelRef: config.UnconfiguredRef, SessionDir: testenv.TempDir(t), SideFactory: factory})
	t.Cleanup(ctrl.Close)
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	t.Cleanup(srv.Close)
	return srv
}

func TestSideRoutesOpenAskAndClose(t *testing.T) {
	conv := &echoSide{}
	srv := sideServer(t, func() (control.SideConversation, error) { return conv, nil })

	status, raw := postSide(t, srv.URL+"/side", map[string]any{})
	var opened struct {
		ID string `json:"id"`
	}
	if status != http.StatusOK || json.Unmarshal(raw, &opened) != nil || opened.ID == "" {
		t.Fatalf("open = %d %s", status, raw)
	}
	status, raw = postSide(t, srv.URL+"/side/"+opened.ID+"/ask", map[string]any{"question": "why?", "quotes": []string{"the passage"}})
	if status != http.StatusOK || !strings.Contains(string(raw), "echo: Quoted from the main conversation (passage 1):") || !strings.Contains(string(raw), "why?") {
		t.Fatalf("ask = %d %s", status, raw)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/side/"+opened.ID, nil)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusNoContent {
		t.Fatalf("close = %v %v", res, err)
	}
	status, raw = postSide(t, srv.URL+"/side/"+opened.ID+"/ask", map[string]any{"question": "again"})
	if status != http.StatusNotFound || !strings.Contains(string(raw), codeSideUnknown) {
		t.Fatalf("a closed one = %d %s", status, raw)
	}
}

func TestSideRoutesRefuseWithTheirOwnCodes(t *testing.T) {
	none := sideServer(t, nil)
	if status, raw := postSide(t, none.URL+"/side", map[string]any{}); status != http.StatusNotFound || !strings.Contains(string(raw), codeSideUnavailable) {
		t.Fatalf("no runner = %d %s", status, raw)
	}
	t.Setenv("REASONIX_HOME", testenv.TempDir(t))
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Agent.MaxSideSessions = 3
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	srv := sideServer(t, func() (control.SideConversation, error) { return &echoSide{}, nil })
	_, raw := postSide(t, srv.URL+"/side", map[string]any{})
	var opened struct{ ID string }
	_ = json.Unmarshal(raw, &opened)
	if status, raw := postSide(t, srv.URL+"/side/"+opened.ID+"/ask", map[string]any{"question": "  "}); status != http.StatusBadRequest || !strings.Contains(string(raw), codeSideEmpty) {
		t.Fatalf("empty = %d %s", status, raw)
	}
	for range 2 {
		postSide(t, srv.URL+"/side", map[string]any{})
	}
	if status, raw := postSide(t, srv.URL+"/side", map[string]any{}); status != http.StatusConflict || !strings.Contains(string(raw), codeSideLimit) {
		t.Fatalf("limit = %d %s", status, raw)
	}
}

func postSide(t *testing.T, url string, body any) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(body)
	res, err := http.Post(url, "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return res.StatusCode, []byte(out.String())
}

type streamingSideFunc func(context.Context, string, event.Sink) (string, error)

func (f streamingSideFunc) Ask(ctx context.Context, prompt string, sink event.Sink) (string, error) {
	return f(ctx, prompt, sink)
}

func openTestSide(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	status, raw := postSide(t, srv.URL+"/side", map[string]any{})
	var opened struct{ ID string }
	if status != http.StatusOK || json.Unmarshal(raw, &opened) != nil || opened.ID == "" {
		t.Fatalf("open = %d %s", status, raw)
	}
	return opened.ID
}

func streamSide(t *testing.T, srv *httptest.Server, id, body string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/side/"+id+"/ask", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json;q=0.5, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream = %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	return res
}

func readSideFrame(t *testing.T, reader *bufio.Reader) (string, []byte) {
	t.Helper()
	var name string
	var data []byte
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading side frame: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return name, data
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			name = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			data = []byte(value)
		}
	}
}

func TestSideStreamFlushesPrivateProgressBeforeCompletion(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	conv := streamingSideFunc(func(ctx context.Context, _ string, sink event.Sink) (string, error) {
		sink.Emit(event.Event{Kind: event.Reasoning, Text: "SECRET REASONING"})
		sink.Emit(event.Event{Kind: event.Reasoning, Text: "MORE SECRET REASONING"})
		sink.Emit(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{Name: "read_file", Args: "SECRET ARGUMENT"}})
		sink.Emit(event.Event{Kind: event.ToolResult, Tool: event.Tool{Output: "SECRET RESULT"}})
		sink.Emit(event.Event{Kind: event.Text, Text: "first delta"})
		sink.Emit(event.Event{Kind: event.Usage})
		select {
		case <-release:
			return "authoritative final answer", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	srv := sideServer(t, func() (control.SideConversation, error) { return conv, nil })
	id := openTestSide(t, srv)
	res := streamSide(t, srv, id, `{"question":"why?"}`)
	reader := bufio.NewReader(res.Body)
	for _, want := range []SideProgress{
		{Kind: "waiting"}, {Kind: "thinking"}, {Kind: "tool", Tool: "read_file"},
		{Kind: "thinking"}, {Kind: "text", Text: "first delta"},
	} {
		name, data := readSideFrame(t, reader)
		var got SideProgress
		if name != "progress" || json.Unmarshal(data, &got) != nil || got != want || strings.Contains(string(data), "SECRET") {
			t.Fatalf("progress = %q %s, want %+v", name, data, want)
		}
	}
	// No release yet: the real network already delivered the first text.
	release <- struct{}{}
	name, data := readSideFrame(t, reader)
	var done struct{ Answer string }
	if name != "done" || json.Unmarshal(data, &done) != nil || done.Answer != "authoritative final answer" {
		t.Fatalf("done = %q %s", name, data)
	}
	if rest, err := io.ReadAll(reader); err != nil || len(rest) != 0 {
		t.Fatalf("stream did not end exactly once: %q %v", rest, err)
	}
}

func TestSideStreamErrorsKeepRefusalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
		err              error
	}{
		{"empty", `{"question":" "}`, codeSideEmpty, http.StatusBadRequest, nil},
		{"bad body", `{`, "request.bad_body", http.StatusBadRequest, nil},
		{"failed after text", `{"question":"q"}`, codeSideFailed, http.StatusBadGateway, errors.New("provider failed")},
		{"coded", `{"question":"q"}`, "provider.unavailable", http.StatusServiceUnavailable, refusal(http.StatusServiceUnavailable, "provider.unavailable", errors.New("offline"), map[string]any{"model": "test"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv := streamingSideFunc(func(_ context.Context, _ string, sink event.Sink) (string, error) {
				sink.Emit(event.Event{Kind: event.Text, Text: "partial"})
				return "", tc.err
			})
			srv := sideServer(t, func() (control.SideConversation, error) { return conv, nil })
			res := streamSide(t, srv, openTestSide(t, srv), tc.body)
			reader := bufio.NewReader(res.Body)
			name, data := readSideFrame(t, reader)
			if name != "progress" || string(data) != `{"kind":"waiting"}` {
				t.Fatalf("initial frame = %q %s", name, data)
			}
			if tc.err != nil {
				name, data = readSideFrame(t, reader)
				if name != "progress" || !strings.Contains(string(data), "partial") {
					t.Fatalf("partial = %q %s", name, data)
				}
			}
			name, data = readSideFrame(t, reader)
			var reason struct {
				Reason
				Status int `json:"status"`
			}
			if name != "error" || json.Unmarshal(data, &reason) != nil || reason.Code != tc.code || reason.Status != tc.status {
				t.Fatalf("error = %q %s", name, data)
			}
			if rest, err := io.ReadAll(reader); err != nil || len(rest) != 0 {
				t.Fatalf("error stream did not end: %q %v", rest, err)
			}
		})
	}
}

func TestSideStreamDisconnectCancelsAndFreesQuestion(t *testing.T) {
	stopped := make(chan struct{}, 1)
	conv := streamingSideFunc(func(ctx context.Context, _ string, sink event.Sink) (string, error) {
		sink.Emit(event.Event{Kind: event.Text, Text: "started"})
		<-ctx.Done()
		stopped <- struct{}{}
		return "", ctx.Err()
	})
	srv := sideServer(t, func() (control.SideConversation, error) { return conv, nil })
	id := openTestSide(t, srv)
	res := streamSide(t, srv, id, `{"question":"q"}`)
	reader := bufio.NewReader(res.Body)
	readSideFrame(t, reader)
	readSideFrame(t, reader)
	res.Body.Close()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnect did not cancel side run")
	}
	// Closing remains responsive after cancellation, and releases the entry.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/side/"+id, nil)
	closed, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	closed.Body.Close()
	if closed.StatusCode != http.StatusNoContent {
		t.Fatalf("close = %d", closed.StatusCode)
	}
}

func TestSideStreamConcurrentCallbacksRemainWholeFrames(t *testing.T) {
	conv := streamingSideFunc(func(_ context.Context, _ string, sink event.Sink) (string, error) {
		var wg sync.WaitGroup
		for i := range 32 {
			wg.Go(func() {
				sink.Emit(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{Name: "read_file_" + strconv.Itoa(i)}})
			})
		}
		wg.Wait()
		return "done", nil
	})
	srv := sideServer(t, func() (control.SideConversation, error) { return conv, nil })
	res := streamSide(t, srv, openTestSide(t, srv), `{"question":"q"}`)
	reader := bufio.NewReader(res.Body)
	readSideFrame(t, reader)
	seen := make(map[string]bool)
	for range 32 {
		name, data := readSideFrame(t, reader)
		var progress SideProgress
		if name != "progress" || json.Unmarshal(data, &progress) != nil || progress.Kind != "tool" || !strings.HasPrefix(progress.Tool, "read_file_") || seen[progress.Tool] {
			t.Fatalf("concurrent progress = %q %s", name, data)
		}
		seen[progress.Tool] = true
	}
	name, data := readSideFrame(t, reader)
	if name != "done" || string(data) != `{"answer":"done"}` {
		t.Fatalf("concurrent done = %q %s", name, data)
	}
}

func TestSideStreamUnknownAndJSONNegotiation(t *testing.T) {
	srv := sideServer(t, func() (control.SideConversation, error) { return &echoSide{}, nil })
	res := streamSide(t, srv, "missing", `{"question":"q"}`)
	reader := bufio.NewReader(res.Body)
	readSideFrame(t, reader)
	name, data := readSideFrame(t, reader)
	var reason Reason
	if name != "error" || json.Unmarshal(data, &reason) != nil || reason.Code != codeSideUnknown {
		t.Fatalf("unknown side = %q %s", name, data)
	}
	id := openTestSide(t, srv)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/side/"+id+"/ask", strings.NewReader(`{"question":"q"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream;q=0, application/json")
	jsonRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer jsonRes.Body.Close()
	var answer struct{ Answer string }
	if jsonRes.StatusCode != http.StatusOK || !strings.HasPrefix(jsonRes.Header.Get("Content-Type"), "application/json") || json.NewDecoder(jsonRes.Body).Decode(&answer) != nil || answer.Answer != "echo: q" {
		t.Fatalf("JSON negotiation = %d %+v", jsonRes.StatusCode, answer)
	}
}

type failedSideWriter struct {
	header http.Header
	writes int
}

func (w *failedSideWriter) Header() http.Header { return w.header }
func (w *failedSideWriter) WriteHeader(int)     {}
func (w *failedSideWriter) Flush()              {}
func (w *failedSideWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("disconnected")
}

func TestSideStreamWriteFailureCancelsAndStopsWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &failedSideWriter{header: make(http.Header)}
	stream := &sideStream{w: writer, cancel: cancel}
	stream.emit(event.Event{Kind: event.Text, Text: "delta"})
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("failed writer did not cancel the question")
	}
	stream.emit(event.Event{Kind: event.Text, Text: "late delta"})
	stream.write("done", map[string]string{"answer": "late answer"}, true)
	if writer.writes != 1 {
		t.Fatalf("closed writer received %d writes", writer.writes)
	}
}

func TestSideFailureSanitizesBothTransports(t *testing.T) {
	const secret = "sk-1234567890abcdefghijklmnopqrstuvwxyz"
	conv := streamingSideFunc(func(context.Context, string, event.Sink) (string, error) {
		return "", errors.New("Authorization: Bearer " + secret)
	})
	srv := sideServer(t, func() (control.SideConversation, error) { return conv, nil })
	id := openTestSide(t, srv)
	status, body := postSide(t, srv.URL+"/side/"+id+"/ask", map[string]string{"question": "q"})
	if status != http.StatusBadGateway || strings.Contains(string(body), secret) || !strings.Contains(string(body), codeSideFailed) {
		t.Fatalf("JSON failure was not sanitized: %d %s", status, body)
	}
	res := streamSide(t, srv, id, `{"question":"q"}`)
	body, err := io.ReadAll(res.Body)
	if err != nil || strings.Contains(string(body), secret) || !strings.Contains(string(body), codeSideFailed) {
		t.Fatalf("SSE failure was not sanitized: %s %v", body, err)
	}
}
