package client

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/remote/protocol"
)

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
