package main

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/internal/config"
	"reasonix/internal/remote"
	"reasonix/internal/remote/bootstrap"
	"reasonix/internal/remote/forward"
)

// ── View structs mirrored in frontend/src/lib/types.ts ──

type RemoteHostView struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	User             string `json:"user"`
	IdentityFile     string `json:"identityFile"`
	ProxyJump        string `json:"proxyJump"`
	DefaultWorkspace string `json:"defaultWorkspace"`
	ServeInstall     string `json:"serveInstall"`
	UseSSHConfig     bool   `json:"useSSHConfig"`
}

type RemoteHostInput struct {
	Label            string `json:"label"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	User             string `json:"user"`
	IdentityFile     string `json:"identityFile"`
	ProxyJump        string `json:"proxyJump"`
	DefaultWorkspace string `json:"defaultWorkspace"`
	ServeInstall     string `json:"serveInstall"`
	UseSSHConfig     bool   `json:"useSSHConfig"`
}

type RemoteFingerprintView struct {
	HostID  string `json:"hostId"`
	Address string `json:"address"`
	KeyType string `json:"keyType"`
	SHA256  string `json:"sha256"`
}

type RemoteConnectionStatusView struct {
	HostID      string                 `json:"hostId"`
	State       string                 `json:"state"`
	Error       string                 `json:"error,omitempty"`
	Fingerprint *RemoteFingerprintView `json:"fingerprint,omitempty"`
	Attempt     int                    `json:"attempt,omitempty"`
}

type RemoteDirEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"isDir"`
	Size      int64  `json:"size"`
	MtimeUnix int64  `json:"mtimeUnix"`
	Symlink   bool   `json:"symlink"`
}

type RemoteFilePreview struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Size      int64  `json:"size"`
	MtimeUnix int64  `json:"mtimeUnix"`
	Truncated bool   `json:"truncated"`
	Binary    bool   `json:"binary"`
	Err       string `json:"err,omitempty"`
}

type RemoteWriteResult struct {
	OK           bool  `json:"ok"`
	Conflict     bool  `json:"conflict"`
	NewMtimeUnix int64 `json:"newMtimeUnix"`
}

