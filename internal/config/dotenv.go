package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadDotEnv loads only global credentials and legacy ~/.env. Project .env is no
// longer read, avoiding cwd-dependent API key or MCP env changes.
func loadDotEnv() {
	loadDotEnvGlobal()
}

// loadDotEnvGlobal loads the global credentials and legacy home .env only.
func loadDotEnvGlobal() {
	if p := UserCredentialsPath(); p != "" {
		loadDotEnvFile(p)
	}
	if home, err := os.UserHomeDir(); err == nil {
		loadDotEnvFile(filepath.Join(home, ".env"))
	}
}

// loadDotEnvForRoot calls loadDotEnvGlobal — project .env is no longer read.
func loadDotEnvForRoot(root string) {
	loadDotEnvGlobal()
}

// loadDotEnvFile reads one .env file (if present) and sets any keys not already
// present in the environment. Lenient, zero-dependency parsing.
func loadDotEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}
