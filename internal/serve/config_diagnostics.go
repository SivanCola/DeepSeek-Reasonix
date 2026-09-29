package serve

import (
	"encoding/json"
	"net/http"

	"reasonix/internal/config"
	"reasonix/internal/session"
)

func (s *Server) configDiagnostics(w http.ResponseWriter, r *http.Request) {
	_, ref, controller, ok := s.fixedSessionExportSource(w, r, session.SessionRef{})
	if !ok {
		return
	}
	view := config.NewDiagnosticSnapshot(ref.HostID, "", "unavailable")
	if controller != nil {
		view = config.InspectDiagnosticDetails(ref.HostID, controller.WorkspaceRoot(), r.URL.Query().Get("detail"))
		if !s.sessionDiagnosticControllerCurrent(controller, ref) {
			view = config.NewDiagnosticSnapshot(ref.HostID, "", "unavailable")
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(view)
}