type RemoteForwardInput struct {
	LocalPort  int    `json:"localPort"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
	Label      string `json:"label"`
}

type RemoteForwardView struct {
	ID         string `json:"id"`
	HostID     string `json:"hostId"`
	LocalPort  int    `json:"localPort"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
	Label      string `json:"label"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
}

type RemoteServerView struct {
	HostID    string `json:"hostId"`
	Workspace string `json:"workspace"`
	State     string `json:"state"`
	Message   string `json:"message,omitempty"`
	LocalURL  string `json:"localUrl,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ── Kernel seam ──

// remoteKernel is the desktop's view of the remote subsystem. The concrete
// *desktopRemoteManager satisfies it; remote_app_test.go injects a fake.
type remoteKernel interface {
	Hosts() ([]RemoteHostView, error)
	AddHost(RemoteHostInput) (RemoteHostView, error)
	UpdateHost(id string, in RemoteHostInput) (RemoteHostView, error)
	RemoveHost(id string) error
	ScanSSHConfig() ([]RemoteHostInput, error)

	Connect(hostID string) error
	Disconnect(hostID string) error
	Statuses() []RemoteConnectionStatusView
	ResolveHostKey(hostID string, accept bool) error

	ListDir(ctx context.Context, hostID, path string) ([]RemoteDirEntry, error)
	ReadFile(ctx context.Context, hostID, path string) (RemoteFilePreview, error)
	WriteFile(ctx context.Context, hostID, path, body string, expectMtime int64) (RemoteWriteResult, error)
	Mkdir(ctx context.Context, hostID, path string) error
	Rename(ctx context.Context, hostID, oldPath, newPath string) error
	Delete(ctx context.Context, hostID, path string, recursive bool) error

	Forwards(hostID string) []RemoteForwardView
	AddForward(hostID string, in RemoteForwardInput) (RemoteForwardView, error)
	RemoveForward(hostID, forwardID string) error

	EnsureServer(ctx context.Context, hostID, workspace string) (RemoteServerView, string, error)
	StopServer(hostID string) error
	ServerStatus(hostID string) RemoteServerView
	ServerLogs(ctx context.Context, hostID string, tailLines int) (string, error)

	Close() error
}

// remoteEventSink receives kernel status transitions for bridging to the
// frontend. All methods may be called from kernel goroutines.
type remoteEventSink interface {
	onStatus(RemoteConnectionStatusView)
	onForwards(hostID string, forwards []RemoteForwardView)
	onServer(RemoteServerView)
}

// ── App wiring ──

func (a *App) remoteRT() (remoteKernel, error) {
	a.remoteMu.Lock()
	defer a.remoteMu.Unlock()
	if a.remoteRuntime != nil {
		return a.remoteRuntime, nil
	}
	mgr := newDesktopRemoteManager(a)
	a.remoteRuntime = mgr
	return mgr, nil
}

func (a *App) stopRemoteRuntime() {
	a.remoteMu.Lock()
	rt := a.remoteRuntime
	a.remoteRuntime = nil
	a.remoteMu.Unlock()
	if rt != nil {
		_ = rt.Close()
	}
}

// emitRemoteEvent bridges a kernel callback to the frontend through the async
// emitter so a slow webview never blocks the kernel.
func (a *App) emitRemoteEvent(name string, payload any) {
	ctx := a.bootContext()
	if ctx == nil {
		return
	}
	a.runtimeEvents.Emit(ctx, name, payload)
}

// remoteEventSink implementation on *App.
func (a *App) onStatus(s RemoteConnectionStatusView) { a.emitRemoteEvent("remote:status", s) }
func (a *App) onServer(s RemoteServerView)           { a.emitRemoteEvent("remote:server", s) }
func (a *App) onForwards(hostID string, f []RemoteForwardView) {
	a.emitRemoteEvent("remote:forwards", map[string]any{"hostId": hostID, "forwards": f})
}

// ── Bound methods ──

func (a *App) RemoteHosts() ([]RemoteHostView, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return nil, err
	}
	return rt.Hosts()
}

func (a *App) AddRemoteHost(in RemoteHostInput) (RemoteHostView, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return RemoteHostView{}, err
	}
	return rt.AddHost(in)
}

func (a *App) UpdateRemoteHost(id string, in RemoteHostInput) (RemoteHostView, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return RemoteHostView{}, err
	}
	return rt.UpdateHost(id, in)
}

func (a *App) RemoveRemoteHost(id string) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	return rt.RemoveHost(id)
}

func (a *App) ScanSSHConfig() ([]RemoteHostInput, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return nil, err
	}
	return rt.ScanSSHConfig()
}

func (a *App) ConnectRemoteHost(id string) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	return rt.Connect(id)
}

func (a *App) DisconnectRemoteHost(id string) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	return rt.Disconnect(id)
}

func (a *App) RemoteConnectionStatuses() []RemoteConnectionStatusView {
	rt, err := a.remoteRT()
	if err != nil {
		return nil
	}
	return rt.Statuses()
}

func (a *App) ConfirmRemoteHostKey(hostID string, accept bool) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	return rt.ResolveHostKey(hostID, accept)
}

func (a *App) ListRemoteDir(hostID, path string) ([]RemoteDirEntry, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return nil, err
	}
	return rt.ListDir(a.bootContext(), hostID, path)
}

func (a *App) ReadRemoteFile(hostID, path string) (RemoteFilePreview, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return RemoteFilePreview{}, err
	}
	return rt.ReadFile(a.bootContext(), hostID, path)
}

