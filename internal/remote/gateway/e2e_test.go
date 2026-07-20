package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/provider"
	"reasonix/internal/remote/broker"
	"reasonix/internal/remote/gateway"
	"reasonix/internal/remote/protocol"
	"reasonix/internal/remote/remoteruntime"
)

func TestGatewayAppBridgeE2E_HelloCapabilitiesAndWorkspace(t *testing.T) {
	// 1. Local Provider Broker (keys stay here).
	resolver := &provider.StaticResolver{
		Descriptors: []provider.Descriptor{{Ref: "deepseek/chat", DisplayName: "DeepSeek", Model: "chat"}},
		Providers:   map[string]provider.Provider{"deepseek/chat": stubProv{name: "deepseek"}},
	}
	brk := broker.NewServer(broker.Options{Resolver: resolver})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	baddr, err := brk.ListenAndServe(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := brk.Issue(broker.Scope{
		HostID: "lab", Fingerprint: "fp", Workspace: t.TempDir(),
		AllowedRefs: map[string]struct{}{"deepseek/chat": {}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. remote-runtime with broker-backed catalog (controller create not needed for hello).
	ws := t.TempDir()
	rtToken := "runtime-secret-token"
	rt := remoteruntime.New(remoteruntime.Options{
		Workspace: ws,
		Version:   "e2e-test",
		Token:     rtToken,
		Resolver:  &broker.Client{BaseURL: "http://" + baddr.String(), Token: tok},
		BuildController: func(ctx context.Context, model, resume string) (*control.Controller, error) {
			return nil, errString("not used in this e2e")
		},
	})
	defer rt.Close()
	rtAddr, err := rt.ListenAndServe(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	// 3. Gateway → remote-runtime + FS/Git backend.
	gw := gateway.New()
	fs := &memFS{files: map[string]string{"/w/README.md": "# hi\n"}}
	gw.SetWorkspaceBackend(fs)
	gaddr, err := gw.ListenAndServe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gw.RegisterSession(gateway.Session{
		ID: "gws_e2e", HostID: "lab", Workspace: "/w",
		RemoteBase: "http://" + rtAddr.String(), RemoteToken: rtToken,
		Fingerprint: "fp", BrokerStatus: "ready",
	}); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// Protocol hello through catch-all proxy.
	req, _ := http.NewRequest(http.MethodGet, "http://"+gaddr.String()+"/gateway/v1/remote/hello", nil)
	req.Header.Set("X-Reasonix-Gateway-Token", gw.Token())
	req.Header.Set("X-Reasonix-Session-Id", "gws_e2e")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("hello status %d: %s", resp.StatusCode, body)
	}
	var hello protocol.HelloResponse
	if err := json.NewDecoder(resp.Body).Decode(&hello); err != nil {
		t.Fatal(err)
	}
	if err := hello.Compatible(); err != nil {
		t.Fatal(err)
	}

	// No token → 401.
	req2, _ := http.NewRequest(http.MethodGet, "http://"+gaddr.String()+"/gateway/v1/remote/hello", nil)
	req2.Header.Set("X-Reasonix-Session-Id", "gws_e2e")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}

	// Capabilities.
	req3, _ := http.NewRequest(http.MethodGet, "http://"+gaddr.String()+"/gateway/v1/remote/capabilities", nil)
	req3.Header.Set("X-Reasonix-Gateway-Token", gw.Token())
	req3.Header.Set("X-Reasonix-Session-Id", "gws_e2e")
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("capabilities %d", resp3.StatusCode)
	}

	// FS list/read + git status (AppBridge workspace surface).
	req4, _ := http.NewRequest(http.MethodGet, "http://"+gaddr.String()+"/gateway/v1/fs/list?path=/w", nil)
	req4.Header.Set("X-Reasonix-Gateway-Token", gw.Token())
	req4.Header.Set("X-Reasonix-Session-Id", "gws_e2e")
	resp4, err := client.Do(req4)
	if err != nil {
		t.Fatal(err)
	}
	defer resp4.Body.Close()
	var listed struct {
		Entries []gateway.DirEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp4.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Entries) != 1 || listed.Entries[0].Name != "README.md" {
		t.Fatalf("entries = %+v", listed.Entries)
	}

	req5, _ := http.NewRequest(http.MethodGet, "http://"+gaddr.String()+"/gateway/v1/fs/read?path=/w/README.md", nil)
	req5.Header.Set("X-Reasonix-Gateway-Token", gw.Token())
	req5.Header.Set("X-Reasonix-Session-Id", "gws_e2e")
	resp5, err := client.Do(req5)
	if err != nil {
		t.Fatal(err)
	}
	defer resp5.Body.Close()
	var prev gateway.FilePreview
	if err := json.NewDecoder(resp5.Body).Decode(&prev); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prev.Body, "# hi") {
		t.Fatalf("body = %q", prev.Body)
	}

	req6, _ := http.NewRequest(http.MethodGet, "http://"+gaddr.String()+"/gateway/v1/git/status", nil)
	req6.Header.Set("X-Reasonix-Gateway-Token", gw.Token())
	req6.Header.Set("X-Reasonix-Session-Id", "gws_e2e")
	resp6, err := client.Do(req6)
	if err != nil {
		t.Fatal(err)
	}
	resp6.Body.Close()
	if resp6.StatusCode != 200 {
		t.Fatalf("git status %d", resp6.StatusCode)
	}
}

func TestGatewayProxiesNestedSessionControlPaths(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/remote/v1/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"path": r.URL.Path, "method": r.Method})
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gw := gateway.New()
	gaddr, err := gw.ListenAndServe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = gw.RegisterSession(gateway.Session{
		ID: "gws_path", HostID: "h", Workspace: "/w",
		RemoteBase: "http://" + ln.Addr().String(),
	})

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPost, "/gateway/v1/remote/sessions/rs_1/approve"},
		{http.MethodPost, "/gateway/v1/remote/sessions/rs_1/cancel"},
		{http.MethodPost, "/gateway/v1/remote/sessions/rs_1/model"},
		{http.MethodPost, "/gateway/v1/remote/sessions/rs_1/compact"},
		{http.MethodGet, "/gateway/v1/remote/sessions/rs_1/checkpoint"},
	} {
		req, _ := http.NewRequest(tc.method, "http://"+gaddr.String()+tc.path, bytes.NewBufferString(`{}`))
		req.Header.Set("X-Reasonix-Gateway-Token", gw.Token())
		req.Header.Set("X-Reasonix-Session-Id", "gws_path")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			resp.Body.Close()
			t.Fatal(err)
		}
		resp.Body.Close()
		wantPath := "/remote/v1" + strings.TrimPrefix(tc.path, "/gateway/v1/remote")
		if out["path"] != wantPath {
			t.Fatalf("%s: got path %q want %q", tc.path, out["path"], wantPath)
		}
		if out["method"] != tc.method {
			t.Fatalf("%s: method %q", tc.path, out["method"])
		}
	}
}

