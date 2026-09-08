package control

import "reasonix/internal/agent"

// ValidateSessionModel checks saved identity before any lease or transcript
// transition. Explicit model changes deliberately build a new runtime instead.
func (c *Controller) ValidateSessionModel(path string) error {
	if c.resolveSessionModel == nil {
		return nil
	}
	model, identity, ok := agent.LoadSessionModelSelection(path)
	if !ok {
		return nil
	}
	_, err := c.resolveSessionModel(model, identity)
	return err
}