func (a *App) WriteRemoteFile(hostID, path, body string, expectMtimeUnix int64) (RemoteWriteResult, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return RemoteWriteResult{}, err
	}
	return rt.WriteFile(a.bootContext(), hostID, path, body, expectMtimeUnix)
}

func (a *App) MkdirRemote(hostID, path string) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	return rt.Mkdir(a.bootContext(), hostID, path)
}

func (a *App) RenameRemotePath(hostID, oldPath, newPath string) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	return rt.Rename(a.bootContext(), hostID, oldPath, newPath)
}

func (a *App) DeleteRemotePath(hostID, path string, recursive bool) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	return rt.Delete(a.bootContext(), hostID, path, recursive)
}

func (a *App) RemoteForwards(hostID string) ([]RemoteForwardView, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return nil, err
	}
	return rt.Forwards(hostID), nil
}

func (a *App) AddRemoteForward(hostID string, in RemoteForwardInput) (RemoteForwardView, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return RemoteForwardView{}, err
	}
	return rt.AddForward(hostID, in)
}

func (a *App) RemoveRemoteForward(hostID, forwardID string) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	return rt.RemoveForward(hostID, forwardID)
}

func (a *App) EnsureRemoteServer(hostID, workspace string) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	a.goSafe("remoteEnsureServer", func() {
		_, _, _ = rt.EnsureServer(a.bootContext(), hostID, workspace)
	})
	return nil
}

func (a *App) OpenRemoteWorkspace(hostID, workspace string) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	view, token, err := rt.EnsureServer(a.bootContext(), hostID, workspace)
	if err != nil {
		return err
	}
	if view.LocalURL == "" {
		return fmt.Errorf("remote serve did not report a local URL")
	}
	url := view.LocalURL
	if token != "" && !strings.Contains(url, "token=") {
		url = fmt.Sprintf("%s?token=%s", strings.TrimRight(url, "/"), token)
	}
	a.saveLastRemoteWorkspace(hostID, workspace)
	return a.openExternalURL(url)
}

func (a *App) StopRemoteServer(hostID string) error {
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	return rt.StopServer(hostID)
}

func (a *App) RemoteServerStatus(hostID string) (RemoteServerView, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return RemoteServerView{}, err
	}
	return rt.ServerStatus(hostID), nil
}

func (a *App) RemoteServerLogs(hostID string, tailLines int) (string, error) {
	rt, err := a.remoteRT()
	if err != nil {
		return "", err
	}
	return rt.ServerLogs(a.bootContext(), hostID, tailLines)
}

// openExternalURL opens url in the system browser via Wails.
func (a *App) openExternalURL(url string) error {
	if a.ctx == nil {
		return fmt.Errorf("no window context to open %s", url)
	}
	wruntime.BrowserOpenURL(a.ctx, url)
	return nil
}

// editUserConfig runs mutate against the user-global config under the edit lock
// and saves it there. Remote hosts are user-global (pinned in LoadForRoot).
func editUserConfig(mutate func(*config.Config) error) error {
	unlock := config.LockUserConfigEdits()
	defer unlock()
	path := config.UserConfigPath()
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("cannot resolve user config path")
	}
	cfg := config.LoadForEdit(path)
	if cfg == nil {
		cfg = config.Default()
	}
	if err := mutate(cfg); err != nil {
		return err
	}
	return cfg.SaveTo(path)
}

// ── desktopRemoteManager: concrete remoteKernel ──

type managedHost struct {
	client   *remote.Client
	cancel   context.CancelFunc
	status   RemoteConnectionStatusView
	server   RemoteServerView
	token    string
	fpAnswer chan bool // TOFU resolution channel; non-nil while a prompt is pending
}

type desktopRemoteManager struct {
	sink remoteEventSink

	mu    sync.Mutex
	hosts map[string]*managedHost
}

func newDesktopRemoteManager(sink remoteEventSink) *desktopRemoteManager {
	return &desktopRemoteManager{sink: sink, hosts: map[string]*managedHost{}}
}

