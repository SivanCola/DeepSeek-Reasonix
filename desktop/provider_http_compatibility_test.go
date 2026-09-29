package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reasonix/internal/config"
)

func TestHTTP1CompatibilityRebuildKeepsSessionHistory(t *testing.T) {
	isolateDesktopUserDirs(t)
	ref, _ := configureSwitchableDefaultModels(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := modelSettingsBootTab(t, app, "compatibility", t.TempDir(), ref)
	app.activeTabID = tab.ID
	old := tab.Ctrl
	enabled := true
	name, _, _ := strings.Cut(ref, "/")
	result := app.ApplyModelSettings(ModelSettingsChange{Kind: "http1_compatibility", Name: name, Enabled: &enabled, RequestID: "runtime-http1", ExpectedFingerprint: app.Settings().ModelSettingsFingerprint})
	if !result.Persisted {
		t.Fatalf("save = %+v", result)
	}
	result = app.RetryModelSettingsApplication(tab.ID)
	if tab.Ctrl == old {
		t.Fatalf("runtime not rebuilt: %+v", result)
	}
	history := tab.Ctrl.History()
	if len(history) < 2 || history[1].Content != "keep history compatibility" {
		t.Fatal("session history lost")
	}
	if tab.model != ref {
		t.Fatal("compatibility changed model selection")
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

func TestHTTP1CompatibilitySettingsAreNarrowAndDoNotStartRequests(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	for _, name := range []string{"one", "two"} {
		if err := app.SaveProvider(ProviderView{Name: name, Kind: "openai", BaseURL: "https://example.invalid", Models: []string{"m"}}); err != nil {
			t.Fatal(err)
		}
	}
	enabled := true
	change := ModelSettingsChange{Kind: "http1_compatibility", Name: "one", Enabled: &enabled, RequestID: "http1", ExpectedFingerprint: app.Settings().ModelSettingsFingerprint}
	result := app.ApplyModelSettings(change)
	if !result.Persisted || result.Application != "not_required" {
		t.Fatalf("save = %+v", result)
	}
	if len(app.tabs) != 0 || len(app.detachedSessions) != 0 {
		t.Fatal("compatibility created a session")
	}
	c := config.LoadForEdit(config.UserConfigPath())
	one, _ := c.Provider("one")
	two, _ := c.Provider("two")
	if !one.HTTP1Only || two.HTTP1Only {
		t.Fatal("connection policy not isolated")
	}
	if replay := app.ApplyModelSettings(change); !replay.Persisted || replay.Revision != result.Revision {
		t.Fatal("receipt replay changed the result")
	}
	change.RequestID = "stale"
	if stale := app.ApplyModelSettings(change); stale.Persisted {
		t.Fatal("stale change accepted")
	}
	change.RequestID = "missing"
	change.Name = "deleted"
	change.ExpectedFingerprint = app.Settings().ModelSettingsFingerprint
	if missing := app.ApplyModelSettings(change); missing.Persisted {
		t.Fatal("unknown provider accepted")
	}
	change.Name = "one"
	change.RequestID = "automatic"
	enabled = false
	if reset := app.ApplyModelSettings(change); !reset.Persisted {
		t.Fatalf("reset = %+v", reset)
	}
	c = config.LoadForEdit(config.UserConfigPath())
	one, _ = c.Provider("one")
	if one.HTTP1Only {
		t.Fatal("automatic mode not restored")
	}
}

func TestSaveProviderPreservesHTTP1Compatibility(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	view := ProviderView{Name: "compat", Kind: "responses", BaseURL: "https://example.invalid", Models: []string{"m"}, HTTP1Only: true}
	if err := app.SaveProvider(view); err != nil {
		t.Fatal(err)
	}
	for _, p := range app.Settings().Providers {
		if p.Name == view.Name {
			if !p.HTTP1Only {
				t.Fatal("settings view lost policy")
			}
			return
		}
	}
	t.Fatal("provider missing")
}
