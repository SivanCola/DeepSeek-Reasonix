package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// Session ownership handoff: the single-writer protocol behind local takeover.
//
// A session has exactly one writer at a time — the runtime holding its lease.
// When the machine hosting Serve is also where the user now sits (the remote
// desktop came "home"), the local Reasonix window can take a session over:
// Serve releases the lease and the local window acquires it. Serve keeps no
// controller authority for a mirrored session, but stays the rendezvous: the
// remote tab's SSE stream keeps rendering because the local writer pushes its
// frames through POST /external/frames, and the remote side drops to read-only
// until it reclaims speaking rights via POST /reclaim.
//
// Every transition is cooperative — nothing ever steals the OS-level lease
// file lock. Handoff releases what Serve holds; reclaim waits for the local
// writer to release what it holds (a dead writer releases implicitly when the
// kernel drops its file lock).

// handoffMode selects how a takeover deals with a turn still running on the
// side that is losing the session.
type handoffMode string

const (
	handoffModeWait      handoffMode = "wait"      // drain: wait for the active turn to finish
	handoffModeInterrupt handoffMode = "interrupt" // cancel the active turn, then hand off
)

const (
	// handoffDefaultTimeout bounds a drain-mode takeover and a reclaim's wait
	// for the local writer to yield.
	handoffDefaultTimeout = 60 * time.Second
	handoffPollInterval   = 200 * time.Millisecond
	// mirrorStaleAfter is how long a mirrored session goes without writer
	// contact (frames or heartbeats) before Serve is willing to probe whether
	// the writer is gone and auto-reclaim. Generous against laptop sleeps and
	// GC pauses; the lease probe is the real authority.
	mirrorStaleAfter = 30 * time.Second
)

// leaseHeldByForeignRuntime probes whether a runtime other than this Serve
// process holds a session's lease. It is a variable only so tests can model
// the local writer as a separate process: the real probe answers false for
// leases held by the calling process, and tests hold the writer's lease
// in-process.
var leaseHeldByForeignRuntime = agent.SessionLeaseHeldByOtherRuntime

// errSessionTakenOver is the stable refusal every mutating endpoint returns
// while the foreground session is mirrored to a local writer. Clients match
// the leading sentence to surface the read-only state.
const errSessionTakenOver = "session is taken over by a local Reasonix window and is read-only here; use POST /reclaim to take it back"

// mirroredSession is Serve's bookkeeping for a session whose lease a local
// runtime now holds. Serve answers reads from the transcript file and mirrors
// the writer's frames to subscribers, but must not mutate the session.
type mirroredSession struct {
	path             string
	since            time.Time
	lastContact      time.Time
	reclaimRequested bool
	reclaimMode      handoffMode
}

func (s *Server) markMirrored(path string) {
	path = agent.CanonicalSessionPath(path)
	if path == "" {
		return
	}
	now := time.Now()
	s.mirrorMu.Lock()
	if s.mirrored == nil {
		s.mirrored = map[string]*mirroredSession{}
	}
	if existing := s.mirrored[path]; existing != nil {
		existing.lastContact = now
	} else {
		s.mirrored[path] = &mirroredSession{path: path, since: now, lastContact: now}
	}
	s.mirrorMu.Unlock()
}

func (s *Server) clearMirrored(path string) *mirroredSession {
	path = agent.CanonicalSessionPath(path)
	s.mirrorMu.Lock()
	m := s.mirrored[path]
	delete(s.mirrored, path)
	s.mirrorMu.Unlock()
	return m
}

func (s *Server) mirroredEntry(path string) *mirroredSession {
	path = agent.CanonicalSessionPath(path)
	s.mirrorMu.Lock()
	defer s.mirrorMu.Unlock()
	return s.mirrored[path]
}

func (s *Server) sessionMirrored(path string) bool {
	return s.mirroredEntry(path) != nil
}