func (m *desktopRemoteManager) Hosts() ([]RemoteHostView, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	out := make([]RemoteHostView, 0, len(cfg.Remote.Hosts))
	for _, h := range cfg.Remote.Hosts {
		out = append(out, hostEntryToView(h))
	}
	return out, nil
}

func (m *desktopRemoteManager) AddHost(in RemoteHostInput) (RemoteHostView, error) {
	entry := inputToHostEntry(in)
	if err := editUserConfig(func(c *config.Config) error { return c.UpsertRemoteHost(entry) }); err != nil {
		return RemoteHostView{}, err
	}
	return hostEntryToView(entry), nil
}

func (m *desktopRemoteManager) UpdateHost(id string, in RemoteHostInput) (RemoteHostView, error) {
	entry := inputToHostEntry(in)
	entry.Name = id
	if err := editUserConfig(func(c *config.Config) error { return c.UpsertRemoteHost(entry) }); err != nil {
		return RemoteHostView{}, err
	}
	return hostEntryToView(entry), nil
}

func (m *desktopRemoteManager) RemoveHost(id string) error {
	_ = m.Disconnect(id)
	removed := false
	if err := editUserConfig(func(c *config.Config) error {
		removed = c.RemoveRemoteHost(id)
		return nil
	}); err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no remote host named %q", id)
	}
	return nil
}

func (m *desktopRemoteManager) ScanSSHConfig() ([]RemoteHostInput, error) {
	src, err := remote.LoadUserSSHConfig()
	if err != nil {
		return nil, err
	}
	var out []RemoteHostInput
	for _, cand := range src.Aliases() {
		host := cand.HostName
		if host == "" {
			host = cand.Alias
		}
		out = append(out, RemoteHostInput{
			Label:        cand.Alias,
			Host:         host,
			Port:         cand.Port,
			User:         cand.User,
			IdentityFile: cand.IdentityFile,
			ProxyJump:    cand.ProxyJump,
			UseSSHConfig: true,
		})
	}
	return out, nil
}

func (m *desktopRemoteManager) Connect(hostID string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	sshCfg, _ := remote.LoadUserSSHConfig()
	host, err := remote.ResolveHost(cfg, hostID, sshCfg)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if mh, ok := m.hosts[hostID]; ok && mh.client != nil {
		m.mu.Unlock()
		return nil // already connecting/connected
	}
	mh := &managedHost{}
	m.hosts[hostID] = mh
	m.mu.Unlock()

	auth := remote.AuthOptions{}
	if entry, ok := cfg.RemoteHost(hostID); ok {
		if entry.PassphraseEnv != "" {
			env := entry.PassphraseEnv
			auth.Passphrase = func() (string, error) { return config.ResolveCredential(env).Value, nil }
		}
		if entry.PasswordEnv != "" {
			env := entry.PasswordEnv
			auth.Password = func() (string, error) { return config.ResolveCredential(env).Value, nil }
		}
	}

	policy := &remote.HostKeyPolicy{Prompt: m.hostKeyPrompt(hostID)}
	client, err := remote.New(remote.Options{Host: host, Auth: auth, HostKeys: policy})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	mh.client = client
	mh.cancel = cancel
	m.mu.Unlock()

	client.Subscribe(func(ev remote.StatusEvent) { m.onClientStatus(hostID, ev) })
	client.Forwards() // ensure set exists

	go func() {
		if err := client.Start(ctx); err != nil {
			// Start's error is already reflected via the Stopped status event.
			return
		}
		m.applyConfiguredForwards(hostID, cfg)
	}()
	return nil
}