// --- helpers ---

type errString string

func (e errString) Error() string { return string(e) }

type stubProv struct{ name string }

func (s stubProv) Name() string { return s.name }
func (s stubProv) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk)
	close(ch)
	return ch, nil
}

type memFS struct {
	files map[string]string
}

func (m *memFS) ListDir(_ context.Context, _, path string) ([]gateway.DirEntry, error) {
	prefix := strings.TrimRight(path, "/") + "/"
	var out []gateway.DirEntry
	for p, body := range m.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		name := strings.TrimPrefix(p, prefix)
		if strings.Contains(name, "/") {
			continue
		}
		out = append(out, gateway.DirEntry{Name: name, Path: p, Size: int64(len(body))})
	}
	return out, nil
}

func (m *memFS) ReadFile(_ context.Context, _, path string) (gateway.FilePreview, error) {
	body, ok := m.files[path]
	if !ok {
		return gateway.FilePreview{Err: "not found"}, nil
	}
	return gateway.FilePreview{Path: path, Body: body, Size: int64(len(body))}, nil
}

func (m *memFS) WriteFile(_ context.Context, _, path, body string, _ int64) (gateway.WriteResult, error) {
	m.files[path] = body
	return gateway.WriteResult{OK: true, NewMtimeUnix: time.Now().Unix()}, nil
}

func (m *memFS) GitStatus(context.Context, string, string) (string, error) {
	return "## main\n M README.md\n", nil
}

func (m *memFS) GitDiff(context.Context, string, string) (string, error) {
	return "diff --git a/README.md b/README.md\n", nil
}
