package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvReadsGlobalCredentials(t *testing.T) {
	cfgHome := t.TempDir()

	t.Chdir(t.TempDir())
	t.Setenv("HOME", cfgHome)
	t.Setenv("USERPROFILE", cfgHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(cfgHome, ".config"))
	t.Setenv("AppData", filepath.Join(cfgHome, "AppData"))

	// Write to the global credentials file
	cred := UserCredentialsPath()
	if cred == "" {
		t.Skip("user config dir unresolved on this platform")
	}
	if err := os.MkdirAll(filepath.Dir(cred), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte("KEY_GLOBAL=from_credentials\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Write legacy home .env
	if err := os.WriteFile(filepath.Join(cfgHome, ".env"), []byte("KEY_HOME=from_home\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KEY_GLOBAL", "")
	os.Unsetenv("KEY_GLOBAL")
	t.Setenv("KEY_HOME", "")
	os.Unsetenv("KEY_HOME")

	loadDotEnv()

	if got := os.Getenv("KEY_GLOBAL"); got != "from_credentials" {
		t.Errorf("global credentials not loaded: KEY_GLOBAL=%q want from_credentials", got)
	}
	if got := os.Getenv("KEY_HOME"); got != "from_home" {
		t.Errorf("home .env not loaded: KEY_HOME=%q want from_home", got)
	}
}

func TestLoadDotEnvDoesNotOverrideEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PINNED", "from_env")
	t.Chdir(t.TempDir())

	// Write to credentials to test env precedence
	cred := UserCredentialsPath()
	if cred != "" {
		if err := os.MkdirAll(filepath.Dir(cred), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(cred, []byte("PINNED=from_file\n"), 0o600)
	}

	loadDotEnv()

	if got := os.Getenv("PINNED"); got != "from_env" {
		t.Errorf("env var must win over .env: PINNED=%q want from_env", got)
	}
}
