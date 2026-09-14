package control

import (
	"context"
	"testing"
)

func newOwnedTestController(t testing.TB, options Options) *Controller {
	t.Helper()
	if options.SessionService != nil {
		service := options.SessionService
		t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	}
	controller := New(options)
	t.Cleanup(controller.Close)
	return controller
}
