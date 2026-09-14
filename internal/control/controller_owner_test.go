package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"reasonix/internal/session"
)

func newOwnedTestController(t testing.TB, options Options) *Controller {
	t.Helper()
	controller := New(options)
	t.Cleanup(func() {
		controller.Close()
		if options.SessionService == nil {
			return
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if !controller.Running() {
				if err := options.SessionService.CloseAll(context.Background()); !errors.Is(err, session.ErrRuntimeBusy) {
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
		t.Error("controller foreground or unbound runtime did not settle after close")
	})
	return controller
}