func (m *desktopRemoteManager) applyConfiguredForwards(hostID string, cfg *config.Config) {
	entry, ok := cfg.RemoteHost(hostID)
	if !ok {
		return
	}
	client := m.client(hostID)
	if client == nil {
		return
	}
	for _, f := range entry.Forwards {
		dir := forward.Local
		if strings.EqualFold(f.Type, "remote") {
			dir = forward.Remote
		}
		_, _ = client.Forwards().Add(forward.Spec{Direction: dir, BindAddr: f.Bind, TargetAddr: f.Target})
	}
	m.emitForwards(hostID)
}

func (m *desktopRemoteManager) Disconnect(hostID string) error {
	m.mu.Lock()
	mh := m.hosts[hostID]
	delete(m.hosts, hostID)
	m.mu.Unlock()
	if mh == nil {
		return nil
	}
	if mh.fpAnswer != nil {
		select {
		case mh.fpAnswer <- false:
		default:
		}
	}
	if mh.cancel != nil {
		mh.cancel()
	}
	if mh.client != nil {
		_ = mh.client.Close()
	}
	return nil
}

func (m *desktopRemoteManager) Statuses() []RemoteConnectionStatusView {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RemoteConnectionStatusView, 0, len(m.hosts))
	for _, mh := range m.hosts {
		out = append(out, mh.status)
	}
	return out
}

func (m *desktopRemoteManager) ResolveHostKey(hostID string, accept bool) error {
	m.mu.Lock()
	mh := m.hosts[hostID]
	var ch chan bool
	if mh != nil {
		ch = mh.fpAnswer
	}
	m.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("no pending host key confirmation for %q", hostID)
	}
	select {
	case ch <- accept:
		return nil
	default:
		return fmt.Errorf("host key confirmation already resolved for %q", hostID)
	}
}

// hostKeyPrompt returns a HostKeyPrompt that surfaces the fingerprint as a
// pending_hostkey status and blocks on the answer channel until the UI calls
// ConfirmRemoteHostKey.
func (m *desktopRemoteManager) hostKeyPrompt(hostID string) remote.HostKeyPrompt {
	return func(ctx context.Context, q remote.HostKeyQuestion) (bool, error) {
		answer := make(chan bool, 1)
		m.mu.Lock()
		mh := m.hosts[hostID]
		if mh == nil {
			m.mu.Unlock()
			return false, fmt.Errorf("host %q gone", hostID)
		}
		mh.fpAnswer = answer
		fp := &RemoteFingerprintView{HostID: hostID, Address: q.Address, KeyType: q.KeyType, SHA256: q.Fingerprint}
		mh.status = RemoteConnectionStatusView{HostID: hostID, State: "pending_hostkey", Fingerprint: fp}
		status := mh.status
		m.mu.Unlock()
		m.sink.onStatus(status)

		select {
		case ok := <-answer:
			m.mu.Lock()
			if mh2 := m.hosts[hostID]; mh2 != nil {
				mh2.fpAnswer = nil
			}
			m.mu.Unlock()
			return ok, nil
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(2 * time.Minute):
			return false, fmt.Errorf("host key confirmation timed out")
		}
	}
}

func (m *desktopRemoteManager) onClientStatus(hostID string, ev remote.StatusEvent) {
	view := RemoteConnectionStatusView{
		HostID:  hostID,
		State:   statusString(ev.Status),
		Attempt: ev.Attempt,
	}
	if ev.Err != nil {
		view.Error = ev.Err.Error()
	}
	m.mu.Lock()
	if mh := m.hosts[hostID]; mh != nil {
		// Preserve a pending fingerprint that a separate prompt goroutine set.
		if mh.status.State == "pending_hostkey" && view.State == "connecting" {
			m.mu.Unlock()
			return
		}
		mh.status = view
	}
	m.mu.Unlock()
	m.sink.onStatus(view)
}

func (m *desktopRemoteManager) client(hostID string) *remote.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mh := m.hosts[hostID]; mh != nil {
		return mh.client
	}
	return nil
}

