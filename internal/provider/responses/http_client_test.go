package responses

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"reasonix/internal/netclient"
	"reasonix/internal/provider"
)

func TestInvalidProxyNeverFallsBackToDefaultTransport(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeEvents(w, `{"type":"response.completed","response":{"id":"r","status":"completed"}}`)
	}))
	defer server.Close()
	proxy := netclient.ProxySpec{Mode: netclient.ModeCustom, URL: "unsupported://proxy.invalid:8080"}
	for _, only := range []bool{false, true} {
		t.Run(fmt.Sprintf("http1=%t", only), func(t *testing.T) {
			for _, kind := range []string{"responses", "dashscope-responses"} {
				p, err := provider.New(kind, provider.Config{BaseURL: server.URL, Model: "m", HTTP1Only: only, Extra: map[string]any{"proxy_spec": proxy}})
				if err == nil || p != nil {
					t.Errorf("%s factory accepted invalid proxy", kind)
				}
			}
			p := New(Config{BaseURL: server.URL, Model: "m", HTTP1Only: only, Proxy: proxy})
			chunks, err := p.Stream(t.Context(), provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hello"}}})
			if chunks != nil {
				for range chunks {
				}
			}
			if err == nil {
				t.Error("direct constructor silently ignored invalid proxy")
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid proxy leaked %d direct requests", requests.Load())
	}
}

func TestInjectedHTTPClientOwnsTransportPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEvents(w, `{"type":"response.completed","response":{"id":"r","status":"completed"}}`)
	}))
	defer server.Close()
	proxy := netclient.ProxySpec{Mode: netclient.ModeCustom, URL: "unsupported://proxy.invalid:8080"}
	p, err := newFromConfig(provider.Config{BaseURL: server.URL, Model: "m", HTTPClient: server.Client(), HTTP1Only: true, Extra: map[string]any{"proxy_spec": proxy}})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, p, provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hello"}}})
}
