package main

// Local takeover of a session held by a same-machine serve process — the
// desktop half of the single-writer handoff protocol (internal/serve's
// /ownership, /handoff, /external/frames, /reclaim and /mirror-end).
//
// The scenario: a remote desktop connected to this machine over SSH and left a
// resident serve holding session leases. The user now sits at THIS machine and
// opens the project locally. The tab lands in lease_blocked; instead of the
// dead-end banner it can now take the session over: Serve releases the lease,
// this window rebuilds on it, and the remote tab keeps watching through the
// frame mirror. When the remote side reclaims, this tab demotes itself to a
// read-only spectator fed by the same mirror in reverse.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/remote/bootstrap"
	"reasonix/internal/store"
)

// SessionTakeoverView is what the confirmation dialog is built from.
type SessionTakeoverView struct {
	Available      bool   `json:"available"`
	Reason         string `json:"reason,omitempty"`
	SessionPath    string `json:"sessionPath,omitempty"`
	Holder         string `json:"holder,omitempty"` // serve | external | other | free
	RemoteAttached bool   `json:"remoteAttached"`
	Running        bool   `json:"running"`
	Mirrored       bool   `json:"mirrored"`
	HolderPID      int    `json:"holderPid,omitempty"`
	HolderHost     string `json:"holderHost,omitempty"`
}

// takeoverHandoffTimeout bounds the drain window for a wait-mode takeover.
const takeoverHandoffTimeout = 5 * time.Minute

// takeoverServeRecord is one resident serve discovered from this machine's
// remote state directory, reachable over loopback HTTP.
type takeoverServeRecord struct {
	slug  string
	state bootstrap.ServeState
	base  string
	token string
}

// discoverLocalTakeoverServes enumerates the serve state files under
// <Reasonix home>/remote. The bootstrap wrote them over SFTP; the takeover
// reads them locally because this machine is now where the user sits.
func discoverLocalTakeoverServes() []takeoverServeRecord {
	dir := config.RemoteStateDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []takeoverServeRecord
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "serve-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		state, err := bootstrap.UnmarshalState(data)
		if err != nil || state.PID <= 0 {
			continue
		}
		if !takeoverProcessAlive(state.PID) {
			// Stale state from a long-dead serve. Probing its dead port only
			// burns handshakes (and can trip client/proxy rate limits).
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(name, "serve-"), ".json")
		record := takeoverServeRecord{slug: slug, state: state}
		// The port file carries the real bound address; the state JSON is the
		// fallback for serves that predate it.
		addr := state.Addr
		if port, err := os.ReadFile(filepath.Join(dir, store.RemoteServePortName(slug))); err == nil {
			if trimmed := strings.TrimSpace(string(port)); trimmed != "" {
				addr = trimmed
			}
		}
		if addr == "" {
			continue
		}
		record.base = "http://" + addr
		if token, err := os.ReadFile(filepath.Join(dir, store.RemoteServeTokenName(slug))); err == nil {
			record.token = strings.TrimSpace(string(token))
		}
		if record.token == "" {
			continue
		}
		out = append(out, record)
	}
	return out
}

// takeoverProcessAlive reports whether a pid is a live process on this host.
// The takeover protocol only ever talks to serves that are actually running;
// everything else is stale state.
func takeoverProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 performs no delivery; EPERM still means the process exists,
	// only ESRCH/ErrProcessDone means it is gone.
	err = proc.Signal(os.Signal(syscall.Signal(0)))
	if errors.Is(err, os.ErrProcessDone) {
		return false
	}
	return err == nil || errors.Is(err, syscall.EPERM)
}

func takeoverClient(ctx context.Context, record takeoverServeRecord) (*http.Client, error) {
	client, err := newServeHTTPClient(record.base)
	if err != nil {
		return nil, err
	}
	if err := serveHandshake(ctx, client, record.base, record.token); err != nil {
		return nil, err
	}
	return client, nil
}

