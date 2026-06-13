package daemon

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"reasonix/internal/agent"
)

// WebhookConfig configures the webhook receiver endpoint.
type WebhookConfig struct {
	// Secret is the HMAC-SHA256 key for validating inbound webhook payloads.
	// Required — webhooks without a valid signature are rejected.
	Secret string `json:"secret"`
	// Enabled controls whether the /webhook endpoint accepts events.
	Enabled bool `json:"enabled"`
}

// WebhookEvent is the envelope for all inbound webhook payloads.
type WebhookEvent struct {
	// Type identifies the event class: "github.ci", "github.pr", "custom", etc.
	Type string `json:"type"`
	// SessionID targets a specific session. If empty, the daemon routes by other fields.
	SessionID string `json:"session_id,omitempty"`
	// Summary is a short human-readable description injected as the user turn.
	Summary string `json:"summary"`
	// Payload is arbitrary event data (passed through to the model as context).
	Payload json.RawMessage `json:"payload,omitempty"`
	// EventID is an optional dedup key. If set, duplicate events are ignored.
	EventID string `json:"event_id,omitempty"`
}

// handleWebhook receives external events, validates the signature, and queues
// a wakeup for the targeted session.
func (d *Daemon) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if d.webhookCfg == nil || !d.webhookCfg.Enabled {
		http.Error(w, `{"error":"webhook not enabled"}`, http.StatusNotFound)
		return
	}

	// Read body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}

	// Validate HMAC-SHA256 signature.
	sig := r.Header.Get("X-Webhook-Signature")
	if sig == "" {
		sig = r.Header.Get("X-Hub-Signature-256") // GitHub compat
	}
	if !d.validateSignature(body, sig) {
		http.Error(w, `{"error":"invalid signature"}`, http.StatusUnauthorized)
		return
	}

	// Parse event.
	var evt WebhookEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if evt.SessionID == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	if evt.Summary == "" {
		evt.Summary = fmt.Sprintf("webhook event: %s", evt.Type)
	}

	// Find session.
	d.mu.Lock()
	entry, ok := d.registry[evt.SessionID]
	if !ok {
		d.mu.Unlock()
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	// Dedup check.
	if evt.EventID != "" && entry.Runtime.Scheduler.LastWakeupEventID == evt.EventID {
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"status":"duplicate","event_id":%q}`, evt.EventID)
		return
	}
	if _, running := d.activeRuns[evt.SessionID]; running {
		d.mu.Unlock()
		http.Error(w, `{"error":"session already running"}`, http.StatusConflict)
		return
	}

	// Update runtime to signal the wakeup.
	entry.Runtime.Run.Status = "pending_continue"
	entry.Runtime.Run.LastWakeupReason = "webhook:" + evt.Type
	entry.Runtime.Run.ResumeCount++
	entry.Runtime.Wait = agent.RuntimeWaitMeta{
		EventSource: "webhook:" + evt.Type,
		EventID:     evt.EventID,
	}
	entry.Runtime.Scheduler.LastWakeupAt = time.Now()
	entry.Runtime.Scheduler.LastWakeupReason = "webhook:" + evt.Type
	if evt.EventID != "" {
		entry.Runtime.Scheduler.LastWakeupEventID = evt.EventID
	}
	runtime := entry.Runtime
	path := entry.Path
	d.mu.Unlock()

	if err := agent.SaveRuntimeMeta(path, runtime); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, err), http.StatusInternalServerError)
		return
	}
	d.enqueueIntent(RunIntent{
		SessionID:   evt.SessionID,
		SessionPath: path,
		Source:      "webhook",
		Reason:      "webhook:" + evt.Type,
		EventID:     evt.EventID,
	})

	d.logger.Info("webhook event received", "type", evt.Type, "session", evt.SessionID, "event_id", evt.EventID)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"session_id":%q,"status":"pending_continue","event_id":%q}`, evt.SessionID, evt.EventID)
}

// validateSignature checks the HMAC-SHA256 signature against the webhook secret.
// Supports both "sha256=<hex>" (GitHub style) and raw hex formats.
func (d *Daemon) validateSignature(body []byte, signature string) bool {
	if d.webhookCfg == nil || d.webhookCfg.Secret == "" {
		return false
	}
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return false
	}

	// Strip "sha256=" prefix if present.
	signature = strings.TrimPrefix(signature, "sha256=")

	mac := hmac.New(sha256.New, []byte(d.webhookCfg.Secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}
