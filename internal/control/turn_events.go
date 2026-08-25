package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
	"reasonix/internal/turnevent"
)

// turnEventSink persists lifecycle envelopes before frontend publication.
// Provider-facing transcript messages remain a separate artifact.
type turnEventSink struct {
	event.AuditForwarder
	inner event.Sink
	c     *Controller
}

// turnEventState has an independent lock so ledger I/O never holds c.mu.
type turnEventState struct {
	mu     sync.RWMutex
	ledger *turnevent.Ledger
	err    error
}

func newTurnEventSink(inner event.Sink, c *Controller) *turnEventSink {
	return &turnEventSink{AuditForwarder: event.AuditForwarder{Inner: inner}, inner: inner, c: c}
}

func (s *turnEventSink) InboxChanged(snap sessioninbox.InboxSnapshot) {
	if s != nil {
		notifyInboxChanged(s.inner, snap)
	}
}

var _ event.OptionalSinkCapabilities = (*turnEventSink)(nil)

func (s *turnEventSink) Emit(e event.Event) {
	if err := s.emitChecked(e); err != nil {
		// Ordinary event.Sink has no error return. Admission uses emitChecked
		// directly and therefore fails closed before the provider starts; later
		// runtime failures are surfaced and cancel-safe through the controller.
		slog.Error("controller: append turn event ledger", "err", err, "turn_id", e.TurnID, "kind", e.Kind)
		if e.Kind == event.TurnDone {
			e.Err = errors.Join(e.Err, err)
			e.Status = event.TurnFailed
		}
		if s != nil && s.inner != nil {
			s.inner.Emit(e)
		}
	}
}

// emitChecked persists before publish and returns durability failures to the
// admission boundary. It also suppresses the executor's duplicate TurnStarted
// because the controller has already committed that transition before the
// provider goroutine is launched.
func (s *turnEventSink) emitChecked(e event.Event) error {
	if s == nil || s.c == nil {
		return nil
	}
	ledger := s.c.turnEventLedger()
	if ledger == nil {
		if s.inner != nil {
			s.inner.Emit(e)
		}
		return nil
	}
	// Outside-turn notices are not lifecycle records and must pass through after
	// bootstrap or a terminal event.
	if ledger.ActiveTurnID() == "" {
		if s.inner != nil {
			s.inner.Emit(e)
		}
		return nil
	}
	if e.Kind == event.TurnStarted && ledger.CurrentStatus() == event.TurnInProgress {
		return nil
	}
	status := e.Status
	if status == "" {
		status = ledger.CurrentStatus()
	}
	switch e.Kind {
	case event.TurnStarted:
		status = event.TurnInProgress
	case event.AskRequest, event.ApprovalRequest:
		status = event.TurnWaitingUser
	case event.TurnDone:
		status = terminalTurnStatus(e)
		if s.c.executor != nil && s.c.executor.Session() != nil {
			session := s.c.executor.Session()
			digest, digestErr := session.ContentDigest()
			if digestErr != nil {
				slog.Warn("controller: compute terminal transcript digest", "err", digestErr)
			} else {
				ledger.SetTranscriptSnapshot(int64(session.TranscriptVersion()), digest)
			}
		}
	case event.TurnStatusChanged:
		// The emitter supplied the exact transition in e.Status.
	}
	stamped, ok, err := ledger.Append(e, status)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if s.inner != nil {
		s.inner.Emit(stamped)
	}
	return nil
}

func terminalTurnStatus(e event.Event) event.TurnStatus {
	if e.Cancelled || errors.Is(e.Err, context.Canceled) {
		return event.TurnInterrupted
	}
	if agent.IsProtocolFailed(e.Err) {
		return event.TurnProtocolFailed
	}
	if e.Err != nil {
		return event.TurnFailed
	}
	return event.TurnCompleted
}

func (c *Controller) turnEventLedger() *turnevent.Ledger {
	if c == nil {
		return nil
	}
	c.turnEvents.mu.RLock()
	defer c.turnEvents.mu.RUnlock()
	return c.turnEvents.ledger
}

func (c *Controller) turnEventLedgerError() error {
	if c == nil {
		return nil
	}
	c.turnEvents.mu.RLock()
	defer c.turnEvents.mu.RUnlock()
	return c.turnEvents.err
}

