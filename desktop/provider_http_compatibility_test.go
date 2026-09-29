package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reasonix/internal/config"
	"strings"
	"testing"
)

func TestHTTP1ConfigSurvivesDesktopSaveAndAppliesToProbes(t *testing.T) {
	isolateDesktopUserDirs(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Errorf("desktop request used %s", r.Proto)
		}
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = io.WriteString(w, `{"data":[{"id":"fixture"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	original := http.DefaultTransport
	base := original.(*http.Transport).Clone()
	base.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = base
	defer func() { http.DefaultTransport = original; base.CloseIdleConnections() }()
	app := NewApp()
	view := ProviderView{Name: "compat", Kind: "openai", BaseURL: server.URL, Models: []string{"fixture"}, APIKeyEnv: "HTTP1_FIXTURE_KEY"}
	if _, err := app.SaveProviderWithKey(view, "fixture-secret"); err != nil {
		t.Fatal(err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	baseline := cfg.ModelSettingsBaseline()
	entry, _ := cfg.Provider(view.Name)
	before := providerModelCatalogFingerprint(*entry)
	entry.HTTP1Only = true
	if before == providerModelCatalogFingerprint(*entry) {
		t.Fatal("catalog identity ignored protocol policy")
	}
	if err := cfg.SaveModelSettingsTo(config.UserConfigPath(), baseline); err != nil {
		t.Fatal(err)
	}
	// Existing desktop RPC has no protocol field; unrelated edits retain it.
	view.ContextWindow = 32768
	if err := app.SaveProvider(view); err != nil {
		t.Fatal(err)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	entry, _ = cfg.Provider(view.Name)
	if !entry.HTTP1Only || entry.ContextWindow != 32768 {
		t.Fatal("desktop edit lost config-only policy or the requested change")
	}
	if err := app.TestProviderModel(view, "fixture", "fixture-secret"); err != nil {
		t.Fatal(err)
	}
	models, err := app.FetchProviderModelCatalogDraft(view, "fixture-secret")
	if err != nil || len(models) != 1 {
		t.Fatalf("draft discovery = %v, %v", models, err)
	}
	if models := app.FetchAllProviderModels([]ProviderView{view}); len(models[view.Name]) != 1 {
		t.Fatalf("batch discovery = %v", models)
	}
}

func TestHTTP1CompatibilityCredentialProxyUsesSourcePolicy(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Errorf("credential proxy used %s", r.Proto)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("credential changed")
		}
		_, _ = io.WriteString(w, "OK")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	original := http.DefaultTransport
	base := original.(*http.Transport).Clone()
	base.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = base
	defer func() { http.DefaultTransport = original; base.CloseIdleConnections() }()
	entry := config.ProviderEntry{Name: "fixture", Kind: "openai", BaseURL: server.URL, Model: "m", HTTP1Only: true}.WithAPIKeyForProbe("fixture-secret")
	cfg := &config.Config{Providers: []config.ProviderEntry{entry}, Network: config.NetworkConfig{ProxyMode: "off"}}
	up, err := resolveProxyProvider(cfg, "fixture/m")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &credentialProxy{routes: map[string]*credProxyRoute{}}
	proxy.setRouteLocked("token", "fixture/m", up)
	defer proxy.close()
	req := httptest.NewRequest(http.MethodPost, "http://localhost/chat/completions", strings.NewReader(`{"model":"m"}`))
	req.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "OK" {
		t.Fatalf("proxy response = %d %s", w.Code, w.Body.String())
	}
}
