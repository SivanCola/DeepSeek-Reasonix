package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The remote side of session forks: the capability gate a serve advertises, and
// the two commands that read its boundaries and create a child from one. An
// older serve has only /fork, which switches the parent session's lease.

// forkTargetsCapable reports whether the tab's token handshake advertised
// session-fork-targets-v1. It stays false for a tab that is not open, one whose
// handshake has not run, and one whose serve advertised no capabilities: an
// older serve must never be treated as if it had the fork routes, because its
// only fork endpoint switches the parent session. Callers refuse it instead.
func (a *App) forkTargetsCapable(tabID string) bool {
	a.remoteTabMu.Lock()
	defer a.remoteTabMu.Unlock()
	return remoteForkTargetsSupported(a.remoteTabs[tabID])
}

// remoteForkTargetsSupported answers the same question from a tab the caller
// already holds, so readers that build a tab's view under remoteTabMu do not
// take the lock again. A nil tab and an unset capability map both answer false.
func remoteForkTargetsSupported(tab *remoteTab) bool {
	return tab != nil && tab.capabilities[serveCapabilitySessionForkTargetsV1]
}

// remoteForkUnsupported is the reason a serve without session-fork-targets-v1
// reports for a remote fork. The legacy /fork endpoint switches the parent
// session and rebinds its lease, which is what this caller asked to avoid, so
// the unsupported serve is refused instead of silently switched.
const remoteForkUnsupported = "this remote Reasonix Serve does not support " + serveCapabilitySessionForkTargetsV1 + "; upgrade it to fork a remote turn into an independent session"

// ForkTargetsRemoteTab lists the source turns a remote tab may fork from. The
// serve derives them from its durable turn records, so the read answers while a
// turn is running and never mutates the tab. A serve without the capability
// answers with an empty set and no error: its caller already holds the
// capability state, so an error here would only force it to match a message.
func (a *App) ForkTargetsRemoteTab(tabID string) (ForkTargetSetView, error) {
	empty := ForkTargetSetView{Targets: []ForkTargetView{}}
	if !a.forkTargetsCapable(tabID) {
		return empty, nil
	}
	raw, err := a.remoteTabGet(tabID, "/fork-targets")
	if err != nil {
		return empty, err
	}
	view := empty
	if err := json.Unmarshal(raw, &view); err != nil {
		return empty, fmt.Errorf("decode remote fork targets: %w", err)
	}
	if view.Targets == nil {
		// A null targets field must not reach the renderer, which calls .map.
		view.Targets = []ForkTargetView{}
	}
	return view, nil
}

// CreateForkRemoteTab creates an independent child session from one completed
// turn of a remote tab. The child belongs to the serve, so the returned view
// carries its identity and not an opened desktop tab: opening a surface for the
// child is the caller's job. Neither the parent session, its broadcast binding,
// nor its lease is touched, unlike /fork, which switches all three.
func (a *App) CreateForkRemoteTab(tabID, turnID, operationID string) (ForkCreationView, error) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ForkCreationView{}, fmt.Errorf("forking a remote turn requires a turn id")
	}
	if !a.forkTargetsCapable(tabID) {
		return ForkCreationView{Error: remoteForkUnsupported}, nil
	}
	var view ForkCreationView
	err := a.remoteTabPostJSON(tabID, "/fork-session", map[string]any{"turnId": turnID, "name": "", "operationId": operationID}, &view)
	if err != nil {
		var statusErr *serveHTTPStatusError
		if errors.As(err, &statusErr) && statusErr.message != "" {
			// The serve names its refusal ("turn is still running", "no verifiable
			// boundary"), so the surface shows that reason rather than a status code.
			return ForkCreationView{Error: statusErr.message}, nil
		}
		return ForkCreationView{}, err
	}
	view.Opened = true
	return view, nil
}
