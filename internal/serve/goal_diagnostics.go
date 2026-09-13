package serve

import (
	"context"
	"net/http"

	"reasonix/internal/control"
)

type goalDiagnosticExporter interface {
	ExportGoalDiagnostics(context.Context, control.GoalDiagnosticMetadata) ([]byte, error)
}

// goalDiagnostics exports the authoritative, fully flushed v3 event stream.
// It is session-fenced but read-only and therefore remains available to a
// spectator inspecting a session owned by another Reasonix surface.
func (s *Server) goalDiagnostics(w http.ResponseWriter, r *http.Request) {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.validateExpectedSessionLocked(w, r) {
		return
	}
	exporter, ok := s.ctl().(goalDiagnosticExporter)
	if !ok {
		http.Error(w, "goal diagnostics require goal-lifecycle-v2", http.StatusNotImplemented)
		return
	}
	payload, err := exporter.ExportGoalDiagnostics(r.Context(), control.GoalDiagnosticMetadata{
		Capabilities: s.capabilities(),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="reasonix-goal-diagnostics.json"`)
	_, _ = w.Write(payload)
}
