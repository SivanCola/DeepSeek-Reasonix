package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"reasonix/desktop/internal/browseripc"
)

// testBrowserApp builds an App with the browser surface wired but the
// companion resolution failing, so every call exercises the
// component-missing path without spawning a process.
func testBrowserApp() *App {
	a := &App{
		tabs:        map[string]*WorkspaceTab{"chat-1": {}, "chat-2": {}},
		activeTabID: "chat-1",
		ctx:         context.Background(),
	}
	a.browser = newBrowserCoordinator(browserCoordinatorOptions{
		resolveBinary: func() (string, error) { return "", os.ErrNotExist },
		spawn: func(ctx context.Context, path string, env []string) (*exec.Cmd, io.WriteCloser, io.Reader, io.Reader, error) {
			return nil, nil, nil, nil, errors.New("unreachable")
		},
		now: time.Now,
	})
	a.browserState = newBrowserStateStore()
	return a
}

// TestOpenBrowserURLRejectsNonHttp: file/mailto/unknown schemes never reach
// the coordinator; only http(s) opens are accepted.
func TestOpenBrowserURLRejectsNonHttp(t *testing.T) {
	a := testBrowserApp()
	for _, url := range []string{
		"file:///etc/passwd",
		"mailto:test@example.com",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"ftp://example.com",
		"",
	} {
		err := a.OpenBrowserURL("", url, "foreground")
		if err == nil || !strings.Contains(err.Error(), "http(s)") {
			t.Errorf("url %q: err = %v, want http(s) rejection", url, err)
		}
	}
}

// TestOpenBrowserURLTabResolution: empty tabID uses the active chat; unknown
// tabID is rejected before any call.
func TestOpenBrowserURLTabResolution(t *testing.T) {
	a := testBrowserApp()
	err := a.OpenBrowserURL("ghost-chat", "https://example.com", "foreground")
	if err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("unknown chat: err = %v", err)
	}
	if err := a.OpenBrowserURL("", "https://example.com", "foreground"); err == nil {
		t.Fatal("missing companion should error")
	}
	if err := a.OpenBrowserURL("chat-1", "https://example.com", "sideways"); err == nil ||
		!strings.Contains(err.Error(), "invalid disposition") {
		t.Fatalf("bad disposition: err = %v", err)
	}
}

