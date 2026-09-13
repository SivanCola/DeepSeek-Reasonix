package sessionv3

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// SessionRef is the only execution identity used by the linear session
// service. Paths and UI selection are deliberately absent.
type SessionRef struct {
	HostID    string `json:"hostId"`
	SessionID string `json:"sessionId"`
}

func (r SessionRef) validate(hostID string) error {
	if r.HostID == "" || r.HostID != hostID {
		return fmt.Errorf("sessionv3: session host %q does not match service host %q", r.HostID, hostID)
	}
	return validateSessionID(r.SessionID)
}

var (
	ErrSessionNotRunning = errors.New("session runtime is not attached")
	ErrRuntimeBusy       = errors.New("session runtime already has an activity")
	ErrRecoveryRequired  = errors.New("session runtime requires recovery")
	ErrStaleActivity     = errors.New("session activity no longer owns commit authority")
)

type RuntimePhase string

const (
	RuntimeIdle             RuntimePhase = "idle"
	RuntimeRunning          RuntimePhase = "running"
	RuntimeCancelling       RuntimePhase = "cancelling"
	RuntimeRecoveryRequired RuntimePhase = "recovery_required"
	RuntimeClosed           RuntimePhase = "closed"
)

type RuntimeSnapshot struct {
	Ref              SessionRef   `json:"session"`
	Epoch            string       `json:"runtimeEpoch"`
	ActivityRevision uint64       `json:"activityRevision"`
	Phase            RuntimePhase `json:"phase"`
	Activity         string       `json:"activity,omitempty"`
	Session          Snapshot     `json:"sessionSnapshot"`
}

type CancelReceipt struct {
	Ref              SessionRef   `json:"session"`
	Accepted         bool         `json:"accepted"`
	RuntimeEpoch     string       `json:"runtimeEpoch,omitempty"`
	ActivityRevision uint64       `json:"activityRevision,omitempty"`
	Phase            RuntimePhase `json:"phase"`
}

// Runtime is the sole owner of a live Session and its write handle. It owns
// transient activity and cancellation; persisted running events never create
// a Runtime after process restart.
type Runtime struct {
	ref     SessionRef
	epoch   string
	session *Session

	mu         sync.Mutex
	phase      RuntimePhase
	activity   string
	revision   uint64
	activityID uint64
	cancel     context.CancelFunc
	closeOnce  sync.Once
	closeErr   error
}

// Activity is a generation-bound permit. Agent and tool work commit business
// events through it so a cancelled or replaced activity cannot publish a late
// result into the session. Diagnostic logging uses a separate non-business
// channel and does not regain this permit.
type Activity struct {
	runtime *Runtime
	id      uint64
}

func newRuntime(ref SessionRef, session *Session) *Runtime {
	return &Runtime{ref: ref, epoch: randomID(), session: session, phase: RuntimeIdle, revision: 1}
}

func (r *Runtime) Ref() SessionRef { return r.ref }

func (r *Runtime) Session() *Session { return r.session }

func (r *Runtime) Snapshot() RuntimeSnapshot {
	r.mu.Lock()
	state := RuntimeSnapshot{Ref: r.ref, Epoch: r.epoch, ActivityRevision: r.revision, Phase: r.phase, Activity: r.activity}
	r.mu.Unlock()
	state.Session = r.session.Snapshot()
	return state
}

// BeginActivity establishes cancellation ownership before any turn event or
// downstream work starts. finish only affects the exact activity generation.
func (r *Runtime) BeginActivity(parent context.Context, name string) (context.Context, func(error), error) {
	ctx, activity, err := r.BeginOwnedActivity(parent, name)
	if err != nil {
		return nil, nil, err
	}
	return ctx, activity.Finish, nil
}

