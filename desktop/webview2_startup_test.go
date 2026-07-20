package main

import (
	"context"
	"testing"
	"time"
)

func TestAwaitStartupFallbackFiresWhenDOMIsNotReady(t *testing.T) {
	timeout := make(chan time.Time, 1)
	timeout <- time.Now()
	if !awaitStartupFallback(context.Background(), timeout, func() bool { return false }) {
		t.Fatal("startup fallback did not fire after timeout")
	}
}

func TestAwaitStartupFallbackSkipsReadyWindow(t *testing.T) {
	timeout := make(chan time.Time, 1)
	timeout <- time.Now()
	if awaitStartupFallback(context.Background(), timeout, func() bool { return true }) {
		t.Fatal("startup fallback fired after domReady")
	}
}

func TestAwaitStartupFallbackStopsWithApplication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if awaitStartupFallback(ctx, make(chan time.Time), func() bool { return false }) {
		t.Fatal("startup fallback fired after application shutdown")
	}
}
