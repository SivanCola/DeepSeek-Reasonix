package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

var startTime = time.Now()

// handleStatus returns daemon health info.
func (d *Daemon) handleStatus(w http.ResponseWriter, r *http.Request) {
	d.mu.RLock()
	sessions := len(d.registry)
	d.mu.RUnlock()

	resp := StatusResponse{
		Status:   "running",
		Addr:     d.addr,
		Sessions: sessions,
		Uptime:   time.Since(startTime).Round(time.Second).String(),
		PID:      os.Getpid(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleSessions lists all tracked sessions with their runtime state.
func (d *Daemon) handleSessions(w http.ResponseWriter, r *http.Request) {
	d.mu.RLock()
	views := make([]SessionView, 0, len(d.registry))
	for _, entry := range d.registry {
		_, active := d.activeRuns[entry.ID]
		views = append(views, SessionView{
			ID:          entry.ID,
			Path:        entry.Path,
			GoalText:    entry.Runtime.Goal.Text,
			GoalStatus:  entry.Runtime.Goal.Status,
			RunStatus:   entry.Runtime.Run.Status,
			WaitKind:    entry.Runtime.Wait.Kind,
			WaitReason:  entry.Runtime.Wait.Reason,
			WaitID:      firstNonEmpty(entry.Runtime.Wait.ApprovalID, entry.Runtime.Wait.AskID, entry.Runtime.Wait.EventID),
			WaitTool:    entry.Runtime.Wait.Tool,
			WaitSubject: entry.Runtime.Wait.Subject,
			Active:      active,
		})
	}
	d.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SessionsResponse{Sessions: views})
}

func (d *Daemon) handleTimeline(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("session")
	}
	if sessionID == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			http.Error(w, `{"error":"invalid limit"}`, http.StatusBadRequest)
			return
		}
		limit = n
	}
	d.mu.RLock()
	entry, ok := d.registry[sessionID]
	d.mu.RUnlock()
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}
	events, _, err := agent.LoadRuntimeTimeline(entry.Path, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"load timeline failed: %s"}`, err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TimelineResponse{SessionID: sessionID, Events: events})
}

// handleContinueGoal triggers goal continuation for a session.
// Body: {"session_id": "...", "reason": "bot|user|cron"}
func (d *Daemon) handleContinueGoal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		Reason    string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	if req.Reason == "" {
		req.Reason = "api"
	}

	d.mu.RLock()
	entry, ok := d.registry[req.SessionID]
	d.mu.RUnlock()
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	d.mu.Lock()
	if _, running := d.activeRuns[req.SessionID]; running {
		d.mu.Unlock()
		http.Error(w, `{"error":"session already running"}`, http.StatusConflict)
		return
	}
	entry.Runtime.Run.Status = "pending_continue"
	entry.Runtime.Run.LastWakeupReason = req.Reason
	entry.Runtime.Run.ResumeCount++
	entry.Runtime.Wait = agent.RuntimeWaitMeta{}
	runtime := entry.Runtime
	path := entry.Path
	d.mu.Unlock()

	if err := saveRuntimeMeta(path, runtime); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, err), http.StatusInternalServerError)
		return
	}
	d.enqueueIntent(RunIntent{
		SessionID:   req.SessionID,
		SessionPath: path,
		Source:      "api",
		Reason:      req.Reason,
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"session_id":%q,"status":"pending_continue"}`, req.SessionID)
}