func takeoverOwnership(ctx context.Context, client *http.Client, base, sessionPath string) (SessionTakeoverView, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serveURL(base, "/ownership?session="+urlQueryEscape(sessionPath)), nil)
	if err != nil {
		return SessionTakeoverView{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return SessionTakeoverView{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return SessionTakeoverView{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return SessionTakeoverView{}, fmt.Errorf("serve /ownership: status %d", resp.StatusCode)
	}
	var view SessionTakeoverView
	if err := json.Unmarshal(body, &view); err != nil {
		return SessionTakeoverView{}, err
	}
	return view, nil
}

func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '~', r == '/', r == ':', r == '\\':
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}

// findTakeoverTarget scans resident serves for one holding (or mirroring) the
// session, and returns a ready-to-use client for it.
func (a *App) findTakeoverTarget(ctx context.Context, sessionPath string) (takeoverServeRecord, *http.Client, SessionTakeoverView, error) {
	records := discoverLocalTakeoverServes()
	// Handshake failures back off: a serve whose token file was rotated by a
	// later bootstrap would otherwise burn a failed login every probe.
	records = a.serveProbesFresh(records)
	var lastErr error
	for _, record := range records {
		client, err := takeoverClient(ctx, record)
		if err != nil {
			a.noteServeProbeFailure(record.base)
			lastErr = err
			continue
		}
		view, err := takeoverOwnership(ctx, client, record.base, sessionPath)
		if err != nil {
			lastErr = err
			continue
		}
		if view.Holder == "serve" || view.Mirrored {
			return record, client, view, nil
		}
	}
	if lastErr != nil {
		return takeoverServeRecord{}, nil, SessionTakeoverView{}, fmt.Errorf("no reachable local serve holds this session: %w", lastErr)
	}
	return takeoverServeRecord{}, nil, SessionTakeoverView{}, fmt.Errorf("no resident serve on this machine holds this session")
}

// QuerySessionTakeover reports whether the lease-blocked tab's session can be
// taken over from a local serve, plus the occupancy details the confirmation
// dialog shows (remote attached, turn running, holder identity).
func (a *App) QuerySessionTakeover(tabID string) (*SessionTakeoverView, error) {
	tab := a.tabByID(tabID)
	if tab == nil {
		return nil, fmt.Errorf("unknown tab")
	}
	path := strings.TrimSpace(tab.currentSessionPath())
	if path == "" {
		path = strings.TrimSpace(tab.SessionPath)
	}
	if path == "" {
		return &SessionTakeoverView{Available: false, Reason: "tab has no session"}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _, view, err := a.findTakeoverTarget(ctx, path)
	if err != nil {
		return &SessionTakeoverView{Available: false, Reason: err.Error(), SessionPath: path}, nil
	}
	view.Available = true
	view.SessionPath = path
	return &view, nil
}

// TakeoverSession performs the confirmed takeover: Serve releases the session
// (draining or cancelling its active turn per mode) and the tab's deferred
// rebuild picks the now-free lease up. mode is "wait" or "interrupt".
func (a *App) TakeoverSession(tabID, mode string) error {
	tab := a.tabByID(tabID)
	if tab == nil {
		return fmt.Errorf("unknown tab")
	}
	path := strings.TrimSpace(tab.currentSessionPath())
	if path == "" {
		path = strings.TrimSpace(tab.SessionPath)
	}
	if path == "" {
		return fmt.Errorf("tab has no session")
	}
	if mode != "wait" && mode != "interrupt" {
		mode = "wait"
	}
	ctx, cancel := context.WithTimeout(context.Background(), takeoverHandoffTimeout+30*time.Second)
	defer cancel()
	record, client, view, err := a.findTakeoverTarget(ctx, path)
	if err != nil {
		return err
	}
	if !view.Mirrored && view.Holder != "serve" {
		return fmt.Errorf("serve no longer holds this session (%s)", view.Holder)
	}
	body, err := json.Marshal(map[string]any{
		"sessionPath": path,
		"force":       true,
		"mode":        mode,
		"timeoutMs":   takeoverHandoffTimeout.Milliseconds(),
	})
	if err != nil {
		return err
	}
	resp, err := serveDo(ctx, client, http.MethodPost, serveURL(record.base, "/handoff"), body)
	if err != nil {
		return fmt.Errorf("takeover handoff: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("takeover handoff: %s", strings.TrimSpace(string(respBody)))
	}

	// Remember the mirror target so the rebuilt tab's sink starts forwarding
	// frames, then wake the deferred rebuild loop instead of waiting out its
	// 2s poll.
	key := sessionRuntimeKey(path)
	a.registerTakeoverMirror(key, tabID, path, record, client)
	a.kickDeferredRebuildRetry()
	return nil
}

// takeoverMirror forwards one local tab's events to the serve that used to own
// the session, so the remote tab keeps rendering, and watches the heartbeat
// responses for a reclaim request. One mirror per session key.
type takeoverMirror struct {
	app         *App
	key         string
	sessionPath string

	mu        sync.Mutex
	tabID     string
	sink      *tabEventSink
	client    *http.Client
	record    takeoverServeRecord
	queue     []eventwire.Event
	lastFlush time.Time

	reclaimRequested    atomic.Bool
	demoted             atomic.Bool
	stopping            atomic.Bool
	consecutiveFailures int32
	stop                chan struct{}
	done                chan struct{}
}

const (
	takeoverMirrorMaxQueue   = 512
	takeoverMirrorFlushEvery = 120 * time.Millisecond
	takeoverMirrorHeartbeat  = 5 * time.Second
)

// adoptSessionFromLocalServe announces a directly-opened local session to the
// resident serve on this machine. Without a handoff there is no mirrored
// entry, so the remote side could neither watch it live nor reclaim it — it
// only saw the raw 409 lease refusal from serve's /resume. Adoption registers
// the mirror (and its frame forwarder), which restores both: the remote tab
// can spectator-attach, and its take-back works through /reclaim.
//
// Skipped when the serve itself holds the session (the /handoff takeover
// flow governs), when the serve is unreachable, or when the session is
// already mirrored by another writer.

// serveProbeBackoffWindow suppresses probing a serve whose handshake failed
// recently - typically a token file rotated by a later bootstrap - so a
// polling loop cannot hammer it with failed logins.
const serveProbeBackoffWindow = 60 * time.Second

func (a *App) serveProbesFresh(records []takeoverServeRecord) []takeoverServeRecord {
	a.serveProbeMu.Lock()
	defer a.serveProbeMu.Unlock()
	if a.serveProbeUntil == nil {
		return records
	}
	now := time.Now()
	out := records[:0]
	for _, record := range records {
		if until := a.serveProbeUntil[record.base]; until.After(now) {
			continue
		}
		out = append(out, record)
	}
	return out
}

func (a *App) noteServeProbeFailure(base string) {
	a.serveProbeMu.Lock()
	if a.serveProbeUntil == nil {
		a.serveProbeUntil = map[string]time.Time{}
	}
	a.serveProbeUntil[base] = time.Now().Add(serveProbeBackoffWindow)
	a.serveProbeMu.Unlock()
}

func (a *App) adoptSessionFromLocalServe(tabID, sessionPath string) {
	if a.adoptSessionFromLocalServeOnce(tabID, sessionPath) {
		return
	}
	// The announce can race a serve restart (token rotation, probe backoff)
	// or transient discovery failures. Dropping it silently leaves a foreign
	// lease the remote side can see but never reclaim cooperatively; retry
	// once after the backoff window while the tab still shows the session.
	time.AfterFunc(serveProbeBackoffWindow, func() {
		if tab := a.tabByID(tabID); tab != nil && !tab.ReadOnly &&
			strings.TrimSpace(tab.currentSessionPath()) == strings.TrimSpace(sessionPath) {
			a.adoptSessionFromLocalServeOnce(tabID, sessionPath)
		}
	})
}

// adoptSessionFromLocalServeOnce announces a directly-opened local session to
// a resident serve. It reports false when no serve could be told, so the
// caller can retry.
func (a *App) adoptSessionFromLocalServeOnce(tabID, sessionPath string) bool {
	key := sessionRuntimeKey(sessionPath)
	if key == "" {
		return true
	}
	a.takeoverMu.Lock()
	exists := a.takeoverMirrors[key] != nil
	a.takeoverMu.Unlock()
	if exists {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	serves := discoverLocalTakeoverServes()
	for _, serve := range serves {
		workspaceDir := config.ProjectSessionDir(serve.state.Workspace)
		if workspaceDir == "" || !pathWithinDir(sessionPath, workspaceDir) {
			continue
		}
		client, err := takeoverClient(ctx, serve)
		if err != nil {
			continue
		}
		view, err := takeoverOwnership(ctx, client, serve.base, sessionPath)
		if err != nil {
			continue
		}
		switch view.Holder {
		case "serve":
			// Serve owns it; the handoff takeover flow applies instead.
			return true
		case "external":
			// Another local runtime already owns and mirrors it.
			return true
		}
		body, err := json.Marshal(map[string]string{"sessionPath": sessionPath})
		if err != nil {
			continue
		}
		resp, err := serveDo(ctx, client, http.MethodPost, serveURL(serve.base, "/adopt"), body)
		if err != nil {
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			continue
		}
		a.registerTakeoverMirror(key, tabID, sessionPath, serve, client)
		slog.Info("desktop: local session adopted by serve for remote spectating",
			"tab", tabID, "session", sessionPath, "serve", serve.base)
		return true
	}
	return false
}

// pathWithinDir reports whether child is inside dir (canonical path prefix).
func pathWithinDir(child, dir string) bool {
	child = strings.TrimRight(filepath.Clean(child), string(filepath.Separator)) + string(filepath.Separator)
	dir = strings.TrimRight(filepath.Clean(dir), string(filepath.Separator)) + string(filepath.Separator)
	return strings.HasPrefix(strings.ToLower(child), strings.ToLower(dir))
}

func (a *App) registerTakeoverMirror(key, tabID, sessionPath string, record takeoverServeRecord, client *http.Client) {
	if key == "" {
		return
	}
	a.takeoverMu.Lock()
	if a.takeoverMirrors == nil {
		a.takeoverMirrors = map[string]*takeoverMirror{}
	}
	m := a.takeoverMirrors[key]
	if m == nil {
		m = &takeoverMirror{
			app:         a,
			key:         key,
			sessionPath: sessionPath,
			stop:        make(chan struct{}),
			done:        make(chan struct{}),
		}
		a.takeoverMirrors[key] = m
		go m.run(client, record)
	}
	m.mu.Lock()
	m.tabID = tabID
	m.record = record
	m.client = client
	m.mu.Unlock()
	a.takeoverMu.Unlock()
	a.attachTakeoverMirror(tabID, sessionPath)
}

// attachTakeoverMirror points the tab's current sink at its mirror, if one is
// registered for the session. Called after every successful (re)bind so a
// deferred rebuild or a later session switch keeps the wiring true.
func (a *App) attachTakeoverMirror(tabID, sessionPath string) {
	key := sessionRuntimeKey(sessionPath)
	if key == "" {
		return
	}
	a.takeoverMu.Lock()
	m := a.takeoverMirrors[key]
	a.takeoverMu.Unlock()
	if m == nil {
		return
	}
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	var sink *tabEventSink
	if tab != nil {
		sink = tab.sink
	}
	a.mu.RUnlock()
	if tab == nil || sink == nil {
		return
	}
	m.mu.Lock()
	m.tabID = tabID
	m.sink = sink
	m.mu.Unlock()
	sink.setTakeoverMirror(m)
}

func (a *App) takeoverMirrorForKey(key string) *takeoverMirror {
	if key == "" {
		return nil
	}
	a.takeoverMu.Lock()
	defer a.takeoverMu.Unlock()
	return a.takeoverMirrors[key]
}

// stopTakeoverMirrors halts every mirror's forwarding loop. The registry
// entries stay: endTakeoverMirrors still needs them after controller teardown
// has released the leases to tell Serve the writers are gone.
func (a *App) stopTakeoverMirrors() {
	for _, m := range a.snapshotTakeoverMirrors() {
		m.stopLoop()
	}
}

// endTakeoverMirrors is the shutdown epilogue: every lease is released by now,
// so Serve's mirror-end immediately hands the sessions back to their remote
// tabs instead of waiting out the stale-mirror timeout.
func (a *App) endTakeoverMirrors() {
	for _, m := range a.snapshotTakeoverMirrors() {
		m.mirrorEnd()
		m.detach()
	}
}

func (a *App) snapshotTakeoverMirrors() []*takeoverMirror {
	a.takeoverMu.Lock()
	mirrors := make([]*takeoverMirror, 0, len(a.takeoverMirrors))
	for _, m := range a.takeoverMirrors {
		mirrors = append(mirrors, m)
	}
	a.takeoverMu.Unlock()
	return mirrors
}

// forwardEvent enqueues one local event for the mirror. Called from the tab
// sink's Emit; must never block the agent loop.
func (m *takeoverMirror) forwardEvent(e event.Event) {
	if m == nil {
		return
	}
	wired := eventwire.ToWire(e)
	m.mu.Lock()
	if len(m.queue) >= takeoverMirrorMaxQueue {
		// The remote refetches /history to reconcile; dropping the oldest
		// delta is the same lossy contract a slow SSE subscriber already has.
		copy(m.queue, m.queue[1:])
		m.queue = m.queue[:len(m.queue)-1]
	}
	m.queue = append(m.queue, wired)
	m.mu.Unlock()
}

func (m *takeoverMirror) snapshotClient() (*http.Client, takeoverServeRecord, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client, m.record, m.tabID
}

func (m *takeoverMirror) run(initialClient *http.Client, initialRecord takeoverServeRecord) {
	defer close(m.done)
	m.mu.Lock()
	if m.client == nil {
		m.client = initialClient
		m.record = initialRecord
	}
	m.mu.Unlock()

	ticker := time.NewTicker(takeoverMirrorFlushEvery)
	defer ticker.Stop()
	var lastPush time.Time
	for {
		select {
		case <-m.stop:
			m.flushOnce(context.Background())
			return
		case <-ticker.C:
		}
		if m.stopping.Load() {
			return
		}
		client, record, tabID := m.snapshotClient()
		if client == nil {
			continue
		}
		// Self-heal the sink wiring: rebuilds and tab switches rebind sinks
		// without takeover knowledge.
		m.app.attachTakeoverMirror(tabID, m.sessionPath)

		due := len(m.drainQueue()) > 0 || time.Since(lastPush) >= takeoverMirrorHeartbeat
		if !due {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		frames := m.drainQueue()
		payload, err := json.Marshal(map[string]any{
			"sessionPath": m.sessionPath,
			"frames":      frames,
		})
		if err != nil {
			cancel()
			continue
		}
		resp, err := serveDo(ctx, client, http.MethodPost, serveURL(record.base, "/external/frames"), payload)
		if err != nil {
			// Mirror transport failed; push the frames back so the next
			// heartbeat retries them, and keep serving the local session.
			cancel()
			m.requeue(frames)
			lastPush = time.Now()
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		cancel()
		lastPush = time.Now()
		switch resp.StatusCode {
		case http.StatusOK:
			m.consecutiveFailures = 0
			var out struct {
				ReclaimRequested bool   `json:"reclaimRequested"`
				ReclaimMode      string `json:"reclaimMode"`
			}
			if json.Unmarshal(body, &out) == nil && out.ReclaimRequested {
				m.requestDemote(out.ReclaimMode)
			}
		case http.StatusConflict:
			// Serve no longer knows this mirror: either the session was
			// reclaimed (serve re-owns it) or serve restarted (all
			// in-memory mirror state lost). Try re-adopting with fresh
			// credentials; if the serve already owns the session,
			// demote to release the lease.
			slog.Info("desktop: mirror 409 — attempting re-adopt or demote",
				"session", m.sessionPath)
			if m.retryAdoptOrDemote() {
				continue // re-adopted with fresh credentials
			}
			return // demoted (lease released) or fatal
		default:
			// Auth failure (401 from a rotated token) or transport glitch.
			// After consecutive failures, re-adopt with fresh credentials;
			// the stale client's cookie/token is permanently invalid after
			// a serve restart.
			m.consecutiveFailures++
			if m.consecutiveFailures >= 3 {
				slog.Warn("desktop: mirror heartbeat failing — re-adopting",
					"session", m.sessionPath, "status", resp.StatusCode,
					"failures", m.consecutiveFailures)
				m.consecutiveFailures = 0
				if m.retryAdoptOrDemote() {
					continue
				}
				return
			}
		}
		// A mirror whose tab is gone entirely (closed, not detached) ends
		// itself so Serve can hand the session back to the remote side.
		if !m.app.takeoverTabLive(m.sessionPath) {
			m.shutdown(true)
			return
		}
	}
}

// retryAdoptOrDemote attempts to re-establish the mirror with fresh serve
// credentials (the serve may have restarted with a new token). If the serve
// already owns the session (reclaim completed or another writer took over),
// demotes this tab to read-only and releases the lease so the remote side
// can proceed. Returns true if re-adopted (caller continues the loop).
func (m *takeoverMirror) retryAdoptOrDemote() bool {
	records := discoverLocalTakeoverServes()
	for _, record := range records {
		if !pathWithinDir(m.sessionPath, config.ProjectSessionDir(record.state.Workspace)) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		client, err := takeoverClient(ctx, record)
		if err != nil {
			cancel()
			continue
		}
		body, bodyErr := json.Marshal(map[string]string{"sessionPath": m.sessionPath})
		if bodyErr != nil {
			cancel()
			continue
		}
		resp, respErr := serveDo(ctx, client, http.MethodPost, serveURL(record.base, "/adopt"), body)
		cancel()
		if respErr != nil {
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			// Re-adopted: swap in the fresh client and keep mirroring.
			m.mu.Lock()
			m.client = client
			m.record = record
			m.mu.Unlock()
			slog.Info("desktop: mirror re-adopted with fresh credentials",
				"session", m.sessionPath, "base", record.base)
			return true
		}
		if resp.StatusCode == http.StatusConflict {
			// Serve holds this session (string in body mentions handoff) —
			// the remote side re-owns it. Demote to release the lease.
			slog.Info("desktop: serve holds session — demoting to release lease",
				"session", m.sessionPath, "status", resp.StatusCode)
			m.demote(false)
			return false
		}
		// Other statuses: try next record.
	}
	// No serve accepted the re-adopt. If the tab is still live, keep the
	// local session but stop the forwarder (remote can't see content).
	slog.Warn("desktop: mirror re-adopt failed on all serves — stopping forwarder",
		"session", m.sessionPath)
	m.shutdown(false)
	return false
}

func (m *takeoverMirror) drainQueue() []eventwire.Event {
	m.mu.Lock()
	frames := m.queue
	m.queue = nil
	m.mu.Unlock()
	return frames
}

func (m *takeoverMirror) requeue(frames []eventwire.Event) {
	if len(frames) == 0 {
		return
	}
	m.mu.Lock()
	m.queue = append(frames, m.queue...)
	if len(m.queue) > takeoverMirrorMaxQueue {
		m.queue = m.queue[:takeoverMirrorMaxQueue]
	}
	m.mu.Unlock()
}

func (m *takeoverMirror) flushOnce(ctx context.Context) {
	client, record, _ := m.snapshotClient()
	if client == nil {
		return
	}
	frames := m.drainQueue()
	if len(frames) == 0 {
		return
	}
	payload, err := json.Marshal(map[string]any{"sessionPath": m.sessionPath, "frames": frames})
	if err != nil {
		return
	}
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := serveDo(flushCtx, client, http.MethodPost, serveURL(record.base, "/external/frames"), payload)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// takeoverTabLive reports whether the mirrored session still has a live
// desktop runtime (visible tab or detached background runtime).
func (a *App) takeoverTabLive(sessionPath string) bool {
	return a.sessionParentLive(sessionPath)
}

// tabHoldingSession returns the live tab currently owning the session path,
// using the same liveness notion as sessionParentLive. Mirrors outlive tab
// rebuilds, so lease-affecting actions must resolve the tab through the
// session rather than a snapshotted tab ID.
func (a *App) tabHoldingSession(sessionPath string) *WorkspaceTab {
	key := sessionRuntimeKey(sessionPath)
	if a == nil || key == "" {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	holding := func(tab *WorkspaceTab) bool {
		if tab == nil {
			return false
		}
		if sessionRuntimeKey(tab.SessionPath) == key {
			return true
		}
		return tab.Ctrl != nil && sessionRuntimeKey(tab.Ctrl.SessionPath()) == key
	}
	for _, tab := range a.tabs {
		if holding(tab) {
			return tab
		}
	}
	for _, tab := range a.detachedSessions {
		if holding(tab) {
			return tab
		}
	}
	return nil
}

// requestDemote reacts to a remote reclaim: this tab loses speaking rights.
// The demotion itself is passive — flip the tab read-only, release the lease,
// tell the user, and let Serve resume ownership.
func (m *takeoverMirror) requestDemote(mode string) {
	if !m.reclaimRequested.CompareAndSwap(false, true) {
		return
	}
	m.app.emitTakeoverNotice(m, event.LevelWarn, "session_reclaim_requested",
		"The remote side is taking this session back; this window is now read-only.")
	go m.demote(mode == string(handoffModeInterruptLocal))
}

const handoffModeInterruptLocal = "interrupt"

func (m *takeoverMirror) demote(interrupt bool) {
	a := m.app
	tab := a.tabByID(m.tabIDSnapshot())
	if tab == nil {
		// Tab IDs rotate on rebuilds and app restarts while a reclaim is in
		// flight; the session path is the stable handle. Without this
		// fallback the demote skipped the read-only flip and the lease
		// release, and the remote reclaim waited on a writer that had
		// already forgotten it was one.
		tab = a.tabHoldingSession(m.sessionPath)
	}
	var sink *tabEventSink
	if tab != nil {
		a.mu.RLock()
		sink = tab.sink
		a.mu.RUnlock()
		// Block new submits at the UI level first, then drop the lease so
		// Serve's reclaim can complete. The controller stays open but inert;
		// with the tab read-only no turn can be admitted.
		a.setTabReadOnly(tab.ID, true)
		tab.releaseSessionLeaseForKey(m.key)
	}
	m.emitNoticeSink(sink, event.LevelWarn, "session_taken_over_local",
		"This session was taken back by the remote side. This window is a read-only spectator.")
	if interrupt && tab != nil && tab.Ctrl != nil {
		// The remote asked to cancel an in-flight local turn; with the tab
		// read-only the admission gate refuses new input, cancel what is
		// already running so the drain completes quickly.
		tab.Ctrl.Cancel()
	}
	m.shutdown(false)
	m.mirrorEnd()
	m.startSpectate(tab, sink)
}

// emitTakeoverNotice surfaces a takeover lifecycle change as a notice frame on
// the tab's event channel (best effort; the sink may not exist yet).
func (a *App) emitTakeoverNotice(m *takeoverMirror, level event.Level, code, text string) {
	m.mu.Lock()
	sink := m.sink
	m.mu.Unlock()
	m.emitNoticeSink(sink, level, code, text)
}

func (m *takeoverMirror) emitNoticeSink(sink *tabEventSink, level event.Level, code, text string) {
	if sink == nil {
		return
	}
	tabID, _ := sink.binding()
	e := event.Event{Kind: event.Notice, Level: level, Code: code, Text: text, SessionPath: m.sessionPath}
	sink.emitRuntimeEvent(eventChannel, toWireTabWithSubmission(e, tabID, sink.runtimeEpochSnapshot(), "", 0))
}

// mirrorEnd tells Serve the writer is gone so the remote side resumes without
// waiting for the stale-mirror timeout. Best effort.
func (m *takeoverMirror) mirrorEnd() {
	client, record, _ := m.snapshotClient()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]string{"sessionPath": m.sessionPath})
	resp, err := serveDo(ctx, client, http.MethodPost, serveURL(record.base, "/mirror-end"), payload)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// stopLoop halts the forwarding goroutine. Idempotent.
func (m *takeoverMirror) stopLoop() {
	if !m.stopping.CompareAndSwap(false, true) {
		return
	}
	close(m.stop)
	<-m.done
}

// detach removes the mirror from the app registry and clears the sink hook.
func (m *takeoverMirror) detach() {
	m.app.takeoverMu.Lock()
	if m.app.takeoverMirrors[m.key] == m {
		delete(m.app.takeoverMirrors, m.key)
	}
	m.app.takeoverMu.Unlock()
	if sink := m.currentSink(); sink != nil {
		sink.setTakeoverMirror(nil)
	}
}

// shutdown stops the forwarding loop and deregisters the mirror; when
// notifyServe is set (the mirror ended without a demotion, e.g. its tab
// closed) Serve is told the writer is gone once it can accept that.
func (m *takeoverMirror) shutdown(notifyServe bool) {
	m.stopLoop()
	m.detach()
	if notifyServe {
		m.mirrorEnd()
	}
}

func (m *takeoverMirror) currentSink() *tabEventSink {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sink
}

// startSpectate keeps the demoted tab rendering by streaming Serve's frames
// (the remote side is the writer again) into the tab's event channel. The
// frames are the same wire contract the local reducer already consumes.
func (m *takeoverMirror) startSpectate(tab *WorkspaceTab, sink *tabEventSink) {
	client, record, _ := m.snapshotClient()
	if tab == nil || sink == nil || client == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithCancel(m.app.ctx)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, serveURL(record.base, "/events?all=1"), nil)
		if err != nil {
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return
		}
		canonical := agent.CanonicalSessionPath(m.sessionPath)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for scanner.Scan() {
			select {
			case <-m.app.ctx.Done():
				return
			default:
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var frame eventwire.Event
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame) != nil {
				continue
			}
			if frame.SessionPath != canonical {
				continue
			}
			sink.emitRuntimeEvent(eventChannel, wireEventTab{Event: frame, TabID: m.tabIDSnapshot()})
		}
	}()
}

func (m *takeoverMirror) tabIDSnapshot() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tabID
}
