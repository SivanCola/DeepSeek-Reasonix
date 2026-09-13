package control

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/extension"
	"reasonix/internal/extension/dispatch"
	"reasonix/internal/provider"
	"reasonix/internal/sessionv3"
)

// BindFreshV3 creates and publishes a fresh identity-bound session. The caller
// may provide an id allocated by its protocol; an empty id lets persistence
// allocate one. Publication happens only after the initial event batch is
// accepted, so failure leaves the currently-bound session usable.
func (c *Controller) BindFreshV3(ctx context.Context, sessionID string) (sessionv3.SessionRef, error) {
	service, _, _ := c.v3Binding()
	if c == nil || service == nil || c.executor == nil {
		return sessionv3.SessionRef{}, errors.New("v3 session service is unavailable")
	}
	prepared, err := service.PrepareCreate(ctx, sessionv3.CreateOptions{SessionID: sessionID})
	if err != nil {
		return sessionv3.SessionRef{}, err
	}
	candidate := prepared.Runtime()
	fresh := agent.NewSession(c.basePrompt())
	if err := seedRuntimeSession(ctx, candidate, "session-create", fresh.Snapshot(), c.ModelRef(), c.ModelSelectionIdentity()); err != nil {
		_ = service.Discard(context.Background(), prepared)
		return sessionv3.SessionRef{}, err
	}
	if _, err := candidate.Session().Flush(ctx); err != nil {
		_ = service.Discard(context.Background(), prepared)
		return sessionv3.SessionRef{}, err
	}
	if _, err := service.Publish(prepared); err != nil {
		_ = service.Discard(context.Background(), prepared)
		return sessionv3.SessionRef{}, err
	}
	old, err := c.publishV3Runtime(candidate, fresh, true)
	if err != nil {
		_ = service.CloseRuntime(context.Background(), candidate)
		return sessionv3.SessionRef{}, err
	}
	if old != nil && old != candidate {
		if closeErr := service.CloseRuntime(context.Background(), old); closeErr != nil {
			return candidate.Ref(), fmt.Errorf("new v3 session published; close previous runtime: %w", closeErr)
		}
	}
	return candidate.Ref(), nil
}

// ContinueLegacyV3 freezes and migrates the selected legacy head, then
// publishes the returned immutable v3 identity. The source remains only as a
// display/import locator and is never rebound as the execution store.
func (c *Controller) ContinueLegacyV3(ctx context.Context, sourcePath, headID string) (sessionv3.SessionRef, error) {
	return c.continueLegacyV3(ctx, sourcePath, headID, true)
}

// ContinueLegacyV3ForRebuild performs the same fail-atomic import while an
// Agent generation is being replaced for the same logical session. The
// SessionTemp generation belongs to that logical session, so this path must
// not rotate it merely because persistence crossed the legacy/v3 boundary.
func (c *Controller) ContinueLegacyV3ForRebuild(ctx context.Context, sourcePath, headID string) (sessionv3.SessionRef, error) {
	return c.continueLegacyV3(ctx, sourcePath, headID, false)
}

func (c *Controller) continueLegacyV3(ctx context.Context, sourcePath, headID string, rotateSessionTemp bool) (sessionv3.SessionRef, error) {
	service, _, _ := c.v3Binding()
	if c == nil || service == nil || c.executor == nil {
		return sessionv3.SessionRef{}, errors.New("v3 session service is unavailable")
	}
	candidate, _, err := service.ContinueLegacy(ctx, sourcePath, headID)
	if err != nil {
		return sessionv3.SessionRef{}, err
	}
	if err := seedRuntimeConfig(ctx, candidate, "legacy-import-config", c.ModelRef(), c.ModelSelectionIdentity()); err != nil {
		_ = service.CloseRuntime(context.Background(), candidate)
		return sessionv3.SessionRef{}, err
	}
	messages := candidate.Session().Snapshot().Projection.ModelMessages
	prepared := agent.NewSession("").CloneWithMessages(messages)
	old, err := c.publishV3Runtime(candidate, prepared, rotateSessionTemp)
	if err != nil {
		_ = service.CloseRuntime(context.Background(), candidate)
		return sessionv3.SessionRef{}, err
	}
	if old != nil && old != candidate {
		if closeErr := service.CloseRuntime(context.Background(), old); closeErr != nil {
			return candidate.Ref(), fmt.Errorf("migrated v3 session published; close previous runtime: %w", closeErr)
		}
	}
	return candidate.Ref(), nil
}

