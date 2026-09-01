package cli

// Local takeover for the CLI resume paths: when the session lease is held by a
// resident serve process on this machine (left behind by a remote desktop that
// connected over SSH), the user can take the session over instead of exiting
// with a refusal. Serve releases the lease via POST /handoff; the remote tab
// keeps watching read-only through the frame mirror.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/remote/bootstrap"
	"reasonix/internal/store"
)

// cliTakeoverTimeout bounds the drain window of a wait-mode takeover.
const cliTakeoverTimeout = 2 * time.Minute

type cliServeRecord struct {
	pid   int
	base  string
	token string
}

type cliTakeoverGrant struct {
	SessionPath     string `json:"sessionPath"`
	MirrorID        string `json:"mirrorId"`
	HandoffID       string `json:"handoffId,omitempty"`
	ReturnHandoffID string `json:"returnHandoffId"`
	SourceWriterID  string `json:"sourceWriterId"`
	TargetWriterID  string `json:"targetWriterId"`
}

type cliTakeoverBinding struct {
	path        string
	record      cliServeRecord
	client      *http.Client
	grant       cliTakeoverGrant
	previous    *control.SessionLeaseKeeper
	priorMirror *cliTakeoverBinding
}

// discoverCLIServes enumerates resident serve processes recorded under
// <Reasonix home>/remote. This machine is the SSH target in the takeover
// scenario, so the bootstrap's SFTP-written state files are local files here.
func discoverCLIServes() []cliServeRecord {
	dir := config.RemoteStateDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []cliServeRecord
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
		slug := strings.TrimSuffix(strings.TrimPrefix(name, "serve-"), ".json")
		addr := state.Addr
		if port, err := os.ReadFile(filepath.Join(dir, store.RemoteServePortName(slug))); err == nil {
			if trimmed := strings.TrimSpace(string(port)); trimmed != "" {
				addr = trimmed
			}
		}
		if addr == "" {
			continue
		}
		token := ""
		if data, err := os.ReadFile(filepath.Join(dir, store.RemoteServeTokenName(slug))); err == nil {
			token = strings.TrimSpace(string(data))
		}
		if token == "" {
			continue
		}
		out = append(out, cliServeRecord{pid: state.PID, base: "http://" + addr, token: token})
	}
	return out
}

// cliServeForPID finds the resident serve holding the lease by matching the
// holder PID the lease error reported.
func cliServeForPID(pid int) *cliServeRecord {
	records := discoverCLIServes()
	for i := range records {
		if records[i].pid == pid {
			return &records[i]
		}
	}
	return nil
}

func cliServeClient(ctx context.Context, record cliServeRecord) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar}
	auth, _ := json.Marshal(map[string]string{"token": record.token})
	authReq, err := http.NewRequestWithContext(ctx, http.MethodPost, record.base+"/auth/token", bytes.NewReader(auth))
	if err != nil {
		return nil, err
	}
	authReq.Header.Set("Content-Type", "application/json")
	authResp, err := client.Do(authReq)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, authResp.Body)
	authResp.Body.Close()
	if authResp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("serve auth: status %d", authResp.StatusCode)
	}
	return client, nil
}

func cliServeRequest(ctx context.Context, record cliServeRecord, method, path string, body []byte) (*http.Response, error) {
	client, err := cliServeClient(ctx, record)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, record.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}