// handleStop stops a running session's goal.
// Body: {"session_id": "..."}
func (d *Daemon) handleStop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}

	d.mu.Lock()
	entry, ok := d.registry[req.SessionID]
	active := d.activeRuns[req.SessionID]
	if ok {
		if active != nil && active.Cancel != nil {
			active.Cancel()
		}
		entry.Runtime.Run.Status = "stopped"
		entry.Runtime.Goal.Status = "stopped"
		entry.Runtime.Wait = agent.RuntimeWaitMeta{}
	}
	var runtime agent.RuntimeMeta
	var path string
	if ok {
		runtime = entry.Runtime
		path = entry.Path
	}
	d.mu.Unlock()
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	if err := saveRuntimeMeta(path, runtime); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, err), http.StatusInternalServerError)
		return
	}
	d.appendTimeline(path, agent.RuntimeTimelineEvent{
		Type:       "stopped",
		Source:     "api",
		RunStatus:  runtime.Run.Status,
		GoalStatus: runtime.Goal.Status,
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"session_id":%q,"status":"stopped"}`, req.SessionID)
}

// handleSchedule sets or clears a cron schedule for a session.
// Body: {"session_id": "...", "daily_at": "09:00", "interval": "1h", "enabled": true}
// All schedule fields are optional; omitted fields are left unchanged.
// Set enabled=false to disable without clearing the schedule.
func (d *Daemon) handleSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string  `json:"session_id"`
		DailyAt   *string `json:"daily_at"` // "HH:MM" or "" to clear
		Interval  *string `json:"interval"` // duration string or "" to clear
		Enabled   *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}

	d.mu.Lock()
	entry, ok := d.registry[req.SessionID]
	if !ok {
		d.mu.Unlock()
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	if req.DailyAt != nil {
		entry.Runtime.Scheduler.DailyAt = *req.DailyAt
	}
	if req.Interval != nil {
		if *req.Interval == "" {
			entry.Runtime.Scheduler.Interval = 0
		} else {
			dur, err := time.ParseDuration(*req.Interval)
			if err != nil {
				d.mu.Unlock()
				http.Error(w, fmt.Sprintf(`{"error":"invalid interval: %s"}`, err), http.StatusBadRequest)
				return
			}
			if dur < time.Second {
				d.mu.Unlock()
				http.Error(w, `{"error":"interval must be at least 1s"}`, http.StatusBadRequest)
				return
			}
			entry.Runtime.Scheduler.Interval = dur
		}
	}
	if req.Enabled != nil {
		entry.Runtime.Scheduler.Enabled = *req.Enabled
	}

	// Compute next wakeup if enabling.
	if entry.Runtime.Scheduler.Enabled && d.scheduler != nil {
		next := d.scheduler.computeNextWakeup(entry.Runtime.Scheduler, time.Now())
		entry.Runtime.Scheduler.NextWakeupAt = next
	} else if !entry.Runtime.Scheduler.Enabled {
		entry.Runtime.Scheduler.NextWakeupAt = time.Time{}
	}

	runtime := entry.Runtime
	path := entry.Path
	d.mu.Unlock()

	if err := agent.SaveRuntimeMeta(path, runtime); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"ok":             true,
		"session_id":     req.SessionID,
		"enabled":        runtime.Scheduler.Enabled,
		"daily_at":       runtime.Scheduler.DailyAt,
		"interval":       runtime.Scheduler.Interval.String(),
		"next_wakeup_at": runtime.Scheduler.NextWakeupAt.Format(time.RFC3339),
	}
	json.NewEncoder(w).Encode(resp)
}

// handleBudget sets or resets the automatic wakeup budget for a session.
// Body: {"session_id":"...","daily_wakeup_limit":10,"reset":true}
func (d *Daemon) handleBudget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID        string `json:"session_id"`
		DailyWakeupLimit *int   `json:"daily_wakeup_limit"`
		Reset            bool   `json:"reset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	if req.DailyWakeupLimit != nil && *req.DailyWakeupLimit < 0 {
		http.Error(w, `{"error":"daily_wakeup_limit must be >= 0"}`, http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	d.mu.Lock()
	entry, ok := d.registry[req.SessionID]
	if !ok {
		d.mu.Unlock()
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}
	if req.DailyWakeupLimit != nil {
		entry.Runtime.Budget.DailyWakeupLimit = *req.DailyWakeupLimit
		if entry.Runtime.Budget.WindowStartedAt.IsZero() {
			entry.Runtime.Budget.WindowStartedAt = budgetWindowStart(now)
		}
	}
	if req.Reset {
		entry.Runtime.Budget.DailyWakeups = 0
		entry.Runtime.Budget.WindowStartedAt = budgetWindowStart(now)
		entry.Runtime.Budget.LastBlockedAt = time.Time{}
		entry.Runtime.Budget.LastBlockedReason = ""
	}
	runtime := entry.Runtime
	path := entry.Path
	d.mu.Unlock()

	if err := agent.SaveRuntimeMeta(path, runtime); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, err), http.StatusInternalServerError)
		return
	}
	d.appendTimeline(path, agent.RuntimeTimelineEvent{
		Type:       "budget_configured",
		Source:     "api",
		RunStatus:  runtime.Run.Status,
		GoalStatus: runtime.Goal.Status,
		Message:    fmt.Sprintf("daily_wakeup_limit=%d daily_wakeups=%d", runtime.Budget.DailyWakeupLimit, runtime.Budget.DailyWakeups),
	})

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"ok":                 true,
		"session_id":         req.SessionID,
		"daily_wakeup_limit": runtime.Budget.DailyWakeupLimit,
		"daily_wakeups":      runtime.Budget.DailyWakeups,
		"window_started_at":  runtime.Budget.WindowStartedAt.Format(time.RFC3339),
	}
	json.NewEncoder(w).Encode(resp)
}

