package serve

import (
	"net/http"
	"strings"
)

func (h *Hub) registerRemoteSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /remotes/{host}/sessions/remove", h.removeRemoteSession)
	mux.HandleFunc("POST /remotes/{host}/sessions/pin", h.remoteSessionFlag("pin"))
	mux.HandleFunc("POST /remotes/{host}/sessions/archive", h.remoteSessionFlag("archive"))
	mux.HandleFunc("POST /remotes/{host}/sessions/viewed", h.remoteSessionFlag("viewed"))
}

func (h *Hub) remoteSessionFlag(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.opts.Remote == nil {
			refuseNoRemote(w)
			return
		}
		var body struct {
			Path     string `json:"path"`
			Pinned   bool   `json:"pinned"`
			Archived bool   `json:"archived"`
		}
		if !decodeRemoteBody(w, r, &body) {
			return
		}
		path := strings.TrimSpace(body.Path)
		if path == "" {
			missingField(w, "path")
			return
		}
		ep, release, err := h.remoteLink(r, r.PathValue("host"))
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		defer release()
		payload := map[string]any{"path": path}
		switch action {
		case "pin":
			payload["pinned"] = body.Pinned
		case "archive":
			payload["archived"] = body.Archived
		}
		if err := farRequest(r.Context(), ep, http.MethodPost, "/tree/sessions/"+action, payload, nil); err != nil {
			writeErr(w, http.StatusBadGateway, relayFarReason(err))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
