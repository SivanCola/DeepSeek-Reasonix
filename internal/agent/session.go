// Package agent wires a Provider, a tool Registry, and a Session into the
// harness loop that drives a coding task to completion.
package agent

import "reasonix/internal/provider"

// Session holds the conversation history for one task.
type Session struct {
	Messages       []provider.Message
	rewriteVersion int // bumped each time the log is rewritten (compact/fold)
	turnCount        int // number of turns completed
	cumulativeTokens int // total tokens across all turns
}

// NewSession initializes a session with an optional system prompt.
func NewSession(system string) *Session {
	s := &Session{}
	if system != "" {
		s.Messages = append(s.Messages, provider.Message{Role: provider.RoleSystem, Content: system})
	}
	return s
}

// Add appends a message.
func (s *Session) Add(m provider.Message) {
	s.Messages = append(s.Messages, m)
}

// RewriteVersion returns the current rewrite version (compaction/fold counter),
// used by cache diagnostics to detect when the log was rewritten.
func (s *Session) RewriteVersion() int { return s.rewriteVersion }

// IncrementRewrite bumps the rewrite counter, signalling that the log was
// rewritten (e.g. by compaction).
func (s *Session) IncrementRewrite() { s.rewriteVersion++ }

// IncrementTurn advances the turn counter (called at the start of each Run).
func (s *Session) IncrementTurn() { s.turnCount++ }

// TurnCount returns the current turn number.
func (s *Session) TurnCount() int { return s.turnCount }

// CumulativeTokens returns the total tokens used across all turns.
func (s *Session) CumulativeTokens() int { return s.cumulativeTokens }

// AddTokens adds n to the cumulative token count.
func (s *Session) AddTokens(n int) { s.cumulativeTokens += n }