func (r *Runtime) BeginOwnedActivity(parent context.Context, name string) (context.Context, *Activity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase == RuntimeRecoveryRequired {
		return nil, nil, ErrRecoveryRequired
	}
	if r.phase == RuntimeClosed {
		return nil, nil, osClosedError()
	}
	if r.phase != RuntimeIdle {
		return nil, nil, ErrRuntimeBusy
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r.phase, r.activity, r.cancel = RuntimeRunning, name, cancel
	r.activityID++
	r.revision++
	activityID := r.activityID
	return ctx, &Activity{runtime: r, id: activityID}, nil
}

func (a *Activity) AppendBatch(ctx context.Context, operationID string, events []Event) (Commit, error) {
	return a.Append(ctx, Batch{OperationID: operationID, Events: events})
}

// Append preserves the complete logical batch, including its turn identity,
// while fencing the commit against the exact activity generation.
func (a *Activity) Append(ctx context.Context, batch Batch) (Commit, error) {
	if a == nil || a.runtime == nil {
		return Commit{}, ErrStaleActivity
	}
	runtime := a.runtime
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.activityID != a.id || runtime.cancel == nil {
		return Commit{}, ErrStaleActivity
	}
	if runtime.phase != RuntimeRunning && (runtime.phase != RuntimeCancelling || !activityClosureBatch(batch)) {
		return Commit{}, ErrStaleActivity
	}
	if err := ctx.Err(); err != nil {
		return Commit{}, err
	}
	return runtime.session.Handle.Append(ctx, batch)
}

func activityClosureBatch(batch Batch) bool {
	if len(batch.Events) == 0 {
		return false
	}
	for _, event := range batch.Events {
		switch event.Kind {
		case "turn/end", "interaction/resolved", "runtime/recovery", "assistant/attempt", "diagnostic":
		default:
			return false
		}
	}
	return true
}

func (a *Activity) Finish(_ error) {
	if a == nil || a.runtime == nil {
		return
	}
	runtime := a.runtime
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.activityID != a.id || (runtime.phase != RuntimeRunning && runtime.phase != RuntimeCancelling) {
		return
	}
	runtime.cancel = nil
	runtime.activity = ""
	runtime.phase = RuntimeIdle
	runtime.revision++
}

// Cancel sends the signal while holding only the small runtime mutex. It does
// not flush, publish UI state, or wait for a callback.
func (r *Runtime) Cancel() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase == RuntimeIdle {
		return false
	}
	if r.phase != RuntimeRunning && r.phase != RuntimeCancelling {
		return false
	}
	if r.phase == RuntimeRunning {
		r.phase = RuntimeCancelling
		r.revision++
	}
	if r.cancel != nil {
		r.cancel()
	}
	return true
}

