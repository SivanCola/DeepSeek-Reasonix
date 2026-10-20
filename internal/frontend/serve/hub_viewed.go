package serve

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/state/sessionstore"
)

// markSessionViewed clears the unread mark of a saved conversation the person
// chose from the sidebar, open in a pane or not. It only writes the session's
// sidecar and is a no-op for a conversation that is not unread.
func (h *Hub) markSessionViewed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badBody(w)
		return
	}
	abs, err := filepath.Abs(strings.TrimSpace(body.Path))
	if err != nil {
		refuse(w, http.StatusBadRequest, codeSessionBadPath, "the session path could not be resolved", nil)
		return
	}
	path, ok := h.listedSessionPaths()[sessionstore.CanonicalSessionPath(abs)]
	if !ok {
		refuse(w, http.StatusForbidden, "session.outside_workspace", "path outside a known workspace", nil)
		return
	}
	if err := sessionstore.MarkSessionViewed(path, time.Now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
