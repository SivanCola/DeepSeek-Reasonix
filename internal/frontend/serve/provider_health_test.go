package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"reasonix/internal/contract/config"
	"reasonix/internal/contract/provider"
	"reasonix/internal/model/providerhealth"
)

func TestProviderHealthDoesNotGuessRemoteWalletConfiguration(t *testing.T) {
	s := newProviderEditServer(t)
	s.AllowProviderEdit()
	s.resolver = &provider.StaticResolver{}
	srv := httptest.NewServer(operatorHandler(s))
	defer srv.Close()
	resp := postProvider(t, srv.URL, "/providers/health", `{"name":"remote-only"}`)
	defer resp.Body.Close()
	var got providerhealth.Reading
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || got.Connection != "unknown" || got.Wallet != "unknown" || got.CheckedAt != 0 {
		t.Fatalf("remote catalog borrowed local evidence: %+v", got)
	}
}

func TestProviderHealthUsesSavedConfigAndKeepsModels(t *testing.T) {
	s := newProviderEditServer(t)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodGet {
			t.Error("health check generated output")
		}
		if r.URL.Path == "/balance" {
			fmt.Fprint(w, `{"is_available":false,"balance_infos":[{"currency":"CNY","total_balance":"0"}]}`)
			return
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer upstream.Close()
	body := fmt.Sprintf("default_model = \"existing/model-a\"\n[[providers]]\nname = \"existing\"\nkind = \"openai\"\nbase_url = %q\nmodels = [\"model-a\"]\nbalance_url = %q\nbalance_format = \"deepseek\"\n", upstream.URL, upstream.URL+"/balance")
	if err := os.WriteFile(config.UserConfigPath(), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(operatorHandler(s))
	defer srv.Close()
	resp := postProvider(t, srv.URL, "/providers/health", `{"name":"existing"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || hits.Load() != 0 {
		t.Fatal("health check bypassed grant")
	}
	s.AllowProviderEdit()
	for range 2 {
		resp = postProvider(t, srv.URL, "/providers/health", `{"name":"existing"}`)
		var got providerhealth.Reading
		json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || got.Connection != "reachable" || got.Wallet != "insufficient" {
			t.Fatalf("health = %d %+v", resp.StatusCode, got)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("not cached: %d", hits.Load())
	}
	resp, err := http.Get(srv.URL + "/models")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Models []modelEntry `json:"models"`
	}
	json.NewDecoder(resp.Body).Decode(&listed)
	resp.Body.Close()
	if len(listed.Models) != 1 || listed.Models[0].Ref != "existing/model-a" {
		t.Fatalf("health removed catalog models: %+v", listed)
	}
	resp = postProvider(t, srv.URL, "/providers/health", `{"name":"missing"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing = %d", resp.StatusCode)
	}
}