func (m *desktopRemoteManager) fs(ctx context.Context, hostID string) (*remote.Client, error) {
	c := m.client(hostID)
	if c == nil {
		return nil, fmt.Errorf("host %q is not connected", hostID)
	}
	return c, nil
}

func (m *desktopRemoteManager) ListDir(ctx context.Context, hostID, path string) ([]RemoteDirEntry, error) {
	c, err := m.fs(ctx, hostID)
	if err != nil {
		return nil, err
	}
	fsys, err := c.SFTP()
	if err != nil {
		return nil, err
	}
	entries, err := fsys.List(ctx, path)
	if err != nil {
		return nil, err
	}
	out := make([]RemoteDirEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, RemoteDirEntry{
			Name: e.Name, Path: e.Path, IsDir: e.IsDir,
			Size: e.Size, MtimeUnix: e.ModTime, Symlink: e.Symlink,
		})
	}
	return out, nil
}

func (m *desktopRemoteManager) ReadFile(ctx context.Context, hostID, path string) (RemoteFilePreview, error) {
	c, err := m.fs(ctx, hostID)
	if err != nil {
		return RemoteFilePreview{}, err
	}
	fsys, err := c.SFTP()
	if err != nil {
		return RemoteFilePreview{}, err
	}
	st, err := fsys.Stat(ctx, path)
	if err != nil {
		return RemoteFilePreview{Path: path, Err: err.Error()}, nil
	}
	data, truncated, kind, err := fsys.ReadFile(ctx, path, 0)
	if err != nil {
		return RemoteFilePreview{Path: path, Err: err.Error()}, nil
	}
	binary := kind != 0 // sftpfs.KindText == 0
	prev := RemoteFilePreview{
		Path: path, Size: st.Size, MtimeUnix: st.ModTime,
		Truncated: truncated, Binary: binary,
	}
	if !binary {
		prev.Body = string(data)
	}
	return prev, nil
}

func (m *desktopRemoteManager) WriteFile(ctx context.Context, hostID, path, body string, expectMtime int64) (RemoteWriteResult, error) {
	c, err := m.fs(ctx, hostID)
	if err != nil {
		return RemoteWriteResult{}, err
	}
	fsys, err := c.SFTP()
	if err != nil {
		return RemoteWriteResult{}, err
	}
	// Optimistic-concurrency check: if the caller passed an expected mtime and
	// the remote file moved, report a conflict instead of overwriting.
	if expectMtime > 0 {
		if st, serr := fsys.Stat(ctx, path); serr == nil && st.ModTime != expectMtime {
			return RemoteWriteResult{Conflict: true}, nil
		}
	}
	if err := fsys.WriteFileAtomic(ctx, path, []byte(body), 0o644); err != nil {
		return RemoteWriteResult{}, err
	}
	st, _ := fsys.Stat(ctx, path)
	return RemoteWriteResult{OK: true, NewMtimeUnix: st.ModTime}, nil
}

func (m *desktopRemoteManager) Mkdir(ctx context.Context, hostID, path string) error {
	c, err := m.fs(ctx, hostID)
	if err != nil {
		return err
	}
	fsys, err := c.SFTP()
	if err != nil {
		return err
	}
	return fsys.MkdirAll(ctx, path)
}

func (m *desktopRemoteManager) Rename(ctx context.Context, hostID, oldPath, newPath string) error {
	c, err := m.fs(ctx, hostID)
	if err != nil {
		return err
	}
	fsys, err := c.SFTP()
	if err != nil {
		return err
	}
	return fsys.Rename(ctx, oldPath, newPath)
}

func (m *desktopRemoteManager) Delete(ctx context.Context, hostID, path string, recursive bool) error {
	c, err := m.fs(ctx, hostID)
	if err != nil {
		return err
	}
	fsys, err := c.SFTP()
	if err != nil {
		return err
	}
	return fsys.Remove(ctx, path, recursive)
}

