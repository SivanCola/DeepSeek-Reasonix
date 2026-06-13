package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"reasonix/internal/agent"
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
		views = append(views, SessionView{
			ID:         entry.ID,
			Path:       entry.Path,
			GoalText:   entry.Runtime.Goal.Text,
			GoalStatus: entry.Runtime.Goal.Status,
			RunStatus:  entry.Runtime.Run.Status,
		})
	}
	d.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SessionsResponse{Sessions: views})
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

	// For the daemon skeleton (Milestone 4), we mark the intent but don't
	// actually spawn a controller and run turns — that requires the full boot
	// pipeline. Instead we update the runtime sidecar to signal continuation
	// was requested, so the next interactive attach picks it up.
	d.mu.Lock()
	entry.Runtime.Run.Status = "pending_continue"
	entry.Runtime.Run.LastWakeupReason = req.Reason
	entry.Runtime.Run.ResumeCount++
	runtime := entry.Runtime
	path := entry.Path
	d.mu.Unlock()

	if err := saveRuntimeMeta(path, runtime); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, err), http.StatusInternalServerError)
		return
	}

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
	if ok {
		entry.Runtime.Run.Status = "stopped"
		entry.Runtime.Goal.Status = "stopped"
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
	_, ok := d.registry[req.SessionID]
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
		Paths:          req.Paths,
		IgnorePatterns: req.IgnorePatterns,
		Debounce:       debounce,
		Enabled:        enabled,
	}

	if d.fileWatcher != nil {
		if enabled && len(req.Paths) > 0 {
			d.fileWatcher.Register(req.SessionID, cfg)
		} else {
			d.fileWatcher.Unregister(req.SessionID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"session_id":%q,"enabled":%t,"paths":%d}`, req.SessionID, enabled, len(req.Paths))
}
