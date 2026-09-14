package control

// turnLifecycleBoundary exposes exact execution and TurnDone fan-out
// transitions without making observers poll scheduler-dependent state.
type turnLifecycleBoundary struct {
	idleDone      chan struct{}
	finishingDone chan struct{}
}

func (b *turnLifecycleBoundary) beginIdle() {
	if b.idleDone == nil {
		b.idleDone = make(chan struct{})
	}
}

func (b *turnLifecycleBoundary) endIdle() {
	if b.idleDone == nil {
		return
	}
	close(b.idleDone)
	b.idleDone = nil
}

func (b *turnLifecycleBoundary) beginFinishing(finishing bool) {
	if finishing {
		b.finishingDone = make(chan struct{})
	}
}

func (b *turnLifecycleBoundary) endFinishing() {
	if b.finishingDone == nil {
		return
	}
	close(b.finishingDone)
	b.finishingDone = nil
}

func (c *Controller) finishTurnFanoutLocked() {
	c.finishing = false
	c.canceling = false
	c.turnBoundary.endFinishing()
}

func (c *Controller) closeTurnBoundariesLocked() {
	c.finishing = false
	c.turnBoundary.endFinishing()
	if !c.running {
		c.turnBoundary.endIdle()
	}
}

// Running reports whether a turn is currently in flight.
func (c *Controller) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running || c.finishing
}

// TurnIdleDone returns a boundary that closes when the currently admitted turn
// chain releases the running-or-finishing admission gate. A turn parked during
// TurnDone fan-out remains in the same chain, so the boundary stays open until
// that turn also completes. Idle controllers return ok=false.
func (c *Controller) TurnIdleDone() (done <-chan struct{}, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if (!c.running && !c.finishing) || c.turnBoundary.idleDone == nil {
		return nil, false
	}
	return c.turnBoundary.idleDone, true
}

// TurnFinishingDone returns the current TurnDone delivery boundary.
func (c *Controller) TurnFinishingDone() (done <-chan struct{}, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.finishing || c.turnBoundary.finishingDone == nil {
		return nil, false
	}
	return c.turnBoundary.finishingDone, true
}
