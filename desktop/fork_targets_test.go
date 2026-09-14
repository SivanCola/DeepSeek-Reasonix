package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/session"
)

// forkTargetsStubController is a control.SessionAPI fake that also satisfies
// forkTargetsController and records the creation request it received.
type forkTargetsStubController struct {
	*tabScopedActionController
	running    bool
	set        session.ForkTargetSet
	setErr     error
	childID    string
	createErr  error
	createTurn string
	createName string
	createOp   string
	creates    int
}

// RuntimeStatus reports a turn in flight when running is set, so a scenario can
// assert the binding answers while the source is busy.
func (c *forkTargetsStubController) RuntimeStatus() control.RuntimeStatus {
	if !c.running {
		return c.tabScopedActionController.RuntimeStatus()
	}
	return control.RuntimeStatus{Running: true, Status: event.TurnInProgress}
}

func (c *forkTargetsStubController) ForkTargets() (session.ForkTargetSet, error) {
	return c.set, c.setErr
}

func (c *forkTargetsStubController) CreateForkSession(turnID, name, operationID string) (string, error) {
	c.creates++
	c.createTurn, c.createName, c.createOp = turnID, name, operationID
	return c.childID, c.createErr
}

// assertEmptyForkTargets checks the shared empty result: no error, a non-nil
// slice, and a "targets" field that JSON-marshals to [] instead of null.
func assertEmptyForkTargets(t *testing.T, name string, view ForkTargetSetView, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: err = %v, want nil", name, err)
	}
	if view.Targets == nil {
		t.Fatalf("%s: Targets is nil; the renderer expects []", name)
	}
	if len(view.Targets) != 0 || view.Verifiable {
		t.Fatalf("%s: view = %+v, want an empty unverifiable set", name, view)
	}
	raw, marshalErr := json.Marshal(view)
	if marshalErr != nil {
		t.Fatalf("%s: marshal: %v", name, marshalErr)
	}
	var decoded struct {
		Targets *[]ForkTargetView `json:"targets"`
	}
	if unmarshalErr := json.Unmarshal(raw, &decoded); unmarshalErr != nil {
		t.Fatalf("%s: unmarshal %s: %v", name, raw, unmarshalErr)
	}
	if decoded.Targets == nil || len(*decoded.Targets) != 0 {
		t.Fatalf("%s: JSON = %s, want \"targets\":[]", name, raw)
	}
}

func TestForkTargetsForTabReturnsEmptyNonNilTargets(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	missing, missingErr := app.ForkTargetsForTab("missing")
	assertEmptyForkTargets(t, "missing tab", missing, missingErr)
	app.setTestCtrl(newTabScopedActionController(), "")
	stubbed, stubbedErr := app.ForkTargetsForTab("test")
	assertEmptyForkTargets(t, "controller without fork targets", stubbed, stubbedErr)
	active, activeErr := app.ForkTargetsForTab("")
	assertEmptyForkTargets(t, "active tab", active, activeErr)
}

func TestForkTargetsForTabMapsTargetsForReadOnlyTab(t *testing.T) {
	isolateDesktopUserDirs(t)

	ctrl := &forkTargetsStubController{
		tabScopedActionController: newTabScopedActionController(),
		// A read-only channel tab whose turn is running still lists targets.
		running: true,
		set: session.ForkTargetSet{
			Targets: []session.ForkTarget{
				{TurnID: "turn-1", TurnNumber: 1, Status: event.TurnCompleted, MessageID: "msg-1", Available: true},
				{TurnID: "turn-2", TurnNumber: 2, Status: event.TurnInProgress, Reason: session.ForkTurnOpen},
			},
			Verifiable: true,
		},
	}
	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.tabs["test"].ReadOnly = true

	view, err := app.ForkTargetsForTab("test")
	if err != nil {
		t.Fatalf("ForkTargetsForTab: %v", err)
	}
	if !view.Verifiable {
		t.Fatal("Verifiable = false, want the controller's value")
	}
	want := []ForkTargetView{
		{TurnID: "turn-1", TurnNumber: 1, Status: string(event.TurnCompleted), MessageID: "msg-1", Available: true},
		{TurnID: "turn-2", TurnNumber: 2, Status: string(event.TurnInProgress), Reason: string(session.ForkTurnOpen)},
	}
	if len(view.Targets) != len(want) {
		t.Fatalf("targets = %+v, want %+v", view.Targets, want)
	}
	for i, target := range view.Targets {
		if target != want[i] {
			t.Fatalf("target[%d] = %+v, want %+v", i, target, want[i])
		}
	}
	raw, marshalErr := json.Marshal(view.Targets[1])
	if marshalErr != nil {
		t.Fatalf("marshal target: %v", marshalErr)
	}
	var fields map[string]any
	if unmarshalErr := json.Unmarshal(raw, &fields); unmarshalErr != nil {
		t.Fatalf("unmarshal target: %v", unmarshalErr)
	}
	if _, present := fields["messageId"]; present {
		t.Fatalf("target without a message id = %s, want messageId omitted", raw)
	}
}

func TestForkTargetsForTabReturnsControllerError(t *testing.T) {
	isolateDesktopUserDirs(t)

	ctrl := &forkTargetsStubController{
		tabScopedActionController: newTabScopedActionController(),
		setErr:                    errors.New("fork targets unavailable"),
	}
	app := NewApp()
	app.setTestCtrl(ctrl, "")

	view, err := app.ForkTargetsForTab("test")
	if err == nil {
		t.Fatal("ForkTargetsForTab: err = nil, want the controller's failure")
	}
	// The error is asserted above; the view still satisfies the empty-set contract.
	assertEmptyForkTargets(t, "failed targets", view, nil)
}

