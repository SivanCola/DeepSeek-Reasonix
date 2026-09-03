package boot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/provider"
)

func TestAccountSwitchSendsMatchingAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	team, err := cfg.AddProviderAccount("deepseek", "", "团队账号", "DEEPSEEK_API_KEY_TEAM")
	if err != nil {
		t.Fatal(err)
	}
	streamAccount := func(account config.ProviderAccount, key string) string {
		gotAuth = ""
		entries, ok := cfg.ResolveAccountProvider(account.ProviderID, account.ID)
		if !ok || len(entries) == 0 {
			t.Fatalf("no entries for %s", account.ID)
		}
		entry := entries[0]
		entry.BaseURL = srv.URL
		entry.Kind = "openai"
		if entry.Model == "" {
			entry.Model = entry.DefaultModel()
		}
		t.Setenv(entry.APIKeyEnv, key)
		entry.ResolveAPIKeyFromProcessEnvForProbe()
		p, err := NewProvider(&entry)
		if err != nil {
			t.Fatalf("NewProvider: %v", err)
		}
		ch, err := p.Stream(context.Background(), provider.Request{
			Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for chunk := range ch {
			if chunk.Type == provider.ChunkError {
				t.Fatalf("stream error: %v", chunk.Err)
			}
		}
		return gotAuth
	}
	if auth := streamAccount(cfg.ProviderAccounts[0], "sk-main"); auth != "Bearer sk-main" {
		t.Fatalf("main auth = %q", auth)
	}
	if auth := streamAccount(team, "sk-team"); auth != "Bearer sk-team" {
		t.Fatalf("team auth = %q", auth)
	}
}

func TestAccountSwitchKeepsPromptBytesStable(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	if _, err := cfg.AddProviderAccount("deepseek", "", "团队账号", "DEEPSEEK_API_KEY_TEAM"); err != nil {
		t.Fatal(err)
	}
	req := provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}
	for _, account := range cfg.ProviderAccounts {
		if account.ProviderID != "deepseek" {
			continue
		}
		entries, ok := cfg.ResolveAccountProvider(account.ProviderID, account.ID)
		if !ok || len(entries) == 0 {
			continue
		}
		entry := entries[0]
		entry.BaseURL = srv.URL
		entry.Kind = "openai"
		if entry.Model == "" {
			entry.Model = entry.DefaultModel()
		}
		t.Setenv(entry.APIKeyEnv, "sk-"+account.ID)
		entry.ResolveAPIKeyFromProcessEnvForProbe()
		p, err := NewProvider(&entry)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := p.Stream(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		for range ch {
		}
	}
	if len(bodies) < 2 {
		t.Fatalf("got %d bodies", len(bodies))
	}
	msg0, _ := json.Marshal(bodies[0]["messages"])
	msg1, _ := json.Marshal(bodies[1]["messages"])
	if string(msg0) != string(msg1) {
		t.Fatalf("prompt bytes changed across accounts:\n%s\n%s", msg0, msg1)
	}
}