func (r *Runtime) RequireRecovery(activity string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase == RuntimeClosed {
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	r.cancel = nil
	r.activityID++
	r.phase = RuntimeRecoveryRequired
	r.activity = activity
	r.revision++
}

// RecordRecovery appends terminal recovery facts after RequireRecovery has
// revoked the activity permit. It cannot be used by a stale worker to publish
// a tool result or another business-state transition.
func (r *Runtime) RecordRecovery(ctx context.Context, batch Batch) (Commit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != RuntimeRecoveryRequired || !recoveryClosureBatch(batch) {
		return Commit{}, ErrStaleActivity
	}
	if err := ctx.Err(); err != nil {
		return Commit{}, err
	}
	return r.session.Handle.Append(ctx, batch)
}

func recoveryClosureBatch(batch Batch) bool {
	if !activityClosureBatch(batch) {
		return false
	}
	for _, event := range batch.Events {
		if event.Kind == "runtime/recovery" {
			return true
		}
	}
	return false
}

func (r *Runtime) close(ctx context.Context) error {
	r.mu.Lock()
	if r.phase == RuntimeRunning || r.phase == RuntimeCancelling || r.phase == RuntimeRecoveryRequired {
		r.mu.Unlock()
		return ErrRuntimeBusy
	}
	r.mu.Unlock()
	r.closeOnce.Do(func() {
		r.mu.Lock()
		if r.cancel != nil {
			r.cancel()
		}
		r.cancel = nil
		r.phase = RuntimeClosed
		r.activity = ""
		r.revision++
		r.mu.Unlock()
		r.closeErr = r.session.Handle.Close(ctx)
	})
	return r.closeErr
}

func osClosedError() error { return errors.New("session runtime is closed") }

// Service applies DSH's prepare/publish/exact-detach rule. Candidate handles
// are opened outside the registry lock; only the exact published Runtime can
// later unregister itself.
type Service struct {
	hostID      string
	persistence SessionPersistence

	mu        sync.Mutex
	active    map[SessionRef]*Runtime
	closed    map[SessionRef]*Runtime
	preparing map[SessionRef]*prepareRuntime
	revision  atomic.Uint64
}

// HostID returns the immutable host namespace used to validate SessionRef.
func (s *Service) HostID() string {
	if s == nil {
		return ""
	}
	return s.hostID
}

type prepareRuntime struct{ done chan struct{} }

// PreparedRuntime owns a write handle that has been fully opened but is not
// yet visible through the host registry. Callers may build projections, seed
// initial events, and flush before atomically publishing the exact instance.
// A candidate must be either published or discarded.
type PreparedRuntime struct {
	service *Service
	runtime *Runtime

	mu        sync.Mutex
	published bool
	discarded bool
}

func (p *PreparedRuntime) Runtime() *Runtime {
	if p == nil {
		return nil
	}
	return p.runtime
}

func NewService(hostID string, persistence SessionPersistence) (*Service, error) {
	if hostID == "" || persistence == nil {
		return nil, errors.New("sessionv3: host id and persistence are required")
	}
	return &Service{hostID: hostID, persistence: persistence, active: map[SessionRef]*Runtime{}, closed: map[SessionRef]*Runtime{}, preparing: map[SessionRef]*prepareRuntime{}}, nil
}

func (s *Service) Create(ctx context.Context, options CreateOptions) (*Runtime, error) {
	prepared, err := s.PrepareCreate(ctx, options)
	if err != nil {
		return nil, err
	}
	runtime, err := s.Publish(prepared)
	if err != nil {
		_ = s.Discard(context.Background(), prepared)
	}
	return runtime, err
}

// PrepareCreate reserves the immutable session identity and its writer lease
// without publishing an attachable runtime. This is the DSH prepare phase:
// host/controller state remains untouched until Publish succeeds.
func (s *Service) PrepareCreate(ctx context.Context, options CreateOptions) (*PreparedRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, err := s.persistence.Create(options)
	if err != nil {
		return nil, err
	}
	store, ok := handle.(WritableSessionHandle)
	if !ok {
		_ = handle.Close(context.Background())
		return nil, errors.New("sessionv3: writable persistence did not return a live store")
	}
	ref := SessionRef{HostID: s.hostID, SessionID: store.SessionID()}
	candidate := newRuntime(ref, &Session{Handle: store})
	return &PreparedRuntime{service: s, runtime: candidate}, nil
}

// Publish makes the exact prepared runtime visible. It never replaces an
// existing instance with the same identity; the caller must resolve that
// ownership conflict explicitly.
func (s *Service) Publish(prepared *PreparedRuntime) (*Runtime, error) {
	if prepared == nil || prepared.service != s || prepared.runtime == nil {
		return nil, errors.New("sessionv3: invalid prepared runtime")
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.discarded {
		return nil, errors.New("sessionv3: prepared runtime was discarded")
	}
	if prepared.published {
		return prepared.runtime, nil
	}
	candidate := prepared.runtime
	ref := candidate.ref
	s.mu.Lock()
	if current := s.active[ref]; current != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrSessionExists, ref.SessionID)
	}
	delete(s.closed, ref)
	s.active[ref] = candidate
	s.revision.Add(1)
	s.mu.Unlock()
	prepared.published = true
	return candidate, nil
}