// foregroundMirroredLocked reports whether the current foreground session has
// been handed to a local writer. Callers hold bindMu.
func (s *Server) foregroundMirroredLocked() bool {
	cur := s.ctl()
	if cur == nil {
		return false
	}
	return s.sessionMirrored(cur.SessionPath())
}

func (s *Server) touchMirrored(path string) {
	s.mirrorMu.Lock()
	if m := s.mirrored[agent.CanonicalSessionPath(path)]; m != nil {
		m.lastContact = time.Now()
	}
	s.mirrorMu.Unlock()
}

// rejectMirroredForegroundLocked answers 409 for foreground mutations while
// the session is mirrored. Returns true when the response was written.
// Callers hold bindMu.
func (s *Server) rejectMirroredForegroundLocked(w http.ResponseWriter) bool {
	if !s.foregroundMirroredLocked() {
		return false
	}
	http.Error(w, errSessionTakenOver, http.StatusConflict)
	return true
}

// snapshotForeground persists the foreground session before a switch, unless a
// local writer owns it — a save attempt there fails closed (no write
// authority) and the conflict path could fork a recovery branch into a file
// the writer now owns. Callers hold bindMu.
func (s *Server) snapshotForeground(cur control.SessionAPI) {
	if s.foregroundMirroredLocked() {
		return
	}
	if err := cur.Snapshot(); err != nil {
		slog.Warn("serve: snapshot before switch", "err", err)
	}
}

// resolveSessionPath validates a client-supplied session path against the
// foreground session dir the same way POST /resume does: absolute, a real
// transcript file, inside the session dir, and not pending cleanup. The
// returned path is symlink-resolved.
func (s *Server) resolveSessionPath(raw string) (string, error) {
	dir := s.ctl().SessionDir()
	if dir == "" {
		return "", errors.New("sessions disabled")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", errors.New("invalid session dir")
	}
	realDir, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return "", errors.New("invalid session dir")
	}
	absPath, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil || !store.IsSessionTranscriptName(filepath.Base(absPath)) {
		return "", errors.New("invalid session path")
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", errors.New("invalid session path")
	}
	if realPath == realDir || !strings.HasPrefix(realPath, realDir+string(os.PathSeparator)) {
		return "", errors.New("path outside session dir")
	}
	if agent.IsCleanupPending(realPath) {
		return "", errors.New("session is pending cleanup")
	}
	return realPath, nil
}

type ownershipView struct {
	SessionPath      string `json:"sessionPath"`
	Holder           string `json:"holder"` // serve | external | other | free
	RemoteAttached   bool   `json:"remoteAttached"`
	Running          bool   `json:"running"`
	Mirrored         bool   `json:"mirrored"`
	ReclaimRequested bool   `json:"reclaimRequested"`
	TakenOver        bool   `json:"takenOver"`
	HolderPID        int    `json:"holderPid,omitempty"`
	HolderHost       string `json:"holderHost,omitempty"`
}