func (m *desktopRemoteManager) Forwards(hostID string) []RemoteForwardView {
	c := m.client(hostID)
	if c == nil {
		return nil
	}
	return forwardEntriesToViews(hostID, c.Forwards().List())
}

func (m *desktopRemoteManager) AddForward(hostID string, in RemoteForwardInput) (RemoteForwardView, error) {
	c := m.client(hostID)
	if c == nil {
		return RemoteForwardView{}, fmt.Errorf("host %q is not connected", hostID)
	}
	spec := forward.Spec{
		Name:       in.Label,
		Direction:  forward.Local,
		BindAddr:   fmt.Sprintf("127.0.0.1:%d", in.LocalPort),
		TargetAddr: fmt.Sprintf("%s:%d", in.RemoteHost, in.RemotePort),
	}
	if _, err := c.Forwards().Add(spec); err != nil {
		return RemoteForwardView{}, err
	}
	m.emitForwards(hostID)
	view := RemoteForwardView{
		ID: spec.DefaultName(), HostID: hostID, LocalPort: in.LocalPort,
		RemoteHost: in.RemoteHost, RemotePort: in.RemotePort, Label: in.Label, State: "active",
	}
	return view, nil
}

func (m *desktopRemoteManager) RemoveForward(hostID, forwardID string) error {
	c := m.client(hostID)
	if c == nil {
		return fmt.Errorf("host %q is not connected", hostID)
	}
	if err := c.Forwards().Remove(forwardID); err != nil {
		return err
	}
	m.emitForwards(hostID)
	return nil
}

func (m *desktopRemoteManager) emitForwards(hostID string) {
	if m.sink != nil {
		m.sink.onForwards(hostID, m.Forwards(hostID))
	}
}

func (m *desktopRemoteManager) EnsureServer(ctx context.Context, hostID, workspace string) (RemoteServerView, string, error) {
	c := m.client(hostID)
	if c == nil {
		return RemoteServerView{}, "", fmt.Errorf("host %q is not connected", hostID)
	}
	cfg, _ := config.Load()
	entry, _ := cfg.RemoteHost(hostID)
	m.emitServer(RemoteServerView{HostID: hostID, Workspace: workspace, State: "starting"})
	res, err := bootstrap.EnsureServe(ctx, c, bootstrap.Options{
		Workspace:   workspace,
		Install:     entry.ServeInstallMode(),
		LocalBinary: currentExecutablePath(),
		LocalGOOS:   runtime.GOOS,
		LocalGOARCH: runtime.GOARCH,
		MinVersion:  bootstrap.MinServeVersion,
		Progress: func(step, detail string) {
			m.emitServer(RemoteServerView{HostID: hostID, Workspace: workspace, State: step, Message: detail})
		},
	})
	if err != nil {
		view := RemoteServerView{HostID: hostID, Workspace: workspace, State: "error", Error: err.Error()}
		m.emitServer(view)
		return view, "", err
	}
	// Forward the serve port locally.
	bound, ferr := c.Forwards().Add(forward.Spec{
		Name: "serve", Direction: forward.Local, BindAddr: "127.0.0.1:0", TargetAddr: res.State.Addr,
	})
	if ferr != nil && !strings.Contains(ferr.Error(), "duplicate") {
		view := RemoteServerView{HostID: hostID, Workspace: workspace, State: "error", Error: ferr.Error()}
		m.emitServer(view)
		return view, "", ferr
	}
	if bound == "" {
		// Already had a serve forward; find its bound address.
		for _, e := range c.Forwards().List() {
			if e.Spec.Name == "serve" {
				bound = e.BoundAddr
			}
		}
	}
	localURL := fmt.Sprintf("http://%s/", bound)
	view := RemoteServerView{HostID: hostID, Workspace: workspace, State: "ready", LocalURL: localURL}
	m.mu.Lock()
	if mh := m.hosts[hostID]; mh != nil {
		mh.server = view
		mh.token = res.Token
	}
	m.mu.Unlock()
	m.emitServer(view)
	return view, res.Token, nil
}

