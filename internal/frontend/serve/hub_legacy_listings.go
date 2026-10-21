package serve

import (
	"path/filepath"
	"reasonix/internal/state/sessionstore"
)

type hubLegacyListings struct {
	service     *sessionstore.LegacyListings
	release     func()
	unsubscribe func()
}

func (h *Hub) legacyListings() *sessionstore.LegacyListings {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.legacy == nil {
		service, release := sessionstore.AcquireLegacyListings()
		h.legacy = &hubLegacyListings{service: service, release: release, unsubscribe: service.Subscribe(h.treeChanged)}
	}
	return h.legacy.service
}

func (h *Hub) closeLegacyListings() {
	h.mu.Lock()
	legacy := h.legacy
	h.legacy = nil
	h.mu.Unlock()
	if legacy != nil {
		legacy.unsubscribe()
		legacy.release()
	}
}

func combineListingStatus(a *sessionstore.ListingStatus, b sessionstore.ListingStatus) {
	a.ListingRevision += b.ListingRevision
	a.CountsComplete = a.CountsComplete && b.CountsComplete
	a.PendingMetadata += b.PendingMetadata
	a.FailedMetadata += b.FailedMetadata
	priority := map[string]int{"ready": 0, "indexing": 1, "discovering": 2, "degraded": 3}
	if priority[b.ListingState] > priority[a.ListingState] {
		a.ListingState = b.ListingState
	}
}

func (h *Hub) listTreeSessions(dir string, status *sessionstore.ListingStatus, retry bool) []sessionstore.SessionInfo {
	if status == nil {
		infos, _ := sessionstore.ListSessions(dir)
		return infos
	}
	infos, err := sessionstore.ListNativeSessions(dir)
	if retry {
		h.legacyListings().Retry(dir)
	}
	legacy, progress := h.legacyListings().Snapshot(dir)
	if err != nil {
		progress.ListingState, progress.CountsComplete = "degraded", false
	}
	combineListingStatus(status, progress)
	present := make(map[string]bool, len(infos))
	for _, native := range infos {
		present[native.Path] = true
	}
	for _, row := range legacy {
		if !present[row.Path] {
			infos = append(infos, row)
		}
	}
	return infos
}

func (h *Hub) prioritizeTreeRows(dirs map[string]map[string]sessionstore.SessionInfo, rows []treeSession) {
	for dir := range dirs {
		paths := []string{}
		for _, row := range rows {
			if filepath.Dir(row.Path) == dir {
				paths = append(paths, row.Path)
			}
		}
		h.legacyListings().Prioritize(dir, paths)
	}
}

func listingStatusFor(progressive bool) *sessionstore.ListingStatus {
	if !progressive {
		return nil
	}
	return &sessionstore.ListingStatus{ListingState: "ready", CountsComplete: true}
}
