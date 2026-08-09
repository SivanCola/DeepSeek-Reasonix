package agent

import (
	"context"
	"strings"
)

type hostTurnIDKey struct{}
type hostTurnAcceptedKey struct{}

// WithHostTurnID attaches a host-owned idempotency key to a turn. The value is
// consumed only when the user message is persisted; it is never provider
// visible.
func WithHostTurnID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, hostTurnIDKey{}, strings.TrimSpace(id))
}

func HostTurnID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(hostTurnIDKey{}).(string)
	return strings.TrimSpace(value)
}

// WithHostTurnAccepted registers a one-shot callback invoked immediately
// after the user message has been appended to the session. Hosts use this to
// advance an ingress journal only after the controller owns the turn.
func WithHostTurnAccepted(ctx context.Context, fn func()) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, hostTurnAcceptedKey{}, fn)
}

func markHostTurnAccepted(ctx context.Context) {
	if ctx == nil {
		return
	}
	fn, _ := ctx.Value(hostTurnAcceptedKey{}).(func())
	if fn != nil {
		fn()
	}
}
