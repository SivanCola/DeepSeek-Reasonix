package client

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/remote/protocol"
)

func TestAuthorizeParamsFillsTypedAuthorityFields(t *testing.T) {
	c := &Client{state: State{
		Initialized: true, HostEpoch: "host-live", WorkspaceID: "workspace-live",
		Target:       protocol.RuntimeTarget{WorkspaceID: "workspace-live", SessionID: "session-live"},
		RuntimeEpoch: "runtime-live", SnapshotID: "snapshot-live", CurrentTurnID: "turn-live",
	}}

	listValue, err := c.authorizeParams(protocol.MethodSessionList, protocol.SessionListParams{})
	if err != nil {
		t.Fatalf("authorize typed session/list: %v", err)
	}
	list := listValue.(protocol.SessionListParams)
	if list.ExpectedHostEpoch != "host-live" || list.WorkspaceID != "workspace-live" {
		t.Fatalf("session/list authority = %+v", list)
	}

	submitValue, err := c.authorizeParams(protocol.MethodSessionSubmit, protocol.SessionSubmitParams{Input: "hello", DisplayText: "hello"})
	if err != nil {
		t.Fatalf("authorize typed session/submit: %v", err)
	}
	submit := submitValue.(protocol.SessionSubmitParams)
	if submit.ExpectedHostEpoch != "host-live" || submit.Target.SessionID != "session-live" || submit.ExpectedRuntimeEpoch != "runtime-live" {
		t.Fatalf("session/submit authority = %+v", submit.SessionMutation)
	}
	if !strings.HasPrefix(string(submit.RequestID), "request_") {
		t.Fatalf("session/submit requestId = %q", submit.RequestID)
	}
}

func TestAuthorizeParamsPreservesRequestIDButOverridesSpoofedAuthority(t *testing.T) {
	c := &Client{state: State{
		HostEpoch: "host-live", WorkspaceID: "workspace-live",
		Target:       protocol.RuntimeTarget{WorkspaceID: "workspace-live", SessionID: "session-live"},
		RuntimeEpoch: "runtime-live",
	}}
	value, err := c.authorizeParams(protocol.MethodSessionSubmit, protocol.SessionSubmitParams{
		SessionMutation: protocol.SessionMutation{
			RequestID: "request-stable", ExpectedHostEpoch: "host-spoofed",
			Target:               protocol.RuntimeTarget{WorkspaceID: "workspace-spoofed", SessionID: "session-spoofed"},
			ExpectedRuntimeEpoch: "runtime-spoofed",
		},
		Input: "hello", DisplayText: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := value.(protocol.SessionSubmitParams)
	if got.RequestID != "request-stable" {
		t.Fatalf("requestId = %q, want stable caller retry id", got.RequestID)
	}
	if got.ExpectedHostEpoch != "host-live" || got.Target != c.state.Target || got.ExpectedRuntimeEpoch != "runtime-live" {
		t.Fatalf("spoofed authority was not replaced: %+v", got.SessionMutation)
	}
}

func TestQueueOverflowSignalsResyncOutOfBand(t *testing.T) {
	resync := make(chan protocol.SessionResyncRequired, 1)
	c := &Client{
		notifyCh: make(chan any, 1),
		state: State{
			Initialized: true, SubscriptionID: "subscription_test", HostEpoch: "host_test",
			Target:       protocol.RuntimeTarget{WorkspaceID: "workspace_test", SessionID: "session_test"},
			RuntimeEpoch: "runtime_test",
		},
		callbacks: Callbacks{OnResyncRequired: func(value protocol.SessionResyncRequired) { resync <- value }},
	}
	c.notifyCh <- struct{}{}
	c.enqueue(protocol.SessionEvent{})
	select {
	case got := <-resync:
		if got.Reason != protocol.ResyncQueueOverflow || got.SubscriptionID != "subscription_test" {
			t.Fatalf("resync = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("queue overflow signal was lost behind the full queue")
	}
}

func TestUnexpectedCloseNotifiesOnceAfterInitialize(t *testing.T) {
	notified := make(chan struct{}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		state: State{Initialized: true}, cancel: cancel,
		callbacks: Callbacks{OnDisconnected: func() { notified <- struct{}{} }},
	}
	c.close(false)
	c.close(false)
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("unexpected close did not notify")
	}
	select {
	case <-notified:
		t.Fatal("close notified more than once")
	default:
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("close did not cancel client context")
	}
}
