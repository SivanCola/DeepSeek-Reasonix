package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"reasonix/internal/config"
)

func TestCredentialProxyProtocolKeepsConfiguredNetworkRoute(t *testing.T) {
	var direct, proxied atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	// Use a non-loopback request host: proxy resolvers intentionally bypass
	// localhost even in custom mode. Keep every actual dial on this fixture.
	original := http.DefaultTransport
	base := original.(*http.Transport).Clone()
	base.Proxy = nil
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "fixture.invalid:80" {
			address = upstream.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	http.DefaultTransport = base
	defer func() { http.DefaultTransport = original; base.CloseIdleConnections() }()
	forward := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		if r.URL.Host != "fixture.invalid" {
			t.Errorf("wrong upstream: %s", r.URL)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer forward.Close()
	for _, only := range []bool{false, true} {
		t.Run(fmt.Sprintf("http1=%t", only), func(t *testing.T) {
			entry := config.ProviderEntry{Name: "fixture", Kind: "openai", BaseURL: "http://fixture.invalid", Model: "m", HTTP1Only: only}.WithAPIKeyForProbe("fixture-secret")
			cfg := &config.Config{Providers: []config.ProviderEntry{entry}, Network: config.NetworkConfig{ProxyMode: "custom", ProxyURL: forward.URL}}
			up, err := resolveProxyProvider(cfg, "fixture/m")
			if err != nil {
				t.Fatal(err)
			}
			proxy := &credentialProxy{routes: map[string]*credProxyRoute{}}
			proxy.setRouteLocked("token", "fixture/m", up)
			defer proxy.close()
			req := httptest.NewRequest(http.MethodPost, "http://localhost/chat/completions", nil)
			req.Header.Set("Authorization", "Bearer token")
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, req)
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}
		})
	}
	if direct.Load() != 0 || proxied.Load() != 2 {
		t.Fatalf("protocol switch changed network route: direct=%d proxy=%d", direct.Load(), proxied.Load())
	}
}
