package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reasonix/internal/config"
)

func TestRemoteWindowTicketRoundTripAndRemoval(t *testing.T) {
	launch := remoteWindowLaunch{
		Mode:         "gateway",
		GatewayURL:   "http://127.0.0.1:54321/",
		GatewayToken: "secret-token",
		SessionID:    "gws_test",
		HostID:       "box",
		Workspace:    "/home/dev/app",
		Title:        "Reasonix [SSH: box]",
	}
	ticket, err := writeRemoteWindowLaunch(launch)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ticket, "secret-token") || filepath.Base(ticket) != ticket {
		t.Fatalf("ticket leaked secret or path: %q", ticket)
	}
	path, err := remoteWindowTicketPath(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("ticket permissions = %o, want 600", got)
		}
	}
	got, err := consumeRemoteWindowLaunch(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != launch.Mode || got.GatewayURL != launch.GatewayURL || got.GatewayToken != launch.GatewayToken {
		t.Fatalf("launch = %+v, want %+v", *got, launch)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("ticket was not removed after consumption: %v", err)
	}
}

func TestRemoteWindowTicketRejectsServeHTMLAndUnsafeURLs(t *testing.T) {
	// Legacy serve HTML tickets are rejected.
	if _, err := writeRemoteWindowLaunch(remoteWindowLaunch{
		URL: "http://127.0.0.1:5000/?token=x",
	}); err == nil {
		t.Fatal("legacy serve launch accepted")
	}
	for _, raw := range []string{
		"https://127.0.0.1:5000/",
		"http://example.com:5000/",
		"file:///tmp/index.html",
		"javascript:alert(1)",
	} {
		if _, err := writeRemoteWindowLaunch(remoteWindowLaunch{
			Mode: "gateway", GatewayURL: raw, GatewayToken: "t", SessionID: "s",
		}); err == nil {
			t.Fatalf("unsafe URL accepted: %q", raw)
		}
	}
	for _, ticket := range []string{"", "../.remote-window-x", "/tmp/.remote-window-x", "unrelated"} {
		if _, err := remoteWindowTicketPath(ticket); err == nil {
			t.Fatalf("unsafe ticket accepted: %q", ticket)
		}
	}
}

func TestConsumeRemoteWindowTicketRejectsBroadPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits through os.Stat")
	}
	dir := config.MemoryUserDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ticket := remoteWindowTicketPrefix + "insecure"
	path := filepath.Join(dir, ticket)
	body := `{"mode":"gateway","gatewayUrl":"http://127.0.0.1:5000/","gatewayToken":"t","sessionId":"s"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if _, err := consumeRemoteWindowLaunch(ticket); err == nil {
		t.Fatal("ticket with broad permissions was accepted")
	}
}

func TestConsumeRemoteWindowTicketRejectsOversizedDescriptor(t *testing.T) {
	dir := config.MemoryUserDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ticket := remoteWindowTicketPrefix + "oversized"
	path := filepath.Join(dir, ticket)
	if err := os.WriteFile(path, make([]byte, remoteWindowTicketMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if _, err := consumeRemoteWindowLaunch(ticket); err == nil {
		t.Fatal("oversized ticket accepted")
	}
}

func TestConsumeRejectsLegacyServeTicket(t *testing.T) {
	dir := config.MemoryUserDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ticket := remoteWindowTicketPrefix + "legacy"
	path := filepath.Join(dir, ticket)
	body := `{"url":"http://127.0.0.1:5000/?token=x","title":"old"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if _, err := consumeRemoteWindowLaunch(ticket); err == nil {
		t.Fatal("legacy serve ticket accepted")
	}
}
