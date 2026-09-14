package control

import "reasonix/internal/session"

// turnFinishingBoundary lets asynchronous frontends wait for TurnDone fan-out
// without waiting for a genuinely running model turn.
type turnFinishingBoundary struct {
	done chan struct{}
}

func (b *turnFinishingBoundary) begin(finishing bool) {
	if finishing {
		b.done = make(chan struct{})
	}
}

func (b *turnFinishingBoundary) end() {
	if b.done == nil {
		return
	}
	close(b.done)
	b.done = nil
}

// Running reports whether a turn is currently in flight.
func (c *Controller) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	if c.turns.phase == session.RuntimeRecoveryRequired && c.turns.done != nil {
		return true
	}
	return c.bodyActiveLocked() || c.finalizingLocked()
}

// TurnFinishingDone returns the current TurnDone delivery boundary.
func (c *Controller) TurnFinishingDone() (done <-chan struct{}, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.finalizingLocked() || c.turns.finishingBound.done == nil {
		return nil, false
	}
	return c.turns.finishingBound.done, true
}
