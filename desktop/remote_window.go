package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
	"unicode"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/internal/config"
)

const (
	remoteWindowTicketArgPrefix = "--remote-window-ticket="
	remoteWindowTicketPrefix    = ".remote-window-"
	remoteWindowTicketTTL       = 2 * time.Minute
	remoteWindowTicketMaxBytes  = 16 * 1024
)

// remoteWindowLaunch is a one-shot handoff from the primary Reasonix process to
// a native remote-desktop child window. Secrets live in a mode-0600 ticket file
// (never argv, URL query, logs, or DOM). Mode "gateway" loads the full desktop
// frontend and talks to the parent Remote Gateway over loopback RPC.
type remoteWindowLaunch struct {
	// Mode is "gateway" for the native remote desktop kernel. Empty is rejected
	// (legacy Serve HTML remote windows are removed).
	Mode         string `json:"mode"`
	GatewayURL   string `json:"gatewayUrl,omitempty"`
	GatewayToken string `json:"gatewayToken,omitempty"`
	SessionID    string `json:"sessionId,omitempty"`
	HostID       string `json:"hostId,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
	Title        string `json:"title,omitempty"`
	// URL is retained only for isSafeRemoteWindowURL validation of GatewayURL.
	URL string `json:"url,omitempty"`
}

func remoteWindowTicketPath(ticket string) (string, error) {
	if ticket == "" || filepath.Base(ticket) != ticket || !strings.HasPrefix(ticket, remoteWindowTicketPrefix) {
		return "", fmt.Errorf("invalid remote window ticket")
	}
	dir := strings.TrimSpace(config.MemoryUserDir())
	if dir == "" {
		return "", fmt.Errorf("cannot resolve remote window state directory")
	}
	return filepath.Join(dir, ticket), nil
}

func writeRemoteWindowLaunch(launch remoteWindowLaunch) (string, error) {
	if launch.Mode != "gateway" {
		return "", fmt.Errorf("remote window requires gateway mode (serve HTML remote entry removed)")
	}
	if !isSafeRemoteWindowURL(launch.GatewayURL) {
		return "", fmt.Errorf("remote gateway URL must use HTTP on loopback")
	}
	if strings.TrimSpace(launch.GatewayToken) == "" || strings.TrimSpace(launch.SessionID) == "" {
		return "", fmt.Errorf("remote window ticket missing gateway credentials")
	}
	dir := config.MemoryUserDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create remote window state directory: %w", err)
	}
	f, err := os.CreateTemp(dir, remoteWindowTicketPrefix)
	if err != nil {
		return "", fmt.Errorf("create remote window ticket: %w", err)
	}
	path := f.Name()
	remove := true
	defer func() {
		_ = f.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return "", fmt.Errorf("secure remote window ticket: %w", err)
	}
	if err := json.NewEncoder(f).Encode(launch); err != nil {
		return "", fmt.Errorf("write remote window ticket: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("sync remote window ticket: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close remote window ticket: %w", err)
	}
	remove = false
	return filepath.Base(path), nil
}

func consumeRemoteWindowLaunch(ticket string) (*remoteWindowLaunch, error) {
	path, err := remoteWindowTicketPath(ticket)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect remote window ticket: %w", err)
	}
	defer os.Remove(path)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("remote window ticket is not a regular file")
	}
	if info.Size() <= 0 || info.Size() > remoteWindowTicketMaxBytes {
		return nil, fmt.Errorf("remote window ticket has an invalid size")
	}
	// Windows does not expose Unix owner/group permission bits through Stat;
	// CreateTemp still creates the file for the current user, while ACLs remain
	// governed by the private user state directory.
	if goruntime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("remote window ticket permissions are too broad")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read remote window ticket: %w", err)
	}
	var launch remoteWindowLaunch
	if err := json.Unmarshal(data, &launch); err != nil {
		return nil, fmt.Errorf("decode remote window ticket: %w", err)
	}
	if launch.Mode != "gateway" {
		return nil, fmt.Errorf("legacy serve remote window tickets are no longer supported")
	}
	if !isSafeRemoteWindowURL(launch.GatewayURL) {
		return nil, fmt.Errorf("remote gateway URL must use HTTP on loopback")
	}
	return &launch, nil
}

func isSafeRemoteWindowURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil {
		return false
	}
	host := strings.TrimSpace(u.Hostname())
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func spawnRemoteWindow(launch remoteWindowLaunch) error {
	ticket, err := writeRemoteWindowLaunch(launch)
	if err != nil {
		return err
	}
	path, _ := remoteWindowTicketPath(ticket)
	executable, err := os.Executable()
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("locate Reasonix executable: %w", err)
	}
	cmd := exec.Command(executable, remoteWindowTicketArgPrefix+ticket)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("start remote Reasonix window: %w", err)
	}
	// The child normally consumes the ticket immediately. This bounds any
	// leftover token file if the child exits before reaching argument parsing.
	time.AfterFunc(remoteWindowTicketTTL, func() { _ = os.Remove(path) })
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release remote Reasonix window process: %w", err)
	}
	return nil
}

func remoteWindowTitle(hostID string) string {
	hostID = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, hostID))
	runes := []rune(hostID)
	if len(runes) > 80 {
		hostID = string(runes[:80]) + "…"
	}
	if hostID == "" {
		hostID = "Remote"
	}
	return "Reasonix [SSH: " + hostID + "]"
}

func (a *App) openRemoteGatewayWindow(launch remoteWindowLaunch) error {
	if a.remoteWindowOpener != nil {
		return a.remoteWindowOpener(launch)
	}
	return spawnRemoteWindow(launch)
}

// openRemoteWindow is retained for tests that inject remoteWindowOpener; production
// remote desktop uses openRemoteGatewayWindow only.
func (a *App) openRemoteWindow(rawURL, hostID string) error {
	return fmt.Errorf("serve HTML remote windows are removed; use the native remote desktop gateway")
}

func (a *App) remoteWindowAssetMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Gateway-mode remote windows load the full desktop frontend assets.
			// Do not replace index.html with a blank shell that navigates to Serve.
			next.ServeHTTP(w, r)
		})
	}
}

func (a *App) domReadyRemoteWindow() {
	if a.remoteWindow == nil {
		return
	}
	if a.remoteWindowNavigated.CompareAndSwap(false, true) {
		if a.remoteWindow.Title != "" {
			runtime.WindowSetTitle(a.ctx, a.remoteWindow.Title)
		}
		// Inject non-secret remote chrome context for the frontend status bar.
		// Token stays in Go memory only (bound methods), never in DOM/JS strings
		// beyond what Wails bindings already isolate.
		payload, _ := json.Marshal(map[string]any{
			"mode":      "gateway",
			"hostId":    a.remoteWindow.HostID,
			"workspace": a.remoteWindow.Workspace,
			"sessionId": a.remoteWindow.SessionID,
		})
		js := fmt.Sprintf("window.__REASONIX_REMOTE__=%s;", string(payload))
		runtime.WindowExecJS(a.ctx, js)
	}
	runtime.WindowCenter(a.ctx)
	runtime.WindowShow(a.ctx)
}

// RemoteWindowInfo returns the child's gateway binding for the remote AppBridge.
// Secrets are returned only over Wails IPC (not URL/DOM).
func (a *App) RemoteWindowInfo() map[string]string {
	if a.remoteWindow == nil {
		return nil
	}
	return map[string]string{
		"mode":         a.remoteWindow.Mode,
		"gatewayUrl":   a.remoteWindow.GatewayURL,
		"gatewayToken": a.remoteWindow.GatewayToken,
		"sessionId":    a.remoteWindow.SessionID,
		"hostId":       a.remoteWindow.HostID,
		"workspace":    a.remoteWindow.Workspace,
	}
}

// IsRemoteWindow reports whether this process is a remote desktop child.
func (a *App) IsRemoteWindow() bool {
	return a.remoteWindow != nil
}

// releaseRemoteGatewaySession notifies the parent gateway that this child is
// closing so the session token and Provider Broker capability are revoked.
func (a *App) releaseRemoteGatewaySession() {
	if a == nil || a.remoteWindow == nil {
		return
	}
	base := strings.TrimRight(strings.TrimSpace(a.remoteWindow.GatewayURL), "/")
	token := strings.TrimSpace(a.remoteWindow.GatewayToken)
	sid := strings.TrimSpace(a.remoteWindow.SessionID)
	if base == "" || token == "" || sid == "" {
		return
	}
	if !isSafeRemoteWindowURL(base) {
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodPost, base+"/gateway/v1/session/release", strings.NewReader("{}"))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Reasonix-Gateway-Token", token)
	req.Header.Set("X-Reasonix-Session-Id", sid)
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