// Discard closes an unpublished candidate and releases its writer lease.
// Published runtimes must be closed through Service.Close so exact-instance
// unregistering cannot be bypassed.
func (s *Service) Discard(ctx context.Context, prepared *PreparedRuntime) error {
	if prepared == nil || prepared.service != s || prepared.runtime == nil {
		return nil
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.published {
		return errors.New("sessionv3: published runtime cannot be discarded")
	}
	if prepared.discarded {
		return prepared.runtime.closeErr
	}
	prepared.discarded = true
	return prepared.runtime.close(ctx)
}

func (s *Service) Open(ctx context.Context, ref SessionRef) (*Runtime, error) {
	if err := ref.validate(s.hostID); err != nil {
		return nil, err
	}
	for {
		s.mu.Lock()
		if current := s.active[ref]; current != nil {
			s.mu.Unlock()
			return current, nil
		}
		if pending := s.preparing[ref]; pending != nil {
			done := pending.done
			s.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		pending := &prepareRuntime{done: make(chan struct{})}
		s.preparing[ref] = pending
		s.mu.Unlock()
		break
	}

	handle, err := s.persistence.Open(ref.SessionID, ReadWrite)
	if err != nil {
		s.finishPrepare(ref)
		return nil, err
	}
	store, ok := handle.(WritableSessionHandle)
	if !ok {
		_ = handle.Close(context.Background())
		s.finishPrepare(ref)
		return nil, errors.New("sessionv3: writable persistence did not return a live store")
	}
	if _, recovered, recoverErr := store.RecoverInterrupted(ctx); recoverErr != nil {
		_ = handle.Close(context.Background())
		s.finishPrepare(ref)
		return nil, fmt.Errorf("sessionv3: close interrupted runtime: %w", recoverErr)
	} else if recovered {
		// Recovery is a persisted fact. The fresh runtime begins idle and never
		// resurrects the prior process's activity or pending authorization.
	}
	candidate := newRuntime(ref, &Session{Handle: store})
	s.mu.Lock()
	pending := s.preparing[ref]
	delete(s.preparing, ref)
	if current := s.active[ref]; current != nil {
		if pending != nil {
			close(pending.done)
		}
		s.mu.Unlock()
		_ = candidate.close(context.Background())
		return current, nil
	}
	delete(s.closed, ref)
	s.active[ref] = candidate
	s.revision.Add(1)
	if pending != nil {
		close(pending.done)
	}
	s.mu.Unlock()
	return candidate, nil
}

func (s *Service) finishPrepare(ref SessionRef) {
	s.mu.Lock()
	if pending := s.preparing[ref]; pending != nil {
		delete(s.preparing, ref)
		close(pending.done)
	}
	s.mu.Unlock()
}

func (s *Service) Runtime(ref SessionRef) (*Runtime, bool) {
	if ref.validate(s.hostID) != nil {
		return nil, false
	}
	s.mu.Lock()
	runtime := s.active[ref]
	s.mu.Unlock()
	return runtime, runtime != nil
}

func (s *Service) Cancel(ref SessionRef) (RuntimeSnapshot, error) {
	runtime, ok := s.Runtime(ref)
	if !ok {
		return RuntimeSnapshot{}, ErrSessionNotRunning
	}
	runtime.Cancel()
	return runtime.Snapshot(), nil
}

// CancelSession is the public session-scoped Stop contract. A missing runtime
// is already idle and therefore succeeds idempotently; no caller-supplied turn
// id participates in routing or authorization.
func (s *Service) CancelSession(ref SessionRef) (CancelReceipt, error) {
	if err := ref.validate(s.hostID); err != nil {
		return CancelReceipt{}, err
	}
	runtime, ok := s.Runtime(ref)
	if !ok {
		return CancelReceipt{Ref: ref, Accepted: true, Phase: RuntimeIdle}, nil
	}
	runtime.Cancel()
	snapshot := runtime.Snapshot()
	return CancelReceipt{Ref: ref, Accepted: true, RuntimeEpoch: snapshot.Epoch, ActivityRevision: snapshot.ActivityRevision, Phase: snapshot.Phase}, nil
}

func (s *Service) Flush(ctx context.Context, ref SessionRef) (DurableReceipt, error) {
	runtime, ok := s.Runtime(ref)
	if !ok {
		return DurableReceipt{}, ErrSessionNotRunning
	}
	return runtime.session.Flush(ctx)
}

// ContinueLegacy freezes one legacy head, publishes its deterministic final
// session, then attaches that exact session. It never writes the source and it
// does not accept the caller's pending submission; hosts enqueue the unchanged
// submission only after this method returns the new immutable identity.
func (s *Service) ContinueLegacy(ctx context.Context, sourcePath, headID string) (*Runtime, MigrationResult, error) {
	filesystem, ok := s.persistence.(*FilesystemPersistence)
	if !ok {
		return nil, MigrationResult{}, errors.New("sessionv3: persistence does not support legacy migration")
	}
	result, err := migrateLegacyHeadForHost(ctx, sourcePath, filesystem.Root, headID)
	if err != nil {
		return nil, result, err
	}
	runtime, err := s.Open(ctx, SessionRef{HostID: s.hostID, SessionID: result.TargetID})
	return runtime, result, err
}

// ContinuePrototype is the explicit, fail-closed bridge for the retired
// sidecar codec. Unknown required events or conflicting tails remain read-only.
func (s *Service) ContinuePrototype(ctx context.Context, sourceDir string) (*Runtime, PrototypeImportResult, error) {
	filesystem, ok := s.persistence.(*FilesystemPersistence)
	if !ok {
		return nil, PrototypeImportResult{}, errors.New("sessionv3: persistence does not support prototype import")
	}
	result, err := ImportPrototype(ctx, sourceDir, filesystem.Root)
	if err != nil {
		return nil, result, err
	}
	runtime, err := s.Open(ctx, SessionRef{HostID: s.hostID, SessionID: result.TargetID})
	return runtime, result, err
}

// Fork creates an independent child at the exact end event of a completed
// turn. No message-count inference is involved.
func (s *Service) Fork(ctx context.Context, ref SessionRef, afterTurnID, childID string) (*Runtime, error) {
	runtime, ok := s.Runtime(ref)
	if !ok {
		return nil, ErrSessionNotRunning
	}
	turn, ok := completedTurn(runtime.session.Snapshot().Projection.Turns, afterTurnID)
	if !ok {
		return nil, fmt.Errorf("sessionv3: completed turn %q not found", afterTurnID)
	}
	return s.forkAt(ctx, runtime, turn.EndSequence, childID)
}

// Rewind creates a child from the event immediately before beforeTurnID.
func (s *Service) Rewind(ctx context.Context, ref SessionRef, beforeTurnID, childID string) (*Runtime, error) {
	runtime, ok := s.Runtime(ref)
	if !ok {
		return nil, ErrSessionNotRunning
	}
	turn, ok := completedTurn(runtime.session.Snapshot().Projection.Turns, beforeTurnID)
	if !ok {
		return nil, fmt.Errorf("sessionv3: completed turn %q not found", beforeTurnID)
	}
	return s.forkAt(ctx, runtime, turn.StartSequence-1, childID)
}

func completedTurn(turns []TurnBoundary, id string) (TurnBoundary, bool) {
	for _, turn := range turns {
		if turn.TurnID == id {
			return turn, true
		}
	}
	return TurnBoundary{}, false
}

func (s *Service) forkAt(ctx context.Context, parent *Runtime, sequence uint64, childID string) (*Runtime, error) {
	filesystem, ok := s.persistence.(*FilesystemPersistence)
	if !ok {
		return nil, errors.New("sessionv3: persistence does not support filesystem fork")
	}
	if childID == "" {
		childID = randomID()
	}
	if err := validateSessionID(childID); err != nil {
		return nil, err
	}
	childDir := filepath.Join(filesystem.Root, childID)
	if _, err := parent.session.Handle.Fork(ctx, childDir, childID, sequence); err != nil {
		return nil, err
	}
	return s.Open(ctx, SessionRef{HostID: s.hostID, SessionID: childID})
}

func (s *Service) Close(ctx context.Context, ref SessionRef) error {
	if err := ref.validate(s.hostID); err != nil {
		return err
	}
	s.mu.Lock()
	runtime := s.active[ref]
	if runtime == nil {
		runtime = s.closed[ref]
	}
	s.mu.Unlock()
	if runtime == nil {
		return ErrSessionNotRunning
	}
	err := runtime.close(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.active[ref] == runtime {
		delete(s.active, ref)
		s.closed[ref] = runtime
		s.revision.Add(1)
	}
	s.mu.Unlock()
	return nil
}

// Detach removes a runtime only if it is still the exact published instance.
// It is used by host callbacks that may arrive after a replacement.
func (s *Service) Detach(runtime *Runtime) bool {
	if runtime == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[runtime.ref] != runtime {
		return false
	}
	delete(s.active, runtime.ref)
	s.closed[runtime.ref] = runtime
	s.revision.Add(1)
	return true
}

type ObserveResult struct {
	Runtime *RuntimeSnapshot `json:"runtime,omitempty"`
	Events  EventPage        `json:"events"`
}

func (s *Service) Observe(ctx context.Context, ref SessionRef, cursor uint64, limit int) (ObserveResult, error) {
	if err := ref.validate(s.hostID); err != nil {
		return ObserveResult{}, err
	}
	if runtime, ok := s.Runtime(ref); ok {
		snapshot := runtime.Snapshot()
		page, err := runtime.session.Handle.AcceptedPage(ctx, cursor, limit)
		return ObserveResult{Runtime: &snapshot, Events: page}, err
	}
	handle, err := s.persistence.Open(ref.SessionID, ReadOnly)
	if err != nil {
		return ObserveResult{}, err
	}
	defer handle.Close(context.Background())
	page, err := handle.Read(ctx, cursor, limit)
	return ObserveResult{Events: page}, err
}