// ownership reports who currently writes a session, whether a remote SSE
// client is attached, and whether a turn is running — the inputs a local
// takeover prompt needs. remoteAttached counts every SSE subscriber; Serve
// cannot distinguish the desktop pump from a browser tab, so it is an
// over-approximation of "the remote side is watching".
func (s *Server) ownership(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("session")
	realPath, err := s.resolveSessionPath(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	view := ownershipView{SessionPath: agent.CanonicalSessionPath(realPath), RemoteAttached: s.bc.Subscribers() > 0}
	if m := s.mirroredEntry(realPath); m != nil {
		view.Holder = "external"
		view.Mirrored = true
		view.TakenOver = true
		view.ReclaimRequested = m.reclaimRequested
		s.appendServeIdentity(&view)
		writeJSON(w, view)
		return
	}
	cur := s.ctl()
	foreground := cur != nil && agent.CanonicalSessionPath(cur.SessionPath()) == agent.CanonicalSessionPath(realPath)
	if foreground {
		view.Holder = "serve"
		view.Running = controllerHasActiveRuntimeWork(cur)
		s.appendServeIdentity(&view)
		writeJSON(w, view)
		return
	}
	if s.detachedBusy(realPath) {
		view.Holder = "serve"
		view.Running = s.detachedHasActiveWork(realPath)
		s.appendServeIdentity(&view)
		writeJSON(w, view)
		return
	}
	if leaseHeldByForeignRuntime(realPath) {
		view.Holder = "other"
	}
	if view.Holder == "" {
		view.Holder = "free"
	}
	writeJSON(w, view)
}

func (s *Server) appendServeIdentity(view *ownershipView) {
	host, _ := os.Hostname()
	view.HolderPID = os.Getpid()
	view.HolderHost = strings.TrimSpace(host)
}

func (s *Server) detachedHasActiveWork(path string) bool {
	path = agent.CanonicalSessionPath(path)
	s.detachedMu.Lock()
	defer s.detachedMu.Unlock()
	d := s.detached[path]
	return d != nil && controllerHasActiveRuntimeWork(d.ctrl)
}

type handoffRequest struct {
	SessionPath string `json:"sessionPath"`
	Force       bool   `json:"force"`
	Mode        string `json:"mode"`
	TimeoutMs   int    `json:"timeoutMs"`
}

// handoff releases a session Serve holds so a local runtime on this machine
// can take it over. With force unset it refuses while a remote client is
// attached — the caller is expected to have confirmed the takeover with its
// user via GET /ownership. wait drains a running turn; interrupt cancels it.
func (s *Server) handoff(w http.ResponseWriter, r *http.Request) {
	var body handoffRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SessionPath) == "" {
		http.Error(w, "missing sessionPath", http.StatusBadRequest)
		return
	}
	mode := parseHandoffMode(body.Mode)
	realPath, err := s.resolveSessionPath(body.SessionPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.sessionMirrored(realPath) {
		writeJSON(w, map[string]string{"status": "already_handed_off", "sessionPath": agent.CanonicalSessionPath(realPath)})
		return
	}
	if !body.Force && s.bc.Subscribers() > 0 {
		http.Error(w, "session is attached to a remote client; retry with force after confirming the takeover", http.StatusConflict)
		return
	}
	timeout := handoffTimeout(body.TimeoutMs)

	// Drain or cancel outside bindMu: waiting inside would freeze every other
	// command for up to the whole timeout.
	if err := s.quietSessionForHandoff(realPath, mode, timeout); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	s.bindMu.Lock()
	err = s.handoffLocked(r, realPath)
	s.bindMu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), statusForHandoffError(err))
		return
	}
	writeJSON(w, map[string]string{"status": "handed_off", "sessionPath": agent.CanonicalSessionPath(realPath)})
}

func parseHandoffMode(raw string) handoffMode {
	if handoffMode(raw) == handoffModeInterrupt {
		return handoffModeInterrupt
	}
	return handoffModeWait
}