// ContinuePrototypeV3 imports the retired sidecar codec through the restricted
// fail-closed bridge, then publishes the final linear session identity.
func (c *Controller) ContinuePrototypeV3(ctx context.Context, sourceDir string) (sessionv3.SessionRef, error) {
	service, _, _ := c.v3Binding()
	if c == nil || service == nil || c.executor == nil {
		return sessionv3.SessionRef{}, errors.New("v3 session service is unavailable")
	}
	candidate, _, err := service.ContinuePrototype(ctx, sourceDir)
	if err != nil {
		return sessionv3.SessionRef{}, err
	}
	if err := seedRuntimeConfig(ctx, candidate, "prototype-import-config", c.ModelRef(), c.ModelSelectionIdentity()); err != nil {
		_ = service.CloseRuntime(context.Background(), candidate)
		return sessionv3.SessionRef{}, err
	}
	prepared := agent.NewSession("").CloneWithMessages(candidate.Session().Snapshot().Projection.ModelMessages)
	old, err := c.publishV3Runtime(candidate, prepared, true)
	if err != nil {
		_ = service.CloseRuntime(context.Background(), candidate)
		return sessionv3.SessionRef{}, err
	}
	if old != nil && old != candidate {
		if closeErr := service.CloseRuntime(context.Background(), old); closeErr != nil {
			return candidate.Ref(), fmt.Errorf("imported v3 session published; close previous runtime: %w", closeErr)
		}
	}
	return candidate.Ref(), nil
}

// OpenV3 attaches this Controller to an existing immutable session identity.
// Opening never creates a missing session and publication retains the current
// binding until the target projection and writer are ready.
func (c *Controller) OpenV3(ctx context.Context, ref sessionv3.SessionRef) (sessionv3.SessionRef, error) {
	service, current, _ := c.v3Binding()
	if c == nil || service == nil || c.executor == nil {
		return sessionv3.SessionRef{}, errors.New("v3 session service is unavailable")
	}
	if current != nil && current.Ref() == ref {
		return ref, nil
	}
	candidate, err := service.Open(ctx, ref)
	if err != nil {
		return sessionv3.SessionRef{}, err
	}
	prepared := agent.NewSession("").CloneWithMessages(candidate.Session().Snapshot().Projection.ModelMessages)
	old, err := c.publishV3Runtime(candidate, prepared, true)
	if err != nil {
		// A newly opened candidate is safe to close only when it was not the
		// currently published controller runtime.
		if current == nil || candidate != current {
			_ = service.CloseRuntime(context.Background(), candidate)
		}
		return sessionv3.SessionRef{}, err
	}
	if old != nil && old != candidate {
		if closeErr := service.CloseRuntime(context.Background(), old); closeErr != nil {
			return candidate.Ref(), fmt.Errorf("v3 session published; close previous runtime: %w", closeErr)
		}
	}
	return candidate.Ref(), nil
}

// SetSessionTitleV3 records mutable title state in the canonical event stream.
func (c *Controller) SetSessionTitleV3(ctx context.Context, title string) error {
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return sessionv3.ErrSessionNotRunning
	}
	payload, err := json.Marshal(map[string]string{"title": title})
	if err != nil {
		return err
	}
	snapshot := runtime.Session().Snapshot()
	_, err = c.appendV3Batch(ctx, runtime.Session().Handle, sessionv3.Batch{
		OperationID: "session-title:" + agent.NewMessageID(),
		TurnID:      snapshot.Projection.TurnID,
		Events:      []sessionv3.Event{{Kind: "session/title", Payload: payload}},
	})
	return err
}

func seedRuntimeSession(ctx context.Context, runtime *sessionv3.Runtime, operationID string, messages []provider.Message, modelRef, modelIdentity string) error {
	if runtime == nil {
		return nil
	}
	events := make([]sessionv3.Event, 0, len(messages)+1)
	for _, message := range messages {
		if message.ID == "" {
			return errors.New("initial v3 message has no stable id")
		}
		payload, err := json.Marshal(map[string]any{"message": message})
		if err != nil {
			return err
		}
		events = append(events, sessionv3.Event{Kind: "message/complete", Payload: payload})
	}
	if strings.TrimSpace(modelRef) != "" {
		config, err := sessionConfigEvent(modelRef, modelIdentity)
		if err != nil {
			return err
		}
		events = append(events, config)
	}
	if len(events) == 0 {
		return nil
	}
	_, err := runtime.Session().AppendBatch(ctx, operationID, events)
	return err
}

