package daemon

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

func (d *Daemon) enqueueIntent(intent RunIntent) {
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = time.Now().UTC()
	}
	if intent.Source == "" {
		intent.Source = "daemon"
	}
	select {
	case d.intentCh <- intent:
	default:
		d.logger.Warn("daemon intent queue full", "session", intent.SessionID, "source", intent.Source)
	}
}

func (d *Daemon) runIntentWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case intent := <-d.intentCh:
			go d.executeIntent(ctx, intent)
		}
	}
}

func (d *Daemon) executeIntent(parent context.Context, intent RunIntent) {
	d.mu.Lock()
	entry, ok := d.registry[intent.SessionID]
	if !ok {
		d.mu.Unlock()
		return
	}
	if _, exists := d.activeRuns[intent.SessionID]; exists {
		d.mu.Unlock()
		return
	}
	entryCopy := *entry
	ctx, cancel := context.WithCancel(parent)
	active := &ActiveRun{
		Intent:    intent,
		StartedAt: time.Now().UTC(),
		Cancel:    cancel,
		Approvals: make(map[string]event.Approval),
		Asks:      make(map[string]event.Ask),
	}
	d.activeRuns[intent.SessionID] = active
	d.mu.Unlock()

	sink := event.Sync(event.FuncSink(func(e event.Event) {
		d.handleRunEvent(intent.SessionID, e)
	}))
	ctrl, err := d.buildController(ctx, d, &entryCopy, sink)
	if err != nil {
		cancel()
		d.finishIntent(intent.SessionID, "failed", err)
		return
	}
	d.mu.Lock()
	active.Control = ctrl
	d.mu.Unlock()
	defer ctrl.Close()
	defer cancel()

	err = ctrl.ContinueGoal(ctx, firstNonEmpty(intent.Reason, intent.Source))
	if err != nil && ctx.Err() == nil {
		d.finishIntent(intent.SessionID, "failed", err)
		return
	}
	d.finishIntent(intent.SessionID, "", err)
}

func defaultControllerFactory(ctx context.Context, d *Daemon, entry *SessionEntry, sink event.Sink) (*control.Controller, error) {
	sess, err := agent.LoadSession(entry.Path)
	if err != nil {
		return nil, err
	}
	workspaceRoot := strings.TrimSpace(entry.Runtime.WorkspaceRoot)
	if workspaceRoot == "" {
		if meta, ok, err := agent.LoadBranchMeta(entry.Path); err == nil && ok {
			workspaceRoot = meta.WorkspaceRoot
		}
	}
	ctrl, err := boot.Build(ctx, boot.Options{
		Model:         entry.Runtime.Model,
		RequireKey:    true,
		Sink:          sink,
		WorkspaceRoot: workspaceRoot,
		SessionDir:    d.sessionDir,
	})
	if err != nil {
		return nil, err
	}
	ctrl.EnableInteractiveApproval()
	ctrl.Resume(sess, entry.Path)
	return ctrl, nil
}

func (d *Daemon) handleRunEvent(sessionID string, e event.Event) {
	switch e.Kind {
	case event.ApprovalRequest:
		d.recordWait(sessionID, agent.RuntimeWaitMeta{
			Kind:       "approval",
			Reason:     "approval required",
			ApprovalID: e.Approval.ID,
			Tool:       e.Approval.Tool,
			Subject:    e.Approval.Subject,
			Since:      time.Now().UTC(),
		}, e)
	case event.AskRequest:
		d.recordWait(sessionID, agent.RuntimeWaitMeta{
			Kind:   "ask",
			Reason: "user answer required",
			AskID:  e.Ask.ID,
			Since:  time.Now().UTC(),
		}, e)
	case event.TurnDone:
		if e.Err != nil {
			d.logger.Warn("daemon run turn finished with error", "session", sessionID, "err", e.Err)
		}
	}
}

func (d *Daemon) recordWait(sessionID string, wait agent.RuntimeWaitMeta, e event.Event) {
	d.mu.Lock()
	entry, ok := d.registry[sessionID]
	active := d.activeRuns[sessionID]
	if ok {
		entry.Runtime.Wait = wait
		entry.Runtime.Run.Status = "waiting_" + wait.Kind
	}
	if active != nil {
		if e.Kind == event.ApprovalRequest {
			active.Approvals[e.Approval.ID] = e.Approval
		}
		if e.Kind == event.AskRequest {
			active.Asks[e.Ask.ID] = e.Ask
		}
	}
	var runtime agent.RuntimeMeta
	var path string
	if ok {
		runtime = entry.Runtime
		path = entry.Path
	}
	d.mu.Unlock()
	if ok {
		if err := saveRuntimeMeta(path, runtime); err != nil {
			d.logger.Warn("daemon: persist wait state", "session", sessionID, "err", err)
		}
	}
}

func (d *Daemon) finishIntent(sessionID, fallbackStatus string, runErr error) {
	d.mu.Lock()
	entry, ok := d.registry[sessionID]
	delete(d.activeRuns, sessionID)
	var path string
	if ok {
		path = entry.Path
	}
	d.mu.Unlock()

	if !ok {
		return
	}
	if loaded, found, err := agent.LoadRuntimeMeta(path); err == nil && found {
		d.mu.Lock()
		if entry := d.registry[sessionID]; entry != nil {
			entry.Runtime = loaded
		}
		d.mu.Unlock()
		return
	}
	if fallbackStatus == "" {
		return
	}
	d.mu.Lock()
	entry = d.registry[sessionID]
	if entry != nil {
		entry.Runtime.Run.Status = fallbackStatus
		if runErr != nil {
			entry.Runtime.Run.LastError = runErr.Error()
		}
	}
	var runtime agent.RuntimeMeta
	if entry != nil {
		runtime = entry.Runtime
	}
	d.mu.Unlock()
	if entry != nil {
		if err := saveRuntimeMeta(path, runtime); err != nil {
			slog.Warn("daemon: persist finished intent", "err", err, "session", sessionID)
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
