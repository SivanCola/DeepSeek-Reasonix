package control

import (
	"context"
	"errors"

	"reasonix/internal/sessionv3"
)

func (c *Controller) beginV3RuntimeActivity(ctx context.Context, name string) (context.Context, *sessionv3.Activity, error) {
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return ctx, nil, nil
	}
	runtimeCtx, activity, err := runtime.BeginOwnedActivity(ctx, name)
	if err != nil {
		return ctx, nil, err
	}
	c.v3ActivityMu.Lock()
	c.v3Activity = activity
	c.v3ActivityMu.Unlock()
	return runtimeCtx, activity, nil
}

func (c *Controller) finishV3RuntimeActivity(activity *sessionv3.Activity) {
	if activity == nil {
		return
	}
	c.v3ActivityMu.Lock()
	if c.v3Activity == activity {
		c.v3Activity = nil
	}
	c.v3ActivityMu.Unlock()
	activity.Finish(nil)
}

func (c *Controller) appendV3Batch(ctx context.Context, store *sessionv3.Session, batch sessionv3.Batch) (sessionv3.Commit, error) {
	if store == nil {
		return sessionv3.Commit{}, sessionv3.ErrSessionNotRunning
	}
	c.v3ActivityMu.Lock()
	activity := c.v3Activity
	c.v3ActivityMu.Unlock()
	if activity != nil {
		commit, err := activity.Append(ctx, batch)
		if !errors.Is(err, sessionv3.ErrStaleActivity) {
			return commit, err
		}
		_, runtime, exclusive := c.v3Binding()
		if exclusive && runtime != nil && runtime.Session() == store {
			return runtime.RecordRecovery(ctx, batch)
		}
		return sessionv3.Commit{}, err
	}
	return store.Append(ctx, batch)
}