func seedRuntimeConfig(ctx context.Context, runtime *sessionv3.Runtime, operationID, modelRef, modelIdentity string) error {
	if runtime == nil || strings.TrimSpace(modelRef) == "" {
		return nil
	}
	event, err := sessionConfigEvent(modelRef, modelIdentity)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(event.Payload)
	_, err = runtime.Session().AppendBatch(ctx, fmt.Sprintf("%s:%x", operationID, digest[:16]), []sessionv3.Event{event})
	return err
}

func sessionConfigEvent(modelRef, modelIdentity string) (sessionv3.Event, error) {
	payload, err := json.Marshal(map[string]string{"modelRef": modelRef, "modelIdentity": modelIdentity})
	if err != nil {
		return sessionv3.Event{}, err
	}
	return sessionv3.Event{Kind: "session/config", Payload: payload}, nil
}

func (c *Controller) publishV3Runtime(candidate *sessionv3.Runtime, prepared *agent.Session, rotateSessionTemp bool) (*sessionv3.Runtime, error) {
	if candidate == nil || prepared == nil {
		return nil, errors.New("v3 runtime publication candidate is unavailable")
	}
	service, _, _ := c.v3Binding()
	if service == nil {
		return nil, errors.New("v3 session service is unavailable")
	}
	if current, ok := service.Runtime(candidate.Ref()); !ok || current != candidate {
		return nil, errors.New("v3 runtime candidate is not the exact published service instance")
	}
	projection := candidate.Session().Snapshot().Projection
	if err := validateV3DomainProjection(projection); err != nil {
		return nil, err
	}
	c.snapshotMu.Lock()
	defer c.snapshotMu.Unlock()
	c.v3BindingMu.Lock()
	old := c.sessionRuntime
	c.sessionRuntime = candidate
	c.v3Exclusive = true
	c.v3BindingMu.Unlock()
	c.mu.Lock()
	// Legacy paths are import inputs only. Retaining one as the live path lets
	// unrelated compatibility helpers recreate sidecars beside a read-only
	// source. The immutable SessionRef is the sole execution identity.
	c.sessionPath = ""
	c.mu.Unlock()
	c.executor.SetSession(prepared)
	if err := c.restoreV3DomainProjection(projection); err != nil {
		// The candidate remains published by SessionService, but the Controller
		// must not expose a partially restored execution view. The caller closes
		// the candidate and retains the old binding on this error path.
		return nil, err
	}
	// Transcript pages are a derived cache. A session switch invalidates the
	// prior identity immediately; the next query rebuilds from the exact v3
	// projection without reading or writing a legacy sidecar.
	c.turnEvents.mu.Lock()
	c.turnEvents.projection = nil
	c.turnEvents.projectionErr = nil
	c.turnEvents.v3ProjectionSequence = 0
	c.turnEvents.v3ProjectionSession = ""
	c.turnEvents.v3ProjectionEpoch = ""
	c.turnEvents.mu.Unlock()
	c.rebindCheckpoints("")
	c.ResetPlannerSession()
	// The inbox belongs to the live runtime generation, not to the imported
	// legacy path. Close the pre-bind queue before rotating the session temp so
	// later Agent rebuilds attach to the same current generation.
	c.pauseInboxOnRotate()
	if rotateSessionTemp {
		c.rotateSessionTemp()
	}
	c.rebindInbox()
	c.refreshRuntimeState(event.Event{})
	return old, nil
}

func validateV3DomainProjection(projection sessionv3.Projection) error {
	if len(projection.PlanState) > 0 {
		var plan struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal(projection.PlanState, &plan); err != nil {
			return fmt.Errorf("restore v3 plan state: %w", err)
		}
	}
	if len(projection.GoalState) > 0 {
		var goal goalState
		if err := json.Unmarshal(projection.GoalState, &goal); err != nil {
			return fmt.Errorf("restore v3 goal state: %w", err)
		}
	}
	return nil
}

