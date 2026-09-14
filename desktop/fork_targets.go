package main

import (
	"log/slog"
	"strings"

	"reasonix/internal/control"
	"reasonix/internal/session"
)

// forkTargetsController is the slice of *control.Controller that lists a
// source's forkable turns and creates a child without switching the parent
// controller or stopping a running turn. control.SessionAPI does not expose
// either method, so the desktop binding asserts them locally; the assertion
// below fails the build if the controller's signatures drift.
type forkTargetsController interface {
	ForkTargets() (session.ForkTargetSet, error)
	CreateForkSession(turnID, name, operationID string) (string, error)
}

var _ forkTargetsController = (*control.Controller)(nil)

// ForkTargetView is one turn of a source tab a caller may fork from. Reason is
// the refusal a surface shows; Available stays false for an open turn and for
// history that keeps no turn records.
type ForkTargetView struct {
	TurnID     string `json:"turnId"`
	TurnNumber int    `json:"turnNumber"`
	Status     string `json:"status"`
	MessageID  string `json:"messageId,omitempty"`
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
}

// ForkTargetSetView is a tab's fork state. Targets is a non-nil slice even when
// empty: null would break the renderer's .map/.length calls.
type ForkTargetSetView struct {
	Targets    []ForkTargetView `json:"targets"`
	Verifiable bool             `json:"verifiable"`
}

// ForkCreationView reports the child session created for a tab. SessionID is
// set as soon as the child is durable, so a caller whose tab attach failed
// recovers that child instead of creating a second one.
type ForkCreationView struct {
	SessionID string `json:"sessionId,omitempty"`
	TabID     string `json:"tabId,omitempty"`
	// Opened is always emitted: a missing field would read as "this build cannot
	// tell you", which is not the same answer as "the child could not be opened".
	Opened bool   `json:"opened"`
	Error  string `json:"error,omitempty"`
}

// ForkTargetsForTab reports the fork boundaries of a source tab; an empty tabID
// addresses the active tab, as ForkForTab does. Reading targets never mutates
// the tab, so it answers while a turn is running and for read-only channel
// tabs, which are legitimate fork sources. A tab with no backend reports an
// empty set rather than an error, mirroring forkForTabWithOptions.
func (a *App) ForkTargetsForTab(tabID string) (ForkTargetSetView, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if tab == nil || ctrl == nil {
		return ForkTargetSetView{Targets: []ForkTargetView{}}, nil
	}
	targets, ok := ctrl.(forkTargetsController)
	if !ok {
		return ForkTargetSetView{Targets: []ForkTargetView{}}, nil
	}
	set, err := targets.ForkTargets()
	if err != nil {
		return ForkTargetSetView{Targets: []ForkTargetView{}}, err
	}
	view := ForkTargetSetView{Targets: make([]ForkTargetView, 0, len(set.Targets)), Verifiable: set.Verifiable}
	for _, target := range set.Targets {
		view.Targets = append(view.Targets, ForkTargetView{
			TurnID:     target.TurnID,
			TurnNumber: target.TurnNumber,
			Status:     string(target.Status),
			MessageID:  target.MessageID,
			Available:  target.Available,
			Reason:     string(target.Reason),
		})
	}
	return view, nil
}

// CreateForkForTab creates a child session from one completed turn of the
// source tab and opens it in a new tab, leaving the source tab's transcript,
// controller, and running turn untouched. A read-only source is allowed: the
// child is written from the source, never into it. When the child exists but
// its tab could not be opened the result carries SessionID with Opened false
// and Error set, so the caller offers recovery from SessionID instead of
// repeating the creation.
func (a *App) CreateForkForTab(tabID string, turnID string, operationID string) (ForkCreationView, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if tab == nil || ctrl == nil {
		return ForkCreationView{}, nil
	}
	creator, ok := ctrl.(forkTargetsController)
	if !ok {
		return ForkCreationView{}, nil
	}
	childID, err := creator.CreateForkSession(turnID, "", operationID)
	if err != nil {
		return ForkCreationView{}, err
	}
	if strings.TrimSpace(childID) == "" {
		// No child identity to open or recover, so this is the same empty result
		// as a tab whose backend is missing.
		return ForkCreationView{}, nil
	}
	view := ForkCreationView{SessionID: childID}
	opened, openErr := a.openForkedSessionTabWithWorkspace(tab, childID, "")
	view.TabID = opened.tab.ID
	if openErr == nil && opened.tab.ID != "" {
		view.Opened = true
		return view, nil
	}
	if openErr != nil {
		slog.Warn("fork: child session created but tab attach failed", "session", childID, "err", openErr)
	}
	// The child is already durable, so the caller must recover it rather than
	// fork the same turn again; the cause is logged above.
	view.Error = rewindForkAttachError
	return view, nil
}