// cliTakeoverHeldSession requests a target-writer reservation and consumes it
// through leases. The previous keeper binding is retained if either step
// fails; callers commit their controller only after this returns a binding.
func cliTakeoverHeldSession(sessionPath string, leaseErr error, leases *control.SessionLeaseKeeper, manager *cliTakeoverManager) (*cliTakeoverBinding, error) {
	if manager != nil && manager.Reclaiming() {
		return nil, fmt.Errorf("the remote side is reclaiming the current session")
	}
	pid := 0
	var leaseError *agent.SessionLeaseError
	if errors.As(leaseErr, &leaseError) && leaseError != nil && leaseError.Info != nil {
		pid = leaseError.Info.PID
	}
	if pid <= 0 {
		return nil, fmt.Errorf("%w; no local serve identity to take over from", agent.ErrSessionLeaseHeld)
	}
	record := cliServeForPID(pid)
	if record == nil {
		return nil, fmt.Errorf("%w; holder pid %d is not a resident serve on this machine", agent.ErrSessionLeaseHeld, pid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cliTakeoverTimeout+15*time.Second)
	defer cancel()
	client, err := cliServeClient(ctx, *record)
	if err != nil {
		return nil, fmt.Errorf("takeover from local serve (pid %d): %w", pid, err)
	}
	body, _ := json.Marshal(map[string]any{
		"sessionPath": sessionPath, "targetWriterId": agent.SessionWriterID(),
		"force": true, "mode": "wait", "timeoutMs": cliTakeoverTimeout.Milliseconds(),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, record.base+"/handoff", bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("takeover from local serve (pid %d): %w", pid, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("takeover from local serve (pid %d): %s", pid, strings.TrimSpace(string(respBody)))
	}
	var grant cliTakeoverGrant
	if json.Unmarshal(respBody, &grant) != nil || grant.MirrorID == "" || grant.HandoffID == "" ||
		grant.ReturnHandoffID == "" || grant.SourceWriterID == "" || grant.TargetWriterID != agent.SessionWriterID() {
		return nil, fmt.Errorf("takeover from local serve (pid %d): invalid handoff grant", pid)
	}
	binding := &cliTakeoverBinding{path: sessionPath, record: *record, client: client, grant: grant}
	if manager != nil {
		current, _, _ := manager.snapshot()
		if current != nil && !manager.Returned() && agent.CanonicalSessionPath(current.path) != agent.CanonicalSessionPath(sessionPath) {
			binding.priorMirror = current
		}
	}
	previous, err := leases.RebindDetachingWithHandoff(sessionPath, grant.SourceWriterID, grant.HandoffID)
	if err != nil {
		cliEndFailedHandoff(binding)
		return nil, err
	}
	binding.previous = previous
	return binding, nil
}

// cliSessionTakeoverCandidate reports whether leaseErr points at a resident
// serve on this machine — the case where a takeover offer makes sense.
func cliSessionTakeoverCandidate(leaseErr error) bool {
	var leaseError *agent.SessionLeaseError
	if !errors.As(leaseErr, &leaseError) || leaseError == nil || leaseError.Info == nil {
		return false
	}
	return cliServeForPID(leaseError.Info.PID) != nil
}

// promptSessionTakeover asks on the terminal (pre-TUI startup) whether to take
// the held session over. Non-interactive sessions answer no.
func promptSessionTakeover(leaseErr error) bool {
	if !isInteractive() {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s\n", sessionLeaseResumeRefusal(leaseErr))
	fmt.Fprint(os.Stderr, "take over the session from this machine's resident serve? [y/N] ")
	answer, err := readCLITakeoverAnswer()
	if err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func readCLITakeoverAnswer() (string, error) {
	buf := make([]byte, 64)
	n, err := os.Stdin.Read(buf)
	if n > 0 {
		return string(buf[:n]), nil
	}
	return "", err
}

const (
	cliTakeoverFlushEvery = 120 * time.Millisecond
	cliTakeoverHeartbeat  = 5 * time.Second
	cliTakeoverMaxFrames  = 512
)

// cliTakeoverManager is the outermost CLI event sink while a handed-off
// session is active. It preserves the terminal sink, mirrors the same typed
// frames to Serve, and cooperatively returns the lease when reclaim is seen.
// One manager survives controller rebuilds; AttachController updates its live
// authority pointer without replacing the sink wired into boot.
type cliTakeoverManager struct {
	event.AuditForwarder
	inner  event.Sink
	leases *control.SessionLeaseKeeper

	mu       sync.Mutex
	binding  *cliTakeoverBinding
	ctrl     control.SessionAPI
	queue    []eventwire.Event
	wake     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	onYield  func()
	returnMu sync.Mutex

	started    bool
	stopOnce   sync.Once
	reclaiming atomic.Bool
	returned   atomic.Bool
}

func newCLITakeoverManager(inner event.Sink, leases *control.SessionLeaseKeeper) *cliTakeoverManager {
	return &cliTakeoverManager{AuditForwarder: event.AuditForwarder{Inner: inner}, inner: inner, leases: leases}
}

func (m *cliTakeoverManager) Emit(e event.Event) {
	if m == nil {
		return
	}
	if m.inner != nil {
		m.inner.Emit(e)
	}
	m.mu.Lock()
	if m.binding != nil && !m.returned.Load() {
		m.queue = append(m.queue, eventwire.ToWire(e))
	}
	wake := m.wake
	m.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (m *cliTakeoverManager) EmitChecked(e event.Event) error {
	if checked, ok := m.inner.(event.CheckedSink); ok {
		if err := checked.EmitChecked(e); err != nil {
			return err
		}
	} else if m.inner != nil {
		m.inner.Emit(e)
	}
	m.mu.Lock()
	if m.binding != nil && !m.returned.Load() {
		m.queue = append(m.queue, eventwire.ToWire(e))
	}
	wake := m.wake
	m.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (m *cliTakeoverManager) AttachController(ctrl control.SessionAPI) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.ctrl = ctrl
	m.mu.Unlock()
}

func (m *cliTakeoverManager) Activate(binding *cliTakeoverBinding) {
	if m == nil || binding == nil {
		return
	}
	m.mu.Lock()
	m.binding = binding
	if !m.started {
		m.started = true
		m.wake = make(chan struct{}, 1)
		m.stop = make(chan struct{})
		m.done = make(chan struct{})
		go m.run()
	}
	m.mu.Unlock()
}

func (m *cliTakeoverManager) SetYieldCallback(fn func()) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.onYield = fn
	m.mu.Unlock()
}

func (m *cliTakeoverManager) Reclaiming() bool { return m != nil && m.reclaiming.Load() }
func (m *cliTakeoverManager) Returned() bool   { return m != nil && m.returned.Load() }

func (m *cliTakeoverManager) snapshot() (*cliTakeoverBinding, control.SessionAPI, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.binding, m.ctrl, m.onYield
}

func (m *cliTakeoverManager) run() {
	m.mu.Lock()
	wake, stop, done := m.wake, m.stop, m.done
	m.mu.Unlock()
	defer close(done)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	heartbeat := time.NewTicker(cliTakeoverHeartbeat)
	defer heartbeat.Stop()
	armed := false
	for {
		select {
		case <-stop:
			m.push(false)
			return
		case <-wake:
			if !armed {
				timer.Reset(cliTakeoverFlushEvery)
				armed = true
			}
		case <-timer.C:
			armed = false
			if !m.push(false) {
				return
			}
		case <-heartbeat.C:
			if !m.push(true) {
				return
			}
		}
	}
}

func (m *cliTakeoverManager) drain() []eventwire.Event {
	m.mu.Lock()
	count := min(len(m.queue), cliTakeoverMaxFrames)
	frames := append([]eventwire.Event(nil), m.queue[:count]...)
	m.queue = m.queue[count:]
	m.mu.Unlock()
	return frames
}

func (m *cliTakeoverManager) requeue(frames []eventwire.Event) {
	if len(frames) == 0 {
		return
	}
	m.mu.Lock()
	m.queue = append(frames, m.queue...)
	m.mu.Unlock()
}

func (m *cliTakeoverManager) wakeIfQueued() {
	m.mu.Lock()
	pending, wake := len(m.queue) > 0, m.wake
	m.mu.Unlock()
	if pending && wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (m *cliTakeoverManager) push(heartbeat bool) bool {
	if m.returned.Load() {
		return false
	}
	binding, _, _ := m.snapshot()
	if binding == nil || binding.client == nil || binding.grant.MirrorID == "" {
		return true
	}
	frames := m.drain()
	if len(frames) == 0 && !heartbeat {
		return true
	}
	payload, _ := json.Marshal(map[string]any{
		"sessionPath": binding.path, "mirrorId": binding.grant.MirrorID, "frames": frames,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, binding.record.base+"/external/frames", bytes.NewReader(payload))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
	}
	var resp *http.Response
	if err == nil {
		resp, err = binding.client.Do(req)
	}
	if err != nil {
		cancel()
		m.requeue(frames)
		return true
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	cancel()
	if resp.StatusCode == http.StatusConflict {
		m.requeue(frames)
		return m.readopt()
	}
	if resp.StatusCode != http.StatusOK {
		m.requeue(frames)
		return true
	}
	var out struct {
		ReclaimRequested bool   `json:"reclaimRequested"`
		ReclaimMode      string `json:"reclaimMode"`
	}
	if json.Unmarshal(body, &out) == nil && out.ReclaimRequested {
		m.requestYield(out.ReclaimMode == "interrupt")
		return true
	}
	m.wakeIfQueued()
	return true
}

func (m *cliTakeoverManager) readopt() bool {
	binding, _, _ := m.snapshot()
	if binding == nil {
		return false
	}
	for _, record := range discoverCLIServes() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		client, err := cliServeClient(ctx, record)
		if err != nil {
			cancel()
			continue
		}
		payload, _ := json.Marshal(map[string]string{"sessionPath": binding.path, "writerId": agent.SessionWriterID()})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, record.base+"/adopt", bytes.NewReader(payload))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
		var resp *http.Response
		if err == nil {
			resp, err = client.Do(req)
		}
		if err != nil {
			cancel()
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		cancel()
		if resp.StatusCode == http.StatusConflict {
			m.requestYield(false)
			return false
		}
		var grant cliTakeoverGrant
		if resp.StatusCode == http.StatusOK && json.Unmarshal(body, &grant) == nil && grant.MirrorID != "" && grant.ReturnHandoffID != "" {
			m.mu.Lock()
			if m.binding == binding {
				m.binding = &cliTakeoverBinding{path: binding.path, record: record, client: client, grant: grant}
			}
			m.mu.Unlock()
			return true
		}
	}
	return true
}

func (m *cliTakeoverManager) requestYield(interrupt bool) {
	if !m.reclaiming.CompareAndSwap(false, true) {
		return
	}
	_, ctrl, callback := m.snapshot()
	if interrupt && ctrl != nil {
		ctrl.Cancel()
	}
	go func() {
		deadline := time.Now().Add(cliTakeoverTimeout)
		for ctrl != nil && ctrl.Running() && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if ctrl != nil && ctrl.Running() {
			m.reclaiming.Store(false)
			return
		}
		if err := m.returnLease(); err != nil {
			m.reclaiming.Store(false)
			return
		}
		if callback != nil {
			callback()
		}
	}()
}

func (m *cliTakeoverManager) returnLease() error {
	m.returnMu.Lock()
	defer m.returnMu.Unlock()
	binding, ctrl, _ := m.snapshot()
	if binding == nil || m.returned.Load() {
		return nil
	}
	if ctrl != nil {
		if err := ctrl.Snapshot(); err != nil {
			return err
		}
	}
	if err := m.leases.ReleaseForHandoff(binding.grant.SourceWriterID, binding.grant.ReturnHandoffID); err != nil {
		return err
	}
	m.returned.Store(true)
	m.mirrorEnd(binding)
	return nil
}

// RebindAway acquires a new ordinary session before returning the mirrored
// one. It lets /resume and related TUI switches keep their original failure
// atomicity while still honoring Serve's reverse reservation.
func (m *cliTakeoverManager) RebindAway(path string) (bool, error) {
	if m == nil {
		return false, nil
	}
	binding, _, _ := m.snapshot()
	if binding == nil || m.returned.Load() || agent.CanonicalSessionPath(binding.path) == agent.CanonicalSessionPath(path) {
		return false, nil
	}
	m.returnMu.Lock()
	defer m.returnMu.Unlock()
	if m.reclaiming.Load() {
		return true, fmt.Errorf("the remote side is reclaiming this session")
	}
	_ = m.push(false)
	if m.reclaiming.Load() {
		return true, fmt.Errorf("the remote side is reclaiming this session")
	}
	if err := m.leases.RebindReturningCurrent(path, binding.grant.SourceWriterID, binding.grant.ReturnHandoffID); err != nil {
		return true, err
	}
	m.returned.Store(true)
	m.mu.Lock()
	started, stop, done := m.started, m.stop, m.done
	m.mu.Unlock()
	if started {
		m.stopOnce.Do(func() { close(stop) })
		<-done
	}
	m.mirrorEnd(binding)
	m.mu.Lock()
	m.binding = nil
	m.queue = nil
	m.started = false
	m.wake = nil
	m.stop = nil
	m.done = nil
	m.stopOnce = sync.Once{}
	m.reclaiming.Store(false)
	m.returned.Store(false)
	m.mu.Unlock()
	return true, nil
}

// cliAcquireFreeSession starts an ordinary failure-atomic switch. The newly
// acquired target stays in leases while the source binding remains detached
// and live until the caller has loaded and authorized the candidate session.
func cliAcquireFreeSession(path string, leases *control.SessionLeaseKeeper, manager *cliTakeoverManager) (*cliTakeoverBinding, error) {
	if leases == nil {
		return &cliTakeoverBinding{path: path}, nil
	}
	if manager != nil && manager.Reclaiming() {
		return nil, fmt.Errorf("the remote side is reclaiming the current session")
	}
	binding := &cliTakeoverBinding{path: path}
	if manager != nil {
		current, _, _ := manager.snapshot()
		if current != nil && !manager.Returned() && agent.CanonicalSessionPath(current.path) != agent.CanonicalSessionPath(path) {
			binding.priorMirror = current
		}
	}
	previous, err := leases.RebindDetaching(path)
	if err != nil {
		return nil, err
	}
	binding.previous = previous
	return binding, nil
}

// cliPrepareTakeoverCandidate reloads after acquisition. For a Serve handoff,
// this observes the Snapshot completed by /handoff rather than the stale
// preflight view. Authority is bound to the private candidate before the
// controller publishes it through Resume.
func cliPrepareTakeoverCandidate(binding *cliTakeoverBinding, leases *control.SessionLeaseKeeper) (*agent.Session, error) {
	if binding == nil {
		return nil, fmt.Errorf("takeover binding unavailable")
	}
	loaded, err := loadResumableSession(binding.path)
	if err != nil {
		return nil, err
	}
	if leases != nil {
		if err := leases.BindSessionAuthority(loaded); err != nil {
			return nil, err
		}
	}
	return loaded, nil
}

// commitPrevious retires the source keeper only after the handed-off target
// has been loaded successfully. A mirrored source is returned through its
// reverse reservation; an ordinary source is simply released.
func (b *cliTakeoverBinding) commitPrevious(manager *cliTakeoverManager) error {
	if b == nil || b.previous == nil {
		return nil
	}
	if b.priorMirror == nil {
		b.previous.RetireDetached()
		b.previous = nil
		return nil
	}
	if manager == nil {
		return fmt.Errorf("takeover manager unavailable for mirrored source")
	}
	return manager.commitPriorMirror(b)
}

func (m *cliTakeoverManager) commitPriorMirror(next *cliTakeoverBinding) error {
	if m == nil || next == nil || next.previous == nil || next.priorMirror == nil {
		return nil
	}
	m.returnMu.Lock()
	defer m.returnMu.Unlock()
	current, _, _ := m.snapshot()
	if current != next.priorMirror || m.returned.Load() {
		return fmt.Errorf("current takeover mirror changed during session switch")
	}
	if m.reclaiming.Load() {
		return fmt.Errorf("the remote side is reclaiming the current session")
	}
	_ = m.push(false)
	if m.reclaiming.Load() {
		return fmt.Errorf("the remote side is reclaiming the current session")
	}
	if err := next.previous.RetireDetachedForHandoff(current.grant.SourceWriterID, current.grant.ReturnHandoffID); err != nil {
		return err
	}
	next.previous = nil
	m.returned.Store(true)
	m.mu.Lock()
	started, stop, done := m.started, m.stop, m.done
	m.mu.Unlock()
	if started {
		m.stopOnce.Do(func() { close(stop) })
		<-done
	}
	m.mirrorEnd(current)
	m.mu.Lock()
	m.binding = nil
	m.queue = nil
	m.started = false
	m.wake = nil
	m.stop = nil
	m.done = nil
	m.stopOnce = sync.Once{}
	m.reclaiming.Store(false)
	m.returned.Store(false)
	m.mu.Unlock()
	return nil
}

func (m *cliTakeoverManager) mirrorEnd(binding *cliTakeoverBinding) {
	if binding == nil || binding.client == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"sessionPath": binding.path, "mirrorId": binding.grant.MirrorID})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, binding.record.base+"/mirror-end", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := binding.client.Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func cliReturnFailedTakeover(binding *cliTakeoverBinding, leases *control.SessionLeaseKeeper) {
	if binding == nil || leases == nil {
		return
	}
	if binding.grant.ReturnHandoffID != "" && binding.grant.SourceWriterID != "" {
		if err := leases.ReleaseForHandoff(binding.grant.SourceWriterID, binding.grant.ReturnHandoffID); err != nil {
			// Keep the source writable even if the reverse reservation cannot be
			// persisted. mirror-end lets Serve retry ordinary recovery.
			leases.Release()
		}
	} else {
		leases.Release()
	}
	if binding.previous != nil {
		leases.Adopt(binding.previous)
		binding.previous = nil
	}
	if binding.grant.MirrorID == "" {
		return
	}
	m := &cliTakeoverManager{leases: leases}
	m.mirrorEnd(binding)
}

func cliEndFailedHandoff(binding *cliTakeoverBinding) {
	if binding == nil {
		return
	}
	m := &cliTakeoverManager{}
	m.mirrorEnd(binding)
}

// Close returns an active mirrored session on ordinary CLI exit. A concurrent
// reclaim owns the same transaction; wait for it rather than publishing a
// second reservation.
func (m *cliTakeoverManager) Close() error {
	if m == nil {
		return nil
	}
	if m.reclaiming.Load() {
		_, ctrl, _ := m.snapshot()
		deadline := time.Now().Add(cliTakeoverTimeout)
		for ctrl != nil && ctrl.Running() && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if ctrl != nil && ctrl.Running() {
			return fmt.Errorf("timed out waiting for the active turn to yield its session")
		}
	}
	if err := m.returnLease(); err != nil {
		return err
	}
	m.mu.Lock()
	started, stop, done := m.started, m.stop, m.done
	m.mu.Unlock()
	if started {
		m.stopOnce.Do(func() { close(stop) })
		<-done
	}
	return nil
}