func TestCreateForkForTabOpensChildInNewTab(t *testing.T) {
	isolateDesktopUserDirs(t)

	childPath := filepath.Join(config.SessionDir(), "created-fork.jsonl")
	ctrl := &forkTargetsStubController{
		tabScopedActionController: newTabScopedActionController(),
		// Creating a child neither stops the running turn nor takes a rotation gate.
		running: true,
		childID: childPath,
	}
	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.tabs["test"].TopicTitle = "Source topic"
	// A read-only channel tab is a legitimate fork source: the child is written
	// from the source, never into it.
	app.tabs["test"].ReadOnly = true

	view, err := app.CreateForkForTab("test", "turn-7", "op-1")
	if err != nil {
		t.Fatalf("CreateForkForTab: %v", err)
	}
	if !view.Opened || view.Error != "" {
		t.Fatalf("view = %+v, want an opened tab without an error", view)
	}
	if view.SessionID != childPath {
		t.Fatalf("sessionId = %q, want %q", view.SessionID, childPath)
	}
	if view.TabID == "" || view.TabID == "test" {
		t.Fatalf("tabId = %q, want a fresh tab", view.TabID)
	}
	if ctrl.createTurn != "turn-7" || ctrl.createName != "" || ctrl.createOp != "op-1" {
		t.Fatalf("create request = (%q, %q, %q), want (turn-7, \"\", op-1)",
			ctrl.createTurn, ctrl.createName, ctrl.createOp)
	}
	if app.tabs["test"] == nil || app.tabs["test"].Ctrl != ctrl {
		t.Fatal("source tab lost its controller")
	}
	if app.activeTabID != view.TabID {
		t.Fatalf("active tab = %q, want the focused source's child %q", app.activeTabID, view.TabID)
	}
	child := app.tabs[view.TabID]
	if child == nil {
		t.Fatalf("child tab %q is missing", view.TabID)
	}
	// The tab's controller build may adopt the fork file as an exclusive v3
	// session, so its identity is compared through the branch meta the open path
	// wrote for the child path.
	meta, ok, metaErr := agent.LoadBranchMeta(childPath)
	if metaErr != nil || !ok {
		t.Fatalf("LoadBranchMeta(%q): ok=%v err=%v", childPath, ok, metaErr)
	}
	if child.TopicID == "" || child.TopicID != meta.TopicID {
		t.Fatalf("child tab topic = %q, branch meta topic = %q", child.TopicID, meta.TopicID)
	}
}

func TestCreateForkForTabKeepsChildWhenTabAttachFails(t *testing.T) {
	isolateDesktopUserDirs(t)

	childPath := filepath.Join(config.SessionDir(), "orphan-fork.jsonl")
	ctrl := &forkTargetsStubController{
		tabScopedActionController: newTabScopedActionController(),
		childID:                   childPath,
	}
	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.tabs["test"].TopicTitle = "Source topic"
	t.Cleanup(func() { forkTabBeforePublishHookForTest.Store(nil) })
	// Closing the source tab mid-flight makes the attach a no-op, which is the
	// same outcome as an attach that fails outright.
	hook := func() {
		app.mu.Lock()
		delete(app.tabs, "test")
		app.removeTabOrderLocked("test")
		if app.activeTabID == "test" {
			app.activeTabID = ""
		}
		app.mu.Unlock()
	}
	forkTabBeforePublishHookForTest.Store(&hook)

	view, err := app.CreateForkForTab("test", "turn-7", "op-1")
	if err != nil {
		t.Fatalf("CreateForkForTab: %v", err)
	}
	if view.Opened {
		t.Fatal("opened = true, want false when the new tab was not created")
	}
	if view.TabID != "" {
		t.Fatalf("tabId = %q, want empty", view.TabID)
	}
	if view.SessionID != childPath {
		t.Fatalf("sessionId = %q, want the created child %q", view.SessionID, childPath)
	}
	if view.Error == "" {
		t.Fatal("error is empty; the caller cannot offer a recovery entry")
	}
	if ctrl.creates != 1 {
		t.Fatalf("creates = %d, want exactly one child", ctrl.creates)
	}
	if ctrl.createOp != "op-1" {
		t.Fatalf("operationId = %q, want op-1 so a retry addresses the same child", ctrl.createOp)
	}
}

func TestCreateForkForTabReturnsCreateFailure(t *testing.T) {
	isolateDesktopUserDirs(t)

	ctrl := &forkTargetsStubController{
		tabScopedActionController: newTabScopedActionController(),
		createErr:                 errors.New("turn is not forkable"),
	}
	app := NewApp()
	app.setTestCtrl(ctrl, "")

	view, err := app.CreateForkForTab("test", "turn-7", "op-1")
	if err == nil {
		t.Fatal("CreateForkForTab: err = nil, want the controller's failure")
	}
	if view.SessionID != "" || view.TabID != "" || view.Opened || view.Error != "" {
		t.Fatalf("view = %+v, want the zero view alongside the error", view)
	}
	if len(app.tabs) != 1 || app.tabs["test"] == nil || app.activeTabID != "test" {
		t.Fatalf("tabs = %d, active = %q, want only the unchanged source tab", len(app.tabs), app.activeTabID)
	}
}
