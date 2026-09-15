package main

import (
	"testing"

	"reasonix/internal/control"
	"reasonix/internal/session"
)

type activeNavigationController struct {
	control.SessionAPI
	control.IdentityLifecycle
	status  control.RuntimeStatus
	closed  bool
	replays int
}

func (c *activeNavigationController) RuntimeStatus() control.RuntimeStatus { return c.status }
func (c *activeNavigationController) Close()                               { c.closed = true; c.SessionAPI.Close() }

func (c *activeNavigationController) ReplayPendingPrompts() {
	c.replays++
	c.SessionAPI.ReplayPendingPrompts()
}

func (c *activeNavigationController) SessionBinding() (*session.Service, *session.Runtime, bool) {
	return c.SessionAPI.(persistedSessionBinding).SessionBinding()
}

func TestCanonicalNavigationPreservesActiveSource(t *testing.T) {
	for name, status := range map[string]control.RuntimeStatus{
		"running": {Running: true}, "approval": {PendingPrompt: true}, "background": {BackgroundJobs: 1},
	} {
		t.Run(name, func(t *testing.T) {
			app, tab, target, _, _ := canonicalWorkspaceOpenFixture(t)
			source := session.SessionRef{HostID: localDesktopHostID, SessionID: tab.SessionID}
			original := tab.Ctrl
			active := &activeNavigationController{SessionAPI: original, IdentityLifecycle: original.(control.IdentityLifecycle), status: status}
			tab.Ctrl = active
			t.Cleanup(original.Close)
			sourceSink := tab.sink
			sourceDisplay := tab.displayBufferState()
			if _, err := app.OpenSession(target.Ref()); err != nil {
				t.Fatal(err)
			}
			key := sessionRuntimeKey(sessionRoute(source.SessionID))
			detached := app.detachedSessions[key]
			if detached == nil || detached.Ctrl != active || active.closed {
				t.Fatal("active source was not retained")
			}
			if tab.sink == sourceSink {
				t.Fatal("target shares background source event sink")
			}
			if owner, _ := sourceSink.binding(); owner != detached.ID || sourceSink.context() != nil || tab.displayBufferState() == sourceDisplay {
				t.Fatal("background events can reach the visible session")
			}
			if ref, _ := active.SessionRef(); ref != source {
				t.Fatal("source writer was rebound")
			}
			if rt := app.runtimeBySessionKey[key]; rt == nil || rt.Owner != detached {
				t.Fatal("source runtime registry lost its owner")
			}
			if _, err := app.OpenSession(source); err != nil {
				t.Fatal(err)
			}
			if tab.Ctrl != active || tab.sink != sourceSink || active.closed || app.detachedSessions[key] != nil {
				t.Fatal("return did not reattach the exact active source")
			}
			if active.replays != 1 || tab.displayBufferState() != sourceDisplay {
				t.Fatal("return did not restore pending prompts and display state")
			}
			if _, err := app.OpenSession(source); err != nil {
				t.Fatalf("active same-session open: %v", err)
			}
		})
	}
}

func TestCanonicalNavigationBetweenTwoActiveOwners(t *testing.T) {
	app, tab, target, _, _ := canonicalWorkspaceOpenFixture(t)
	source := session.SessionRef{HostID: localDesktopHostID, SessionID: tab.SessionID}
	wrap := func() *activeNavigationController {
		ctrl := tab.Ctrl
		active := &activeNavigationController{SessionAPI: ctrl, IdentityLifecycle: ctrl.(control.IdentityLifecycle), status: control.RuntimeStatus{Running: true}}
		tab.Ctrl = active
		t.Cleanup(ctrl.Close)
		return active
	}
	first := wrap()
	if _, err := app.OpenSession(target.Ref()); err != nil {
		t.Fatal(err)
	}
	second := wrap()
	for range 3 {
		if _, err := app.OpenSession(source); err != nil {
			t.Fatal(err)
		}
		if tab.Ctrl != first {
			t.Fatal("source controller replaced")
		}
		if _, err := app.OpenSession(target.Ref()); err != nil {
			t.Fatal(err)
		}
		if tab.Ctrl != second {
			t.Fatal("target controller replaced")
		}
	}
	if first.closed || second.closed || len(app.detachedSessions) != 1 {
		t.Fatal("active navigation closed or duplicated an owner")
	}
}
