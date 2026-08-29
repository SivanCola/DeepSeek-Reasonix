package main

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/netclient"
)

func TestNetworkProxySpecForRootMatchesEffectiveProjectConfig(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte("[network]\nproxy_mode = \"off\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PROJECT_PROXY_URL=http://127.0.0.1:9876\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte("[network]\nproxy_mode = \"custom\"\nproxy_url = \"${PROJECT_PROXY_URL}\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	spec := NewApp().networkProxySpecForRoot(root)
	if spec.Mode != netclient.ModeCustom || spec.URL != "http://127.0.0.1:9876" {
		t.Fatalf("model probe proxy = %+v, want effective project proxy", spec)
	}
}