// TestGetBrowserStatusArrayContract: status surfaces [] never null even in the
// component-missing state, and JSON encodes arrays not null.
func TestGetBrowserStatusArrayContract(t *testing.T) {
	a := testBrowserApp()
	view := a.GetBrowserStatus()
	if view.Capabilities == nil {
		t.Fatal("Capabilities is nil; Wails contract requires []")
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"capabilities":null`) {
		t.Fatalf("Capabilities encodes as null: %s", data)
	}
	if !view.RecoveryAvailable {
		t.Fatal("component-missing state must offer recovery")
	}
}

// TestListBrowserSiteGrantsArrayContract: grants surface is [] never null.
func TestListBrowserSiteGrantsArrayContract(t *testing.T) {
	a := testBrowserApp()
	view, err := a.ListBrowserSiteGrants()
	if err == nil {
		t.Fatal("missing companion should error")
	}
	if view.Grants == nil {
		t.Fatal("Grants is nil; Wails contract requires []")
	}
	data, _ := json.Marshal(view)
	if strings.Contains(string(data), `"grants":null`) {
		t.Fatalf("Grants encodes as null: %s", data)
	}
}

// TestClearBrowserDataValidation: empty and unknown scopes fail cleanly with
// [] returns, never nil.
func TestClearBrowserDataValidation(t *testing.T) {
	a := testBrowserApp()
	cleared, err := a.ClearBrowserData(BrowserDataClearRequest{Scopes: []string{}})
	if err == nil || cleared == nil {
		t.Fatalf("empty scopes: cleared=%v err=%v", cleared, err)
	}
	cleared, err = a.ClearBrowserData(BrowserDataClearRequest{Scopes: []string{"everything"}})
	if err == nil || cleared == nil {
		t.Fatalf("unknown scope: cleared=%v err=%v", cleared, err)
	}
	// Valid scopes forward to the companion and report component_missing.
	cleared, err = a.ClearBrowserData(BrowserDataClearRequest{Scopes: []string{"cookies"}})
	if err == nil || cleared == nil {
		t.Fatalf("valid scope: cleared=%v err=%v", cleared, err)
	}
}

// TestBrowserSettingsRoundTrip: defaults to builtin; patches persist; invalid
// modes are rejected.
func TestBrowserSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	orig := browserSettingsPath
	browserSettingsPath = func() string {
		return dir + "/" + browserSettingsFileName
	}
	t.Cleanup(func() { browserSettingsPath = orig })

	a := testBrowserApp()
	if view := a.GetBrowserSettings(); view.DefaultOpenMode != browserDefaultOpenModeBuiltin {
		t.Fatalf("default mode = %q, want builtin", view.DefaultOpenMode)
	}
	if err := a.UpdateBrowserSettings(BrowserSettingsPatch{DefaultOpenMode: "sideways"}); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if err := a.UpdateBrowserSettings(BrowserSettingsPatch{DefaultOpenMode: browserDefaultOpenModeSystem}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if view := a.GetBrowserSettings(); view.DefaultOpenMode != browserDefaultOpenModeSystem {
		t.Fatalf("mode after update = %q", view.DefaultOpenMode)
	}
	// Persisted across a fresh App instance.
	if view := testBrowserApp().GetBrowserSettings(); view.DefaultOpenMode != browserDefaultOpenModeSystem {
		t.Fatalf("mode after reload = %q", view.DefaultOpenMode)
	}
}

// TestBrowserSettingsFutureFormatNotOverwritten: settings written by a newer
// format version must survive an older version's update attempt untouched.
func TestBrowserSettingsFutureFormatNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	orig := browserSettingsPath
	browserSettingsPath = func() string {
		return dir + "/" + browserSettingsFileName
	}
	t.Cleanup(func() { browserSettingsPath = orig })

	future := `{"format":"reasonix.browser.settings.v2","version":2,"defaultOpenMode":"system","futureField":"keep-me"}`
	if err := os.WriteFile(browserSettingsPath(), []byte(future), 0o600); err != nil {
		t.Fatal(err)
	}
	a := testBrowserApp()
	if view := a.GetBrowserSettings(); view.DefaultOpenMode != browserDefaultOpenModeBuiltin {
		t.Fatalf("future settings must fall back to defaults, got %q", view.DefaultOpenMode)
	}
	if err := a.UpdateBrowserSettings(BrowserSettingsPatch{DefaultOpenMode: browserDefaultOpenModeSystem}); err == nil {
		t.Fatal("update over a future-format file must fail")
	}
	after, err := os.ReadFile(browserSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != future {
		t.Fatalf("future settings file was overwritten:\n got: %s\nwant: %s", after, future)
	}
}

// TestInstallOrRepairBrowserComponentTypedError: the recovery entry exists and
// fails with a clear, actionable error until Phase 5 ships distribution.
func TestInstallOrRepairBrowserComponentTypedError(t *testing.T) {
	a := testBrowserApp()
	err := a.InstallOrRepairBrowserComponent()
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("err = %v", err)
	}
}

// TestBrowserIPCRequestBudget: a coordinator with a full pending map rejects
// new calls instead of unbounded growth.
func TestBrowserIPCRequestBudget(t *testing.T) {
	a := testBrowserApp()
	// Force the ready state with a sink writer so the call reaches the pending
	// budget check without spawning a companion.
	a.browser.mu.Lock()
	a.browser.state = browserReady
	a.browser.writer = discardWriteCloser{}
	a.browser.mu.Unlock()
	for i := 0; i < browseripc.MaxPendingRequests; i++ {
		a.browser.mu.Lock()
		a.browser.pending[fmt.Sprintf("req-%d", i)] = &pendingBrowserCall{
			reply: make(chan browseripc.Response, 1),
		}
		a.browser.mu.Unlock()
	}
	err := a.OpenBrowserURL("chat-1", "https://example.com", "foreground")
	if err == nil || !strings.Contains(err.Error(), "limit reached") {
		t.Fatalf("err = %v, want pending limit error", err)
	}
}

type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriteCloser) Close() error                { return nil }
