package control

import (
	"context"

	"reasonix/internal/session"
)

func goalRuntimeRetired(c *Controller, service *session.Service, runtime *session.Runtime, settled bool) bool {
	current, ok := service.Runtime(runtime.Ref())
	if settled && !c.Running() && ok && current == runtime {
		_ = service.Close(context.Background(), runtime.Ref())
		return true
	}
	return !ok || current != runtime
}