// handleWaitEvent sets or clears an external event wait condition.
// Body: {"session_id":"...","event_source":"github.workflow_run","event_id":"...","event_status":"completed","event_conclusion":"success","reason":"waiting for CI","subject":"PR #42","clear":false}
func (d *Daemon) handleWaitEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID       string `json:"session_id"`
		EventSource     string `json:"event_source"`
		EventID         string `json:"event_id"`
		EventStatus     string `json:"event_status"`
		EventConclusion string `json:"event_conclusion"`
		Reason          string `json:"reason"`
		Subject         string `json:"subject"`
		Clear           bool   `json:"clear"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	if !req.Clear && req.EventSource == "" && req.EventID == "" {
		http.Error(w, `{"error":"event_source or event_id required"}`, http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	d.mu.Lock()
	entry, ok := d.registry[req.SessionID]
	if !ok {
		d.mu.Unlock()
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}
	if _, active := d.activeRuns[req.SessionID]; active {
		d.mu.Unlock()
		http.Error(w, `{"error":"session already running"}`, http.StatusConflict)
		return
	}
	action := "wait_started"
	if req.Clear {
		entry.Runtime.Wait = agent.RuntimeWaitMeta{}
		if entry.Runtime.Run.Status == "waiting_event" {
			entry.Runtime.Run.Status = "idle"
		}
		action = "wait_cleared"
	} else {
		reason := req.Reason
		if reason == "" {
			reason = "waiting for external event"
		}
		entry.Runtime.Wait = agent.RuntimeWaitMeta{
			Kind:            "event",
			Reason:          reason,
			EventSource:     strings.TrimSpace(req.EventSource),
			EventID:         strings.TrimSpace(req.EventID),
			EventStatus:     strings.TrimSpace(req.EventStatus),
			EventConclusion: strings.TrimSpace(req.EventConclusion),
			Subject:         strings.TrimSpace(req.Subject),
			Since:           now,
		}
		entry.Runtime.Run.Status = "waiting_event"
	}
	runtime := entry.Runtime
	path := entry.Path
	d.mu.Unlock()

	if err := agent.SaveRuntimeMeta(path, runtime); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, err), http.StatusInternalServerError)
		return
	}
	d.appendTimeline(path, agent.RuntimeTimelineEvent{
		Type:       action,
		Source:     "api",
		RunStatus:  runtime.Run.Status,
		GoalStatus: runtime.Goal.Status,
		WaitKind:   runtime.Wait.Kind,
		WaitID:     runtime.Wait.EventID,
		Subject:    runtime.Wait.Subject,
		Reason:     runtime.Wait.Reason,
		EventID:    runtime.Wait.EventID,
	})

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"ok":               true,
		"session_id":       req.SessionID,
		"run_status":       runtime.Run.Status,
		"wait_kind":        runtime.Wait.Kind,
		"event_source":     runtime.Wait.EventSource,
		"event_id":         runtime.Wait.EventID,
		"event_status":     runtime.Wait.EventStatus,
		"event_conclusion": runtime.Wait.EventConclusion,
	}
	json.NewEncoder(w).Encode(resp)
}

// handleWaitTime sets or clears a time wait condition.
// Body: {"session_id":"...","until":"2026-06-13T10:00:00Z","after":"1h","reason":"waiting until CI window","subject":"release"}
func (d *Daemon) handleWaitTime(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		Until     string `json:"until"`
		After     string `json:"after"`
		Reason    string `json:"reason"`
		Subject   string `json:"subject"`
		Clear     bool   `json:"clear"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	var until time.Time
	if !req.Clear {
		var err error
		until, err = parseWaitUntil(req.Until, req.After, now)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
	}

	d.mu.Lock()
	entry, ok := d.registry[req.SessionID]
	if !ok {
		d.mu.Unlock()
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}
	if _, active := d.activeRuns[req.SessionID]; active {
		d.mu.Unlock()
		http.Error(w, `{"error":"session already running"}`, http.StatusConflict)
		return
	}
	action := "wait_started"
	if req.Clear {
		entry.Runtime.Wait = agent.RuntimeWaitMeta{}
		if entry.Runtime.Run.Status == "waiting_time" {
			entry.Runtime.Run.Status = "idle"
		}
		action = "wait_cleared"
	} else {
		reason := strings.TrimSpace(req.Reason)
		if reason == "" {
			reason = "waiting until time"
		}
		subject := strings.TrimSpace(req.Subject)
		if subject == "" {
			subject = until.Format(time.RFC3339)
		}
		entry.Runtime.Wait = agent.RuntimeWaitMeta{
			Kind:    "time",
			Reason:  reason,
			Subject: subject,
			Until:   until,
			Since:   now,
		}
		entry.Runtime.Run.Status = "waiting_time"
	}
	runtime := entry.Runtime
	path := entry.Path
	d.mu.Unlock()

	if err := agent.SaveRuntimeMeta(path, runtime); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, err), http.StatusInternalServerError)
		return
	}
	d.appendTimeline(path, agent.RuntimeTimelineEvent{
		Type:       action,
		Source:     "api",
		RunStatus:  runtime.Run.Status,
		GoalStatus: runtime.Goal.Status,
		WaitKind:   runtime.Wait.Kind,
		Subject:    runtime.Wait.Subject,
		Reason:     runtime.Wait.Reason,
	})

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"ok":         true,
		"session_id": req.SessionID,
		"run_status": runtime.Run.Status,
		"wait_kind":  runtime.Wait.Kind,
		"until":      runtime.Wait.Until.Format(time.RFC3339),
	}
	json.NewEncoder(w).Encode(resp)
}