func (c *Controller) restoreV3DomainProjection(projection sessionv3.Projection) error {
	var plan struct {
		Enabled bool `json:"enabled"`
	}
	if len(projection.PlanState) > 0 {
		if err := json.Unmarshal(projection.PlanState, &plan); err != nil {
			return fmt.Errorf("restore v3 plan state: %w", err)
		}
	}
	c.mu.Lock()
	c.sessionSettings.planMode = plan.Enabled
	c.mu.Unlock()
	if setter, ok := c.runner.(interface{ SetPlanMode(bool) }); ok {
		setter.SetPlanMode(plan.Enabled)
	} else if c.executor != nil {
		c.executor.SetPlanMode(plan.Enabled)
	}
	if err := c.goals.restoreGoalEvent(projection.GoalState); err != nil {
		return fmt.Errorf("restore v3 goal state: %w", err)
	}
	if c.executor != nil {
		c.executor.RestoreDeliveryCheckpoint(c.goals.deliveryState())
	}
	return nil
}

func (c *Controller) v3Binding() (*sessionv3.Service, *sessionv3.Runtime, bool) {
	if c == nil {
		return nil, nil, false
	}
	c.v3BindingMu.RLock()
	service, runtime, exclusive := c.sessionService, c.sessionRuntime, c.v3Exclusive
	c.v3BindingMu.RUnlock()
	return service, runtime, exclusive
}

// SessionV3Binding exposes the host-owned service/runtime pair for an Agent
// rebuild. Callers must attach the pair to the replacement Controller; they
// must not close or republish the writer themselves.
func (c *Controller) SessionV3Binding() (*sessionv3.Service, *sessionv3.Runtime, bool) {
	service, runtime, exclusive := c.v3Binding()
	return service, runtime, exclusive && service != nil && runtime != nil
}

// SessionV3Service exposes the host query/management owner without requiring
// an active runtime. Cold history listing must not create an Agent or writer.
func (c *Controller) SessionV3Service() *sessionv3.Service {
	service, _, exclusive := c.v3Binding()
	if !exclusive {
		return nil
	}
	return service
}

// UsesExclusiveSessionV3 reports the configured execution contract even when
// a lazy fresh session has not yet been allocated. Hosts use it to avoid
// manufacturing a legacy path during rebuild preparation.
func (c *Controller) UsesExclusiveSessionV3() bool {
	service, _, exclusive := c.v3Binding()
	return exclusive && service != nil
}

func (c *Controller) exclusiveV3Enabled() bool {
	_, _, exclusive := c.v3Binding()
	return exclusive
}

// rotateExclusiveV3Session implements /new and /clear without allocating a
// legacy transcript path. clear additionally deletes the closed source v3
// directory; new leaves it available in history.
func (c *Controller) rotateExclusiveV3Session(clear bool) error {
	service, runtime, _ := c.v3Binding()
	if service == nil || runtime == nil {
		return errors.New("exclusive v3 session runtime is unavailable")
	}
	oldRef := runtime.Ref()
	if err := c.Snapshot(); err != nil {
		return err
	}
	reason := "new"
	if clear {
		reason = "clear"
	}
	if err := c.extensionSessionPhase(context.Background(), extension.PointSessionRotate, dispatch.PhaseRotate, oldRef.SessionID); err != nil {
		return err
	}
	c.hooks.SessionEnd(context.Background(), reason)
	c.extensionSessionEvent(extension.PointSessionEnd, dispatch.PhaseEnd, oldRef.SessionID)
	ref, err := c.BindFreshV3(context.Background(), "")
	if err != nil {
		return err
	}
	if clear {
		if err := service.Delete(context.Background(), oldRef); err != nil {
			return fmt.Errorf("new session %s is active; delete cleared session: %w", ref.SessionID, err)
		}
	}
	c.ClearGoal()
	c.mu.Lock()
	c.startedOnce = true
	c.mu.Unlock()
	c.hooks.SetSessionID(ref.SessionID)
	c.enqueueHookContexts(c.hooks.SessionStart(context.Background(), reason))
	c.extensionSessionEvent(extension.PointSessionStart, dispatch.PhaseStart, ref.SessionID)
	c.clearSessionWriteAccess()
	return nil
}
