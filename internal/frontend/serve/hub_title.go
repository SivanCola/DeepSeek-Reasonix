package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/base/nilutil"
	"reasonix/internal/contract/provider"
	"reasonix/internal/state/sessionstore"
	"reasonix/internal/state/store"
)

func (h *Hub) titleRequest(w http.ResponseWriter, r *http.Request) (string, *string, bool) {
	var body struct {
		Path  string  `json:"path"`
		Title *string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badBody(w)
		return "", nil, false
	}
	requested, err := filepath.Abs(strings.TrimSpace(body.Path))
	name := filepath.Base(requested)
	if err != nil || !filepath.IsLocal(name) || !store.IsSessionTranscriptName(name) {
		refuse(w, http.StatusBadRequest, codeSessionBadPath, "the session path could not be resolved", nil)
		return "", nil, false
	}
	for _, root := range h.sessionRoots() {
		dir, err := filepath.Abs(SessionDirFor(root.dir))
		if err != nil || !strings.HasPrefix(requested, dir+string(os.PathSeparator)) || filepath.Dir(requested) != dir {
			continue
		}
		// The request selects a host-owned directory; it is not an I/O path.
		path := filepath.Join(dir, name)
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return "", nil, false
		}
		physicalDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return "", nil, false
		}
		if !strings.HasPrefix(resolved, physicalDir+string(os.PathSeparator)) || filepath.Dir(resolved) != physicalDir {
			refuse(w, http.StatusForbidden, "session.outside_workspace", "path outside a known workspace", nil)
			return "", nil, false
		}
		return path, body.Title, true
	}
	refuse(w, http.StatusForbidden, "session.outside_workspace", "path outside a known workspace", nil)
	return "", nil, false
}

func (h *Hub) renameSession(w http.ResponseWriter, r *http.Request) {
	path, input, ok := h.titleRequest(w, r)
	if !ok {
		return
	}
	if input == nil {
		badBody(w)
		return
	}
	title := strings.TrimSpace(*input)
	if title == "" {
		cache := h.titleCacheFor(filepath.Dir(path))
		if err := cache.clearExplicit(filepath.Base(path)); err != nil {
			refuse(w, http.StatusInternalServerError, codeSessionTitleSaveFailed, "could not save session title", nil)
			return
		}
		h.resumeAutomaticTitle(path, cache)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.titleCacheFor(filepath.Dir(path)).putExplicit(filepath.Base(path), title); err != nil {
		refuse(w, http.StatusInternalServerError, codeSessionTitleSaveFailed, "could not save session title", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) autoNameSession(w http.ResponseWriter, r *http.Request) {
	path, _, ok := h.titleRequest(w, r)
	if !ok {
		return
	}
	source, err := sessionstore.UserMessagesTitleSource(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if source == "" {
		refuse(w, http.StatusBadRequest, "session.title_no_message", "the session has no user message", nil)
		return
	}
	generator := h.sessionTitleGenerator(filepath.Dir(path))
	if nilutil.IsNil(generator.currentTitleProvider().prov) {
		refuse(w, http.StatusServiceUnavailable, "session.title_unavailable", "no title model is configured", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), titleFillTimeout)
	defer cancel()
	title, err := generator.tryGenerateTitle(ctx, source)
	if err != nil || title == "" {
		refuseTitle(w, err)
		return
	}
	if err := r.Context().Err(); err != nil {
		return
	}
	writeJSON(w, struct {
		Title string `json:"title"`
	}{Title: title})
}

func (h *Hub) sessionTitleGenerator(dir string) *Server {
	h.mu.RLock()
	for _, rt := range h.runtimes {
		if rt.Server != nil && !nilutil.IsNil(rt.Server.currentTitleProvider().prov) && rt.Server.Controller().SessionDir() == dir {
			h.mu.RUnlock()
			return rt.Server
		}
	}
	h.mu.RUnlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.titleFallback != nil {
		return h.titleFallback
	}
	generator := &Server{}
	if err := generator.initTitleProvider(); err != nil {
		return generator
	}
	if !nilutil.IsNil(generator.currentTitleProvider().prov) {
		h.titleFallback = generator
	}
	return generator
}

func (h *Hub) resumeAutomaticTitle(path string, cache *titleCache) {
	first, _ := sessionstore.SessionPreview(path)
	source := titleSource(first)
	name := filepath.Base(path)
	mod := sessionstore.SessionContentModTime(path).UnixNano()
	if _, ok := cache.get(name, source, mod); ok || source == "" {
		return
	}
	generator := h.sessionTitleGenerator(filepath.Dir(path))
	worker := &Server{
		titleModel: generator.currentTitleProvider(),
		titles:     cache, fill: newTitleFiller(),
	}
	worker.scheduleTitle(name, source, mod)
}

// refuseTitle says why automatic naming failed. Each kind asks for something
// different: wait, pick another title model, or look at what the endpoint said.
func refuseTitle(w http.ResponseWriter, err error) {
	var api *provider.APIError
	var auth *provider.AuthError
	switch {
	case errors.Is(err, errTitleTimeout):
		refuse(w, http.StatusGatewayTimeout, "session.title_timeout", "the title model did not answer in time", nil)
	case errors.Is(err, errTitleNoText):
		refuse(w, http.StatusBadGateway, "session.title_no_text", "the title model answered with no text", nil)
	case errors.Is(err, errTitleThought):
		refuse(w, http.StatusBadGateway, "session.title_thought_only", "the title model spent its output budget thinking and gave no title", nil)
	case errors.As(err, &api), errors.As(err, &auth):
		status := 0
		if api != nil {
			status = api.Status
		} else {
			status = auth.Status
		}
		refuse(w, http.StatusBadGateway, "session.title_refused", "the title model's endpoint refused the request",
			map[string]any{"status": status, "detail": modelCheckDetail(err, func() string { return "" })})
	default:
		refuse(w, http.StatusBadGateway, "session.title_failed", "the model did not generate a title", nil)
	}
}
