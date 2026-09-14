package control

import (
	"context"

	"reasonix/internal/session"
)

func (c *Controller) appendSessionBatch(ctx context.Context, store *session.Session, batch session.Batch) (session.Commit, error) {
	if store == nil {
		return session.Commit{}, session.ErrSessionNotRunning
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return store.Append(ctx, batch)
}