func (c *Controller) prepareTurnAdmission(body func(context.Context) error) func(context.Context) error {
	admissionErr := c.turnEventLedgerError()
	if ledger := c.turnEventLedger(); admissionErr == nil && ledger != nil {
		if _, err := ledger.Begin(); err != nil {
			admissionErr = err
		} else if err := c.emitTurnEventChecked(event.Event{Kind: event.TurnStatusChanged, Status: event.TurnQueued}); err != nil {
			admissionErr = err
		} else if err := c.emitTurnEventChecked(event.Event{Kind: event.TurnStarted, Status: event.TurnInProgress}); err != nil {
			admissionErr = err
		}
	}
	if admissionErr == nil {
		return body
	}
	slog.Error("controller: persist turn admission", "err", admissionErr)
	return func(context.Context) error { return fmt.Errorf("persist turn admission: %w", admissionErr) }
}

func (c *Controller) applyTurnDoneProtocol(done event.Event, cancelRequested bool) event.Event {
	if cancelRequested {
		// Interruption is a terminal state, not a send failure; partial text is
		// already display-only by this point.
		done.Err = nil
	}
	if done.Outcome == "" && c.executor != nil {
		done.Outcome = string(c.executor.TurnFinishOutcome())
	}
	return done
}

func (c *Controller) turnEventRuntimeStatus() (string, event.TurnStatus, uint64, uint64) {
	ledger := c.turnEventLedger()
	if ledger == nil {
		return "", "", 0, 0
	}
	latest, replayAfter := ledger.ProjectionCursor()
	return ledger.ActiveTurnID(), ledger.CurrentStatus(), latest, replayAfter
}

func (c *Controller) rebindTurnEvents(sessionPath string) {
	if c == nil {
		return
	}
	ledger, err := turnevent.Open(sessionPath, agent.BranchID(sessionPath))
	if err != nil {
		slog.Warn("controller: open turn event ledger", "err", err, "session", agent.BranchID(sessionPath))
		c.turnEvents.mu.Lock()
		c.turnEvents.ledger = nil
		c.turnEvents.err = err
		c.turnEvents.mu.Unlock()
		return
	}
	c.turnEvents.mu.Lock()
	c.turnEvents.ledger = ledger
	c.turnEvents.err = nil
	c.turnEvents.mu.Unlock()
}

func (c *Controller) emitTurnStatus(status event.TurnStatus) {
	if c == nil || status == "" {
		return
	}
	c.sink.Emit(event.Event{Kind: event.TurnStatusChanged, Status: status})
}

// emitTurnEventChecked reaches the lifecycle sink below the inbox observer so
// admission can fail closed on disk errors instead of starting an unledgered
// provider request. Lifecycle events do not participate in inbox notice logic.
func (c *Controller) emitTurnEventChecked(e event.Event) error {
	if c == nil {
		return nil
	}
	if inbox, ok := c.sink.(*inboxEventSink); ok {
		if lifecycle, ok := inbox.inner.(*turnEventSink); ok {
			return lifecycle.emitChecked(e)
		}
	}
	c.sink.Emit(e)
	return nil
}

// SetTurnEventRoutingMetadata attaches desktop routing identity to lifecycle
// envelopes only. It never changes provider-visible prompts or tool schemas.
func (c *Controller) SetTurnEventRoutingMetadata(runtimeEpoch, submissionID string) {
	if ledger := c.turnEventLedger(); ledger != nil {
		ledger.SetRoutingMetadata(runtimeEpoch, submissionID)
	}
}

// TurnEventsAfter returns the durable lifecycle suffix used by reconnecting
// frontends to close sequence gaps.
func (c *Controller) TurnEventsAfter(after uint64) ([]turnevent.Envelope, error) {
	ledger := c.turnEventLedger()
	if ledger == nil {
		return []turnevent.Envelope{}, nil
	}
	return ledger.EventsAfter(after)
}

// TurnIDForSubmission exposes the synchronous admission receipt without
// depending on whether the provider is still running when Wails returns.
func (c *Controller) TurnIDForSubmission(submissionID string) string {
	ledger := c.turnEventLedger()
	if ledger == nil {
		return ""
	}
	return ledger.TurnIDForSubmission(submissionID)
}
