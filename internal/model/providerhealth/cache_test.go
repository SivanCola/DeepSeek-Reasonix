package providerhealth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/base/netclient"
	"reasonix/internal/contract/config"
)

func TestExpiredFailureRecovers(t *testing.T) {
	var status atomic.Int32
	status.Store(503)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()
	p := config.ProviderEntry{Kind: "openai", BaseURL: srv.URL}
	var checker Checker
	spec := netclient.ProxySpec{Mode: netclient.ModeOff}
	if got := checker.Read(context.Background(), &p, spec); got.Connection != "unreachable" {
		t.Fatalf("got %+v", got)
	}
	status.Store(200)
	checker.mu.Lock()
	for _, entry := range checker.entries {
		entry.at = time.Now().Add(-2 * freshFor)
	}
	checker.mu.Unlock()
	if got := checker.Read(context.Background(), &p, spec); got.Connection != "reachable" {
		t.Fatalf("did not recover: %+v", got)
	}
}

func TestQuotaUsesStructuredCodeNotErrorWords(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":{"code":"insufficient_quota"}}`, "quota_exhausted"},
		{`{"error":{"type":"insufficient_quota"}}`, "quota_exhausted"},
		{`{"error":{"message":"insufficient_quota balance 余额不足"}}`, "rate_limited"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, tc.body)
		}))
		p := config.ProviderEntry{Kind: "openai", BaseURL: srv.URL}
		var checker Checker
		got := checker.Read(context.Background(), &p, netclient.ProxySpec{Mode: netclient.ModeOff})
		srv.Close()
		if got.Connection != tc.want {
			t.Fatalf("%s: %+v", tc.body, got)
		}
	}
}
