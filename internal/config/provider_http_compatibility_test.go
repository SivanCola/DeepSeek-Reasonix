package config

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"reasonix/internal/netclient"
)

func TestProviderHTTP1CompatibilityPersistence(t *testing.T) {
	c := &Config{Providers: []ProviderEntry{
		{Name: "one", Kind: "openai", BaseURL: "https://one.invalid", Model: "m"},
		{Name: "two", Kind: "responses", BaseURL: "https://two.invalid", Model: "m"},
	}}
	before := c.ModelRuntimeFingerprint("one/m")
	path := filepath.Join(t.TempDir(), "config.toml")
	baseline := c.ModelSettingsBaseline()
	if err := os.WriteFile(path, []byte(baseline), 0600); err != nil {
		t.Fatal(err)
	}
	c.Providers[0].HTTP1Only = true
	if c.ModelRuntimeFingerprint("one/m") == before {
		t.Fatal("runtime ignores protocol changes")
	}
	if err := c.SaveModelSettingsTo(path, baseline); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadForEdit(path)
	one, ok := reloaded.Provider("one")
	if !ok || !one.HTTP1Only {
		t.Fatal("compatibility lost after reload")
	}
	two, ok := reloaded.Provider("two")
	if !ok || two.HTTP1Only {
		t.Fatal("changed sibling connection")
	}
	for _, render := range []func(*Config) string{RenderTOML, RenderTOMLProjectDelta} {
		var decoded Config
		if _, err := toml.Decode(render(c), &decoded); err != nil {
			t.Fatal(err)
		}
		entry, ok := decoded.Provider("one")
		if !ok || !entry.HTTP1Only {
			t.Fatal("render dropped compatibility")
		}
	}
	baseline = reloaded.ModelSettingsBaseline()
	one.HTTP1Only = false
	if err := reloaded.SaveModelSettingsTo(path, baseline); err != nil {
		t.Fatal(err)
	}
	again := LoadForEdit(path)
	one, _ = again.Provider("one")
	if one.HTTP1Only {
		t.Fatal("automatic mode did not persist")
	}
}

func TestModelDiscoveryUsesHTTP1Compatibility(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Errorf("discovery used %s", r.Proto)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"fixture"}]}`)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	original := http.DefaultTransport
	base := original.(*http.Transport).Clone()
	base.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = base
	defer func() { http.DefaultTransport = original; base.CloseIdleConnections() }()
	entry := ProviderEntry{Name: "fixture", Kind: "openai", BaseURL: server.URL, HTTP1Only: true}
	entry = entry.WithAPIKeyForProbe("fixture-secret")
	models, err := entry.FetchModelsWithProxy(t.Context(), netclient.ProxySpec{Mode: netclient.ModeOff})
	if err != nil || len(models) != 1 || models[0] != "fixture" {
		t.Fatalf("models = %v, %v", models, err)
	}
}
