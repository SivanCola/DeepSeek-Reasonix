package control

import "reasonix/internal/state/sessiontemp"

const sessionScratchTag = "session-scratch"

const scratchGuidance = "This session has a private scratch directory, $TMPDIR, which the host deletes when the session ends. " +
	"Put throwaway tests and probes at `$TMPDIR/<name>`, spelled that way in bash (quoted) and in the file tools, " +
	"instead of the repository, /tmp or `mktemp -d`, and do not delete it."

const scratchRetracted = "No session scratch directory is available now; do not rely on $TMPDIR for scratch files."

// scratchAnchor tells the model the one spelling of its scratch directory that
// bash and the file tools both resolve. The host path differs per platform and
// per sandbox (bwrap binds the directory at /tmp), so $TMPDIR is the identity
// and the path is never projected. Only a manager the assembly supplied counts:
// it is the one the tools are bound to.
type scratchAnchor struct {
	temp *sessiontemp.Manager
	debt projectionDebt
}

// InheritScratchFrom carries what prev's model was told across a rebuild that
// keeps its conversation, so an unchanged block is not sent twice.
func (c *Controller) InheritScratchFrom(prev *Controller) {
	if prev == nil {
		return
	}
	c.scratch.debt.inherit(&prev.scratch.debt)
}

// owed asks the manager rather than waiting to be told: a generation that can
// no longer be created changes the answer, and noticing is the projection's job.
func (s *scratchAnchor) owed() string {
	if scratchDir(s.temp) != "" {
		return s.debt.owed(scratchGuidance)
	}
	if !s.debt.sent() {
		return s.debt.owed("")
	}
	return s.debt.owed(scratchRetracted)
}

// scratchDir names the current generation, creating it when no command has yet:
// a directory the model is told about must exist before its first write.
func scratchDir(m *sessiontemp.Manager) string {
	if m == nil {
		return ""
	}
	if dir := m.Dir(); dir != "" {
		return dir
	}
	lease, err := m.Acquire()
	if err != nil {
		return ""
	}
	defer lease.Release()
	return lease.Dir()
}
