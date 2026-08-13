package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/permission"
)

func TestMemoryApprovalStillPromptsUnderAuto(t *testing.T) {
	approvalRequests := make(chan event.Approval, 1)
	c := New(Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				approvalRequests <- e.Approval
			}
		}),
	})
	c.SetToolApprovalMode(ToolApprovalAuto)

	done := make(chan bool, 1)
	errs := make(chan error, 1)
	go func() {
		allow, _, err := c.requestApproval(context.Background(), "remember", "", nil)
		if err != nil {
			errs <- err
			return
		}
		done <- allow
	}()

	var approval event.Approval
	select {
	case approval = <-approvalRequests:
	case <-time.After(30 * time.Second):
		t.Fatal("memory approval request was not emitted under auto approval")
	}
	if approval.Tool != "remember" {
		t.Fatalf("approval tool = %q, want remember", approval.Tool)
	}

	select {
	case err := <-errs:
		t.Fatalf("requestApproval: %v", err)
	case allow := <-done:
		t.Fatalf("memory approval must wait for manual approval under auto, got allow=%v", allow)
	case <-time.After(50 * time.Millisecond):
	}

	c.Approve(approval.ID, true, true, true)
	select {
	case err := <-errs:
		t.Fatalf("requestApproval: %v", err)
	case allow := <-done:
		if !allow {
			t.Fatal("manual approval should allow memory write")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("memory approval stayed blocked after Approve")
	}
}

func TestYoloAllowsMemoryWithoutPrompt(t *testing.T) {
	feedback := json.RawMessage(`{"name":"python-env-cross-project","type":"feedback","description":"cross-project Python env","body":"Embedded Python is unreliable."}`)
	var approvalRequested bool
	c := New(Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				approvalRequested = true
			}
		}),
	})
	c.SetToolApprovalMode(ToolApprovalYolo)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, toolName := range []string{"remember", "forget"} {
		allow, remember, err := c.requestApproval(ctx, toolName, "", feedback)
		if err != nil || !allow || remember {
			t.Fatalf("requestApproval(%s) in YOLO = (%v,%v,%v), want allow without persist", toolName, allow, remember, err)
		}
	}

	gate := c.newInteractiveGate()
	for _, toolName := range []string{"remember", "forget"} {
		allow, reason, err := gate.Check(context.Background(), toolName, feedback, false)
		if err != nil || !allow {
			t.Fatalf("interactive gate %s under YOLO = (%v,%q,%v), want allow", toolName, allow, reason, err)
		}
		if got := gate.Policy.Decide(toolName, false, feedback); got != permission.Allow {
			t.Fatalf("%s policy under YOLO = %v, want allow", toolName, got)
		}
	}
	if approvalRequested {
		t.Fatal("YOLO must not emit a remember/forget approval prompt")
	}
}

func TestToolApprovalModeAutoForcesMemoryAskRules(t *testing.T) {
	c := New(Options{})
	c.SetToolApprovalMode(ToolApprovalAuto)

	gate := c.newInteractiveGate()
	for _, toolName := range []string{"remember", "forget"} {
		if got := gate.Policy.Decide(toolName, false, json.RawMessage(`{}`)); got != permission.Ask {
			t.Fatalf("%s under auto mode = %v, want ask", toolName, got)
		}
	}
}

func TestToolApprovalModeYoloAllowsMemoryAndHonorsDeny(t *testing.T) {
	c := New(Options{
		Policy: permission.New("ask", nil, nil, []string{"remember"}),
	})
	c.SetToolApprovalMode(ToolApprovalYolo)

	gate := c.newInteractiveGate()
	if got := gate.Policy.Decide("forget", false, json.RawMessage(`{}`)); got != permission.Allow {
		t.Fatalf("forget under yolo mode = %v, want allow", got)
	}
	if got := gate.Policy.Decide("bash", false, json.RawMessage(`{"command":"go test ./..."}`)); got != permission.Allow {
		t.Fatalf("regular tool under yolo mode = %v, want allow", got)
	}
	allow, _, err := gate.Check(context.Background(), "remember", json.RawMessage(`{"name":"blocked","body":"no"}`), false)
	if err != nil || allow {
		t.Fatalf("denied remember under yolo = (%v,%v), want deny", allow, err)
	}
}

func TestSetAutoApproveToolsDrainsPendingMemoryApproval(t *testing.T) {
	approvalRequests := make(chan event.Approval, 1)
	c := New(Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				approvalRequests <- e.Approval
			}
		}),
	})

	done := make(chan bool, 1)
	errs := make(chan error, 1)
	go func() {
		allow, _, err := c.requestApproval(context.Background(), "forget", "", nil)
		if err != nil {
			errs <- err
			return
		}
		done <- allow
	}()

	select {
	case <-approvalRequests:
	case <-time.After(30 * time.Second):
		t.Fatal("memory approval request was not emitted")
	}

	c.SetAutoApproveTools(true)

	select {
	case err := <-errs:
		t.Fatalf("requestApproval: %v", err)
	case allow := <-done:
		if !allow {
			t.Fatal("pending memory approval should be allowed when YOLO turns on")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("pending memory approval stayed blocked after YOLO turned on")
	}
}