func handoffTimeout(ms int) time.Duration {
	if ms <= 0 {
		return handoffDefaultTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

func statusForHandoffError(err error) int {
	switch {
	case errors.Is(err, errSessionNotHeld), errors.Is(err, errHandoffBusyAgain):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

var (
	errSessionNotHeld   = errors.New("session is not held by this serve process")
	errHandoffBusyAgain = errors.New("session became busy again during handoff; retry")
)

// quietSessionForHandoff waits for (or cancels toward) an idle session before
// the binding transaction runs. It re-checks under bindMu afterwards: turn
// admission holds bindMu, so once the caller holds it and the session is
// idle, no new turn can start on it.
func (s *Server) quietSessionForHandoff(realPath string, mode handoffMode, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		cur := s.ctl()
		foreground := cur != nil && agent.CanonicalSessionPath(cur.SessionPath()) == agent.CanonicalSessionPath(realPath)
		var busy bool
		switch {
		case foreground:
			busy = controllerHasActiveRuntimeWork(cur)
			if busy && mode == handoffModeInterrupt {
				cur.Cancel()
			}
		case s.detachedBusy(realPath):
			s.detachedMu.Lock()
			d := s.detached[agent.CanonicalSessionPath(realPath)]
			ctrl := control.SessionAPI(nil)
			if d != nil {
				ctrl = d.ctrl
			}
			s.detachedMu.Unlock()
			busy = ctrl != nil && controllerHasActiveRuntimeWork(ctrl)
			if busy && mode == handoffModeInterrupt {
				ctrl.Cancel()
			}
		default:
			return errSessionNotHeld
		}
		if !busy {
			return nil
		}
		if time.Now().After(deadline) {
			if mode == handoffModeInterrupt {
				return fmt.Errorf("session did not stop within %s; retry", timeout)
			}
			return fmt.Errorf("session is still running after %s; retry with mode=interrupt to cancel it", timeout)
		}
		time.Sleep(handoffPollInterval)
	}
}

// handoffLocked performs the release transaction. Callers hold bindMu and
// have already quieted the session.
func (s *Server) handoffLocked(r *http.Request, realPath string) error {
	cur := s.ctl()
	canonical := agent.CanonicalSessionPath(realPath)
	switch {
	case cur != nil && agent.CanonicalSessionPath(cur.SessionPath()) == canonical:
		if controllerHasActiveRuntimeWork(cur) {
			return errHandoffBusyAgain
		}
		// Flush the in-memory transcript while this process still owns the
		// file, then hand the lease over. Rebind("") also unbinds the
		// controller's write authority, so any later save fails closed
		// instead of racing the new writer.
		if err := cur.Snapshot(); err != nil {
			slog.Warn("serve: snapshot before handoff", "err", err)
		}
		if s.leases != nil {
			if err := s.leases.Rebind(""); err != nil {
				return fmt.Errorf("handoff: release session lease: %w", err)
			}
		}
	case s.detachedBusy(realPath):
		detached := s.takeDetached(realPath)
		if detached == nil {
			return errHandoffBusyAgain
		}
		if controllerHasActiveRuntimeWork(detached.ctrl) {
			_, _ = s.registerDetached(detached.ctrl, detached.keeper, detached.tag)
			return errHandoffBusyAgain
		}
		detached.ctrl.Close()
		if detached.keeper != nil {
			detached.keeper.Release()
		}
		if concrete, ok := detached.ctrl.(*control.Controller); ok {
			s.forgetSessionTag(concrete)
		}
	default:
		return errSessionNotHeld
	}
	s.markMirrored(realPath)
	slog.Info("serve: session handed off to local runtime", "session", canonical)
	s.bc.Emit(event.Event{
		Kind:        event.Notice,
		Level:       event.LevelWarn,
		Code:        event.NoticeCodeSessionTakenOver,
		Text:        "This session was taken over by a local Reasonix window and is read-only here.",
		Detail:      "A Reasonix window on this machine took over the conversation. It keeps streaming here; use \"take back\" to reclaim it.",
		SessionPath: canonical,
	})
	return nil
}

type externalFramesRequest struct {
	SessionPath string            `json:"sessionPath"`
	Frames      []eventwire.Event `json:"frames"`
}

type externalFramesResponse struct {
	ReclaimRequested bool        `json:"reclaimRequested"`
	ReclaimMode      handoffMode `json:"reclaimMode,omitempty"`
}

// externalFrames mirrors the local writer's frames to every subscriber. An
// empty frame list is a heartbeat: the response tells the writer when the
// remote side asked for the session back, so an idle writer learns about a
// reclaim without pushing anything.
func (s *Server) externalFrames(w http.ResponseWriter, r *http.Request) {
	var body externalFramesRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SessionPath) == "" {
		http.Error(w, "missing sessionPath", http.StatusBadRequest)
		return
	}
	realPath, err := s.resolveSessionPath(body.SessionPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m := s.mirroredEntry(realPath)
	if m == nil {
		http.Error(w, "session is not mirrored by this serve process", http.StatusConflict)
		return
	}
	canonical := agent.CanonicalSessionPath(realPath)
	for i := range body.Frames {
		frame := body.Frames[i]
		frame.SessionPath = canonical
		s.bc.EmitWire(frame)
	}
	s.touchMirrored(realPath)
	writeJSON(w, externalFramesResponse{ReclaimRequested: m.reclaimRequested, ReclaimMode: m.reclaimMode})
}

// reclaim is the remote side's way back: it asks the local writer to yield
// the session, waits for the lease to come free, then re-owns the session.
// The local side demotes passively — it sees reclaimRequested on its next
// frame push or heartbeat — so exactly one side speaks at any moment.
func (s *Server) reclaim(w http.ResponseWriter, r *http.Request) {
	var body handoffRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SessionPath) == "" {
		http.Error(w, "missing sessionPath", http.StatusBadRequest)
		return
	}
	mode := parseHandoffMode(body.Mode)
	timeout := handoffTimeout(body.TimeoutMs)
	realPath, err := s.resolveSessionPath(body.SessionPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	canonical := agent.CanonicalSessionPath(realPath)

	s.mirrorMu.Lock()
	m := s.mirrored[canonical]
	if m == nil {
		s.mirrorMu.Unlock()
		if s.serveHoldsSession(realPath) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// A session held by a local process that was never adopted (the adopt
		// can fail silently) has no mirror forwarder to signal. The reclaim
		// can only wait for the lease to free — cap it short so the caller
		// gets actionable feedback instead of a two-minute hang.
		if leaseHeldByForeignRuntime(realPath) {
			slog.Info("serve: reclaim on un-mirrored foreign-held session (adopter absent)",
				"session", canonical)
			deadline := time.Now().Add(10 * time.Second)
			for leaseHeldByForeignRuntime(realPath) {
				if time.Now().After(deadline) {
					http.Error(w, "session is held by a local Reasonix window that never registered a mirror; close that window or retry after it exits", http.StatusConflict)
					return
				}
				time.Sleep(handoffPollInterval)
			}
			s.bindMu.Lock()
			defer s.bindMu.Unlock()
			s.resumeSession(w, r, realPath)
			return
		}
		http.Error(w, "session is not held by any known runtime", http.StatusConflict)
		return
	}
	m.reclaimRequested = true
	m.reclaimMode = mode
	s.mirrorMu.Unlock()
	s.bc.Emit(event.Event{
		Kind:        event.Notice,
		Code:        event.NoticeCodeSessionReclaimRequested,
		Text:        "The remote side asked to take this session back.",
		SessionPath: canonical,
	})
	slog.Info("serve: reclaim requested", "session", canonical, "mode", string(mode))

	deadline := time.Now().Add(timeout)
	for leaseHeldByForeignRuntime(realPath) {
		if time.Now().After(deadline) {
			http.Error(w, "local writer did not yield the session; retry", http.StatusConflict)
			return
		}
		time.Sleep(handoffPollInterval)
	}

	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	// The writer may have ended the mirror itself while we waited.
	if s.mirroredEntry(realPath) == nil {
		if s.serveHoldsSession(realPath) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	} else {
		s.clearMirrored(realPath)
	}
	if cur := s.ctl(); cur != nil && agent.CanonicalSessionPath(cur.SessionPath()) == canonical {
		s.reclaimForeground(w, cur, realPath)
		return
	}
	s.resumeSession(w, r, realPath)
}

func (s *Server) serveHoldsSession(realPath string) bool {
	cur := s.ctl()
	if cur != nil && agent.CanonicalSessionPath(cur.SessionPath()) == agent.CanonicalSessionPath(realPath) {
		return true
	}
	return s.detachedBusy(realPath)
}

// reclaimForeground reloads the transcript from disk (the local writer may
// have extended it) and rebinds the lease and write authority to the still
// open foreground controller. Callers hold bindMu.
func (s *Server) reclaimForeground(w http.ResponseWriter, cur control.SessionAPI, realPath string) {
	loaded, err := agent.LoadSession(realPath)
	if err != nil {
		http.Error(w, "load session: "+err.Error(), http.StatusBadRequest)
		return
	}
	if s.leases != nil {
		if err := s.leases.Rebind(realPath); err != nil {
			if errors.Is(err, agent.ErrSessionLeaseHeld) {
				http.Error(w, sessionInUseError(err), http.StatusConflict)
			} else {
				http.Error(w, "session lease: "+err.Error(), http.StatusInternalServerError)
			}
			return
		}
	}
	if !s.commitLoadedResume(w, cur, loaded, realPath) {
		return
	}
	s.bc.ResetSessionPath(realPath)
	s.announceSessionChanged(realPath, false)
	s.broadcastReclaimed(realPath)
	w.WriteHeader(http.StatusNoContent)
	s.replayPendingPromptsBroadcast()
}

func (s *Server) broadcastReclaimed(realPath string) {
	s.bc.Emit(event.Event{
		Kind:        event.Notice,
		Code:        event.NoticeCodeSessionReclaimed,
		Text:        "This session is driven remotely again.",
		SessionPath: agent.CanonicalSessionPath(realPath),
	})
}

// adopt registers a session the local runtime already owns as mirrored, so
// the remote side can watch it read-only and reclaim it. This is the local
// desktop's announcement when it opens a session directly (no takeover — there
// was nothing to hand off): Serve must know about the writer to mediate
// reclaim and mirror the frames. Sessions Serve itself holds are refused —
// those go through /handoff instead.
func (s *Server) adopt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionPath string `json:"sessionPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SessionPath) == "" {
		http.Error(w, "missing sessionPath", http.StatusBadRequest)
		return
	}
	realPath, err := s.resolveSessionPath(body.SessionPath)
	if err != nil {
		http.Error(w, err.Error(), resolveSessionPathStatus(err))
		return
	}
	if s.serveHoldsSession(realPath) {
		http.Error(w, "session is held by this serve; use POST /handoff to take it over", http.StatusConflict)
		return
	}
	if s.sessionMirrored(realPath) {
		writeJSON(w, map[string]string{"status": "already_adopted", "sessionPath": agent.CanonicalSessionPath(realPath)})
		return
	}
	s.markMirrored(realPath)
	slog.Info("serve: session adopted by local runtime", "session", agent.CanonicalSessionPath(realPath))
	s.bc.Emit(event.Event{
		Kind:        event.Notice,
		Level:       event.LevelWarn,
		Code:        event.NoticeCodeSessionTakenOver,
		Text:        "This session was taken over by a local Reasonix window and is read-only here.",
		Detail:      "A Reasonix window on this machine opened this session; it keeps streaming here. Use \"take back\" to reclaim it.",
		SessionPath: agent.CanonicalSessionPath(realPath),
	})
	writeJSON(w, map[string]string{"status": "adopted", "sessionPath": agent.CanonicalSessionPath(realPath)})
}

// mirroredReadView reports whether session is mirrored, and if so builds the
// file-backed read view for read-only endpoints that select a specific
// session.
func (s *Server) mirroredReadView(session string) (string, []provider.Message, bool) {
	realPath, err := s.resolveSessionPath(session)
	if err != nil || !s.sessionMirrored(realPath) {
		return "", nil, false
	}
	msgs, ok := s.mirroredHistory(realPath)
	if !ok {
		return "", nil, false
	}
	return agent.CanonicalSessionPath(realPath), msgs, true
}

// externalReadView serves the file-backed read view for any session a local
// runtime owns — mirrored via /adopt or /handoff, or merely held by another
// process on this machine. A spectator client (remote tab) needs the local
// writer's transcript, not Serve's foreground.
func (s *Server) externalReadView(session string) (string, []provider.Message, bool) {
	if s.sessionMirrored(session) {
		return s.mirroredReadView(session)
	}
	realPath, err := s.resolveSessionPath(session)
	if err != nil || !leaseHeldByForeignRuntime(realPath) {
		return "", nil, false
	}
	msgs, ok := s.mirroredHistory(realPath)
	if !ok {
		return "", nil, false
	}
	return agent.CanonicalSessionPath(realPath), msgs, true
}

// statusViewForPath renders the per-session status payload for the ?session=
// selector. Local-owned sessions (mirrored or foreign-held) report takenOver;
// Serve-owned sessions report takenOver=false so a spectator client clears its
// read-only pin after reclaim or when the session returns to the foreground.
func (s *Server) statusViewForPath(path string, held bool) map[string]any {
	if held {
		return s.externalStatusView(path)
	}
	running := false
	cur := s.ctl()
	if cur != nil && agent.CanonicalSessionPath(cur.SessionPath()) == agent.CanonicalSessionPath(path) {
		running = controllerHasActiveRuntimeWork(cur)
	}
	return map[string]any{
		"label":            s.ctl().Label(),
		"running":          running,
		"plan":             false,
		"autoApproveTools": false,
		"bypass":           false,
		"toolApprovalMode": "ask",
		"cwd":              s.ctl().SessionDir(),
		"pendingPrompt":    false,
		"backgroundJobs":   0,
		"takenOver":        false,
		"sessionName":      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		"sessionPath":      agent.CanonicalSessionPath(path),
	}
}

// externalStatusView renders the status payload for a session a local runtime
// owns (mirrored or foreign-held): nothing here can run, ownership is external,
// and the surface must render read-only.
func (s *Server) externalStatusView(path string) map[string]any {
	if m := s.mirroredEntry(path); m != nil {
		return s.mirrorStatusView(path)
	}
	sess := map[string]any{
		"label":            s.ctl().Label(),
		"running":          false,
		"plan":             false,
		"autoApproveTools": false,
		"bypass":           false,
		"toolApprovalMode": "ask",
		"cwd":              s.ctl().SessionDir(),
		"pendingPrompt":    false,
		"backgroundJobs":   0,
		"takenOver":        true,
		"sessionName":      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		"sessionPath":      agent.CanonicalSessionPath(path),
	}
	return sess
}

// mirrorStatusView renders the status payload for a mirrored session selected
// via ?session=: nothing can run here, ownership is external, and the surface
// must render read-only.
func (s *Server) mirrorStatusView(path string) map[string]any {
	m := s.mirroredEntry(path)
	sess := map[string]any{
		"label":            s.ctl().Label(),
		"running":          false,
		"plan":             false,
		"autoApproveTools": false,
		"bypass":           false,
		"toolApprovalMode": "ask",
		"cwd":              s.ctl().SessionDir(),
		"pendingPrompt":    false,
		"backgroundJobs":   0,
		"takenOver":        true,
		"sessionName":      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		"sessionPath":      agent.CanonicalSessionPath(path),
	}
	if m != nil {
		sess["reclaimRequested"] = m.reclaimRequested
	}
	return sess
}

// mirrorEnd is the local writer's farewell: it has closed its tab and dropped
// the lease, so the remote side can speak again without an explicit reclaim
// round-trip. A writer that still holds the lease is told to release first —
// ending the mirror under a live writer would leave the foreground writable
// in name only (its write authority is gone) and render stale history.
func (s *Server) mirrorEnd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionPath string `json:"sessionPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SessionPath) == "" {
		http.Error(w, "missing sessionPath", http.StatusBadRequest)
		return
	}
	realPath, err := s.resolveSessionPath(body.SessionPath)
	if err != nil {
		http.Error(w, err.Error(), resolveSessionPathStatus(err))
		return
	}
	if s.mirroredEntry(realPath) == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if leaseHeldByForeignRuntime(realPath) {
		http.Error(w, "local writer still holds the session; release it before ending the mirror", http.StatusConflict)
		return
	}
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if s.mirroredEntry(realPath) == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.clearMirrored(realPath)
	if cur := s.ctl(); cur != nil && agent.CanonicalSessionPath(cur.SessionPath()) == agent.CanonicalSessionPath(realPath) {
		s.reclaimForeground(w, cur, realPath)
		return
	}
	s.broadcastReclaimed(realPath)
	w.WriteHeader(http.StatusNoContent)
}

// maybeAutoReclaimMirrored recovers a mirror whose writer vanished without
// calling /mirror-end (killed window, laptop died). The OS releases the lease
// with the process; once the entry is stale and the lease is free, hand the
// session back to the remote side.
func (s *Server) maybeAutoReclaimMirrored(path string) {
	m := s.mirroredEntry(path)
	if m == nil {
		return
	}
	if time.Since(m.lastContact) < mirrorStaleAfter {
		return
	}
	if leaseHeldByForeignRuntime(path) {
		// The writer is alive but quiet (or another runtime took the file).
		// Push the staleness window so a chatty-but-healthy writer never
		// gets reclaimed under itself.
		s.touchMirrored(path)
		return
	}
	if m.reclaimRequested {
		// The writer vanished AFTER a reclaim was requested: its OS lock died
		// with it, so the outstanding reclaim can finally complete. Skipping
		// here (as this function used to) left the entry mirrored with the
		// flag set forever — the remote tab stayed a read-only spectator with
		// every retry 409ing after the wait timeout.
		slog.Info("serve: completing outstanding reclaim for vanished writer",
			"session", agent.CanonicalSessionPath(path))
	}
	go func() {
		s.bindMu.Lock()
		defer s.bindMu.Unlock()
		if s.mirroredEntry(path) == nil {
			return
		}
		s.clearMirrored(path)
		cur := s.ctl()
		if cur != nil && agent.CanonicalSessionPath(cur.SessionPath()) == agent.CanonicalSessionPath(path) {
			loaded, err := agent.LoadSession(path)
			if err != nil || s.leases == nil || s.leases.Rebind(path) != nil || !s.commitLoadedResumeQuiet(cur, loaded, path) {
				slog.Warn("serve: auto-reclaim of stale mirror failed", "session", path, "err", err)
				return
			}
			s.bc.ResetSessionPath(path)
		}
		s.announceSessionChanged(agent.CanonicalSessionPath(path), false)
		s.broadcastReclaimed(path)
		slog.Info("serve: stale mirror auto-reclaimed", "session", path)
	}()
}

// commitLoadedResumeQuiet is commitLoadedResume without an HTTP response: the
// auto-reclaim path has no client to answer.
func (s *Server) commitLoadedResumeQuiet(cur control.SessionAPI, loaded *agent.Session, realPath string) bool {
	w := noopResponseWriter{}
	return s.commitLoadedResume(w, cur, loaded, realPath)
}

type noopResponseWriter struct{}

func (noopResponseWriter) Header() http.Header       { return http.Header{} }
func (noopResponseWriter) Write([]byte) (int, error) { return 0, nil }
func (noopResponseWriter) WriteHeader(int)           {}

// mirroredHistory reads the transcript file for a mirrored session so
// hydrating and reconciling clients see the local writer's turns, not Serve's
// frozen in-memory copy. Returns false when the file cannot be read; callers
// fall back to the stale in-memory history.
func (s *Server) mirroredHistory(realPath string) ([]provider.Message, bool) {
	loaded, err := agent.LoadSession(realPath)
	if err != nil || loaded == nil {
		return nil, false
	}
	return loaded.Messages, true
}