func (m *desktopRemoteManager) StopServer(hostID string) error {
	c := m.client(hostID)
	if c == nil {
		return fmt.Errorf("host %q is not connected", hostID)
	}
	m.mu.Lock()
	ws := m.hosts[hostID].server.Workspace
	m.mu.Unlock()
	if err := bootstrap.Stop(context.Background(), c, ws); err != nil {
		return err
	}
	m.emitServer(RemoteServerView{HostID: hostID, Workspace: ws, State: "stopped"})
	return nil
}

func (m *desktopRemoteManager) ServerStatus(hostID string) RemoteServerView {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mh := m.hosts[hostID]; mh != nil {
		return mh.server
	}
	return RemoteServerView{HostID: hostID, State: "stopped"}
}

func (m *desktopRemoteManager) ServerLogs(ctx context.Context, hostID string, tailLines int) (string, error) {
	c := m.client(hostID)
	if c == nil {
		return "", fmt.Errorf("host %q is not connected", hostID)
	}
	m.mu.Lock()
	ws := m.hosts[hostID].server.Workspace
	m.mu.Unlock()
	var sb strings.Builder
	if err := bootstrap.Logs(ctx, c, ws, tailLines, &sb); err != nil {
		return "", err
	}
	return sb.String(), nil
}

func (m *desktopRemoteManager) emitServer(v RemoteServerView) {
	if m.sink != nil {
		m.sink.onServer(v)
	}
}

func (m *desktopRemoteManager) Close() error {
	m.mu.Lock()
	hosts := m.hosts
	m.hosts = map[string]*managedHost{}
	m.mu.Unlock()
	for _, mh := range hosts {
		if mh.fpAnswer != nil {
			select {
			case mh.fpAnswer <- false:
			default:
			}
		}
		if mh.cancel != nil {
			mh.cancel()
		}
		if mh.client != nil {
			_ = mh.client.Close()
		}
	}
	return nil
}

// ── helpers ──

func hostEntryToView(h config.RemoteHostEntry) RemoteHostView {
	return RemoteHostView{
		ID: h.Name, Label: h.Name, Host: h.Host, Port: h.Port, User: h.User,
		IdentityFile: h.IdentityFile, ProxyJump: h.ProxyJump,
		DefaultWorkspace: h.Workspace, ServeInstall: h.ServeInstallMode(), UseSSHConfig: h.UseSSHConfig,
	}
}

func inputToHostEntry(in RemoteHostInput) config.RemoteHostEntry {
	name := strings.TrimSpace(in.Label)
	return config.RemoteHostEntry{
		Name: name, Host: in.Host, Port: in.Port, User: in.User,
		IdentityFile: in.IdentityFile, ProxyJump: in.ProxyJump,
		Workspace: in.DefaultWorkspace, ServeInstall: in.ServeInstall, UseSSHConfig: in.UseSSHConfig,
	}
}

func forwardEntriesToViews(hostID string, entries []forward.Entry) []RemoteForwardView {
	out := make([]RemoteForwardView, 0, len(entries))
	for _, e := range entries {
		state := "active"
		if !e.Up {
			state = "error"
		}
		v := RemoteForwardView{
			ID: e.Spec.Name, HostID: hostID, Label: e.Spec.Name, State: state,
		}
		if e.LastErr != nil {
			v.Error = e.LastErr.Error()
		}
		out = append(out, v)
	}
	return out
}

func statusString(s remote.Status) string {
	switch s {
	case remote.StatusConnecting:
		return "connecting"
	case remote.StatusConnected:
		return "connected"
	case remote.StatusReconnecting:
		return "reconnecting"
	case remote.StatusDegraded:
		return "degraded"
	case remote.StatusStopped:
		return "stopped"
	default:
		return "stopped"
	}
}
