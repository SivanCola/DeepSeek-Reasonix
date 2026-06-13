package cli

import "testing"

func TestResolveDaemonWebhookConfigDisabled(t *testing.T) {
	cfg, err := resolveDaemonWebhookConfig(false, "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolveDaemonWebhookConfig: %v", err)
	}
	if cfg != nil {
		t.Fatalf("config = %+v, want nil", cfg)
	}
}

func TestResolveDaemonWebhookConfigRequiresSecret(t *testing.T) {
	_, err := resolveDaemonWebhookConfig(true, "", func(string) string { return "" })
	if err == nil {
		t.Fatal("expected missing secret error")
	}
}

func TestResolveDaemonWebhookConfigFromFlag(t *testing.T) {
	cfg, err := resolveDaemonWebhookConfig(true, "  flag-secret  ", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolveDaemonWebhookConfig: %v", err)
	}
	if cfg == nil || !cfg.Enabled || cfg.Secret != "flag-secret" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestResolveDaemonWebhookConfigFromEnv(t *testing.T) {
	cfg, err := resolveDaemonWebhookConfig(false, "", func(key string) string {
		if key != "REASONIX_DAEMON_WEBHOOK_SECRET" {
			t.Fatalf("unexpected env key %q", key)
		}
		return "env-secret"
	})
	if err != nil {
		t.Fatalf("resolveDaemonWebhookConfig: %v", err)
	}
	if cfg == nil || !cfg.Enabled || cfg.Secret != "env-secret" {
		t.Fatalf("config = %+v", cfg)
	}
}
