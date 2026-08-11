package main

import (
	"context"
	"testing"
	"time"
)

func TestEmitConfigLoadWarningsRequiresContextAndOwnsPayload(t *testing.T) {
	if (&App{}).emitConfigLoadWarnings([]string{"warning"}) {
		t.Fatal("handler accepted warnings without a Wails context")
	}
	if (&App{ctx: context.Background()}).emitConfigLoadWarnings(nil) {
		t.Fatal("handler accepted an empty warning list")
	}

	type emittedEvent struct {
		name     string
		warnings []string
	}
	started := make(chan struct{})
	release := make(chan struct{})
	events := make(chan emittedEvent, 1)
	app := &App{ctx: context.Background()}
	app.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		close(started)
		<-release
		warnings, _ := payload[0].([]string)
		events <- emittedEvent{name: name, warnings: warnings}
	}

	warnings := []string{"user config is invalid"}
	if !app.emitConfigLoadWarnings(warnings) {
		t.Fatal("handler rejected warnings with a Wails context")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime event emitter did not start")
	}
	warnings[0] = "mutated by caller"
	close(release)

	select {
	case got := <-events:
		if got.name != configLoadWarningsEvent {
			t.Fatalf("event name = %q, want %q", got.name, configLoadWarningsEvent)
		}
		if len(got.warnings) != 1 || got.warnings[0] != "user config is invalid" {
			t.Fatalf("event warnings = %v, want owned original payload", got.warnings)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime event was not delivered")
	}
}
