package control

import (
	"context"
	"testing"
	"time"
)

func newOwnedTestController(t testing.TB, options Options) *Controller {
	t.Helper()
	controller := New(options)
	t.Cleanup(func() {
		controller.Close()
		if options.SessionService == nil {
			return
		}
		select {
		case <-controller.closeFinalized:
			// Close an idle retained runtime when this is the final owner. A
			// runtime still bound by another controller belongs to that
			// controller's cleanup instead.
			_ = options.SessionService.CloseAll(context.Background())
		case <-time.After(5 * time.Second):
			t.Error("controller resources did not settle after close")
		}
	})
	return controller
}