func parseWaitUntil(untilRaw, afterRaw string, now time.Time) (time.Time, error) {
	untilRaw = strings.TrimSpace(untilRaw)
	afterRaw = strings.TrimSpace(afterRaw)
	if untilRaw == "" && afterRaw == "" {
		return time.Time{}, fmt.Errorf("until or after required")
	}
	if untilRaw != "" && afterRaw != "" {
		return time.Time{}, fmt.Errorf("only one of until or after may be set")
	}
	if afterRaw != "" {
		dur, err := time.ParseDuration(afterRaw)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid after: %s", err)
		}
		if dur < time.Second {
			return time.Time{}, fmt.Errorf("after must be at least 1s")
		}
		return now.Add(dur).UTC(), nil
	}
	until, err := time.Parse(time.RFC3339, untilRaw)
	if err != nil {
		until, err = time.Parse(time.RFC3339Nano, untilRaw)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid until: %s", err)
	}
	return until.UTC(), nil
}

// handleWatch configures file watching for a session.
// Body: {"session_id": "...", "paths": ["src/"], "ignore_patterns": ["*.tmp"], "debounce": "3s", "enabled": true}
func (d *Daemon) handleWatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID      string   `json:"session_id"`
		Paths          []string `json:"paths"`
		IgnorePatterns []string `json:"ignore_patterns"`
		Debounce       string   `json:"debounce"`
		Enabled        *bool    `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}

	d.mu.RLock()
	entry, ok := d.registry[req.SessionID]
	var runtime agent.RuntimeMeta
	var path string
	if ok {
		runtime = entry.Runtime
		path = entry.Path
	}
	d.mu.RUnlock()
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	debounce := 3 * time.Second
	if req.Debounce != "" {
		dur, err := time.ParseDuration(req.Debounce)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid debounce: %s"}`, err), http.StatusBadRequest)
			return
		}
		debounce = dur
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	cfg := FileWatchConfig{
		Paths:          append([]string(nil), req.Paths...),
		IgnorePatterns: append([]string(nil), req.IgnorePatterns...),
		Debounce:       debounce,
		Enabled:        enabled,
	}
	runtime.FileWatch = agent.RuntimeWatchMeta{
		Paths:          append([]string(nil), cfg.Paths...),
		IgnorePatterns: append([]string(nil), cfg.IgnorePatterns...),
		Debounce:       cfg.Debounce,
		Enabled:        cfg.Enabled && len(cfg.Paths) > 0,
	}
	if err := agent.SaveRuntimeMeta(path, runtime); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, err), http.StatusInternalServerError)
		return
	}

	d.mu.Lock()
	if entry := d.registry[req.SessionID]; entry != nil {
		entry.Runtime = runtime
	}
	d.mu.Unlock()

	if d.fileWatcher != nil {
		watchEntry := &SessionEntry{ID: req.SessionID, Path: path, Runtime: runtime}
		if runtime.FileWatch.Enabled {
			d.fileWatcher.Register(req.SessionID, fileWatchConfigForEntry(watchEntry))
		} else {
			d.fileWatcher.Unregister(req.SessionID)
		}
	}
	d.appendTimeline(path, agent.RuntimeTimelineEvent{
		Type:       "watch_configured",
		Source:     "api",
		RunStatus:  runtime.Run.Status,
		GoalStatus: runtime.Goal.Status,
		Message:    fmt.Sprintf("file watch enabled=%t paths=%d", runtime.FileWatch.Enabled, len(runtime.FileWatch.Paths)),
	})

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"session_id":%q,"enabled":%t,"paths":%d}`, req.SessionID, runtime.FileWatch.Enabled, len(runtime.FileWatch.Paths))
}

// handleApprove resolves a pending daemon approval.
// Body: {"session_id":"...","approval_id":"...","session":true,"persist":false}
func (d *Daemon) handleApprove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID  string `json:"session_id"`
		ApprovalID string `json:"approval_id"`
		Session    bool   `json:"session"`
		Persist    bool   `json:"persist"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.ApprovalID == "" {
		http.Error(w, `{"error":"session_id and approval_id required"}`, http.StatusBadRequest)
		return
	}
	allow := r.URL.Path == "/approvals/approve"

	d.mu.Lock()
	active := d.activeRuns[req.SessionID]
	if active == nil || active.Control == nil {
		d.mu.Unlock()
		http.Error(w, `{"error":"active run not found"}`, http.StatusNotFound)
		return
	}
	if _, ok := active.Approvals[req.ApprovalID]; !ok {
		d.mu.Unlock()
		http.Error(w, `{"error":"approval not found"}`, http.StatusNotFound)
		return
	}
	delete(active.Approvals, req.ApprovalID)
	if entry := d.registry[req.SessionID]; entry != nil {
		entry.Runtime.Run.Status = "running"
		entry.Runtime.Wait = agent.RuntimeWaitMeta{}
		runtime := entry.Runtime
		path := entry.Path
		d.mu.Unlock()
		_ = saveRuntimeMeta(path, runtime)
		action := "approval_denied"
		if allow {
			action = "approval_approved"
		}
		d.appendTimeline(path, agent.RuntimeTimelineEvent{
			Type:       action,
			Source:     "api",
			RunStatus:  runtime.Run.Status,
			GoalStatus: runtime.Goal.Status,
			WaitKind:   "approval",
			WaitID:     req.ApprovalID,
		})
		active.Control.Approve(req.ApprovalID, allow, req.Session, req.Persist)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"session_id":%q,"approval_id":%q,"allow":%t}`, req.SessionID, req.ApprovalID, allow)
		return
	}
	d.mu.Unlock()
	http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
}

// handleAnswer resolves a pending daemon ask request.
// Body: {"session_id":"...","ask_id":"...","selected":"..."} or
// {"session_id":"...","ask_id":"...","answers":[...]}.
func (d *Daemon) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string            `json:"session_id"`
		AskID     string            `json:"ask_id"`
		Selected  string            `json:"selected"`
		Answers   []event.AskAnswer `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.AskID == "" {
		http.Error(w, `{"error":"session_id and ask_id required"}`, http.StatusBadRequest)
		return
	}

	d.mu.Lock()
	active := d.activeRuns[req.SessionID]
	if active == nil || active.Control == nil {
		d.mu.Unlock()
		http.Error(w, `{"error":"active run not found"}`, http.StatusNotFound)
		return
	}
	ask, ok := active.Asks[req.AskID]
	if !ok {
		d.mu.Unlock()
		http.Error(w, `{"error":"ask not found"}`, http.StatusNotFound)
		return
	}
	delete(active.Asks, req.AskID)
	if len(req.Answers) == 0 && req.Selected != "" {
		if len(ask.Questions) > 0 {
			for _, q := range ask.Questions {
				req.Answers = append(req.Answers, event.AskAnswer{QuestionID: q.ID, Selected: []string{req.Selected}})
			}
		} else {
			req.Answers = []event.AskAnswer{{Selected: []string{req.Selected}}}
		}
	}
	if entry := d.registry[req.SessionID]; entry != nil {
		entry.Runtime.Run.Status = "running"
		entry.Runtime.Wait = agent.RuntimeWaitMeta{}
		runtime := entry.Runtime
		path := entry.Path
		d.mu.Unlock()
		_ = saveRuntimeMeta(path, runtime)
		d.appendTimeline(path, agent.RuntimeTimelineEvent{
			Type:       "ask_answered",
			Source:     "api",
			RunStatus:  runtime.Run.Status,
			GoalStatus: runtime.Goal.Status,
			WaitKind:   "ask",
			WaitID:     req.AskID,
		})
		active.Control.AnswerQuestion(req.AskID, req.Answers)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"session_id":%q,"ask_id":%q}`, req.SessionID, req.AskID)
		return
	}
	d.mu.Unlock()
	http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
}
