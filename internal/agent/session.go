// Package agent wires a Provider, a tool Registry, and a Session into the
// harness loop that drives a coding task to completion.
package agent

import "reasonix/internal/provider"

// Session holds the conversation history for one task.
type Session struct {
	Messages         []provider.Message
	rewriteVersion   int // bumped each time the log is rewritten (compact/fold)
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

// RewiteVersion returns the current rewrite version.
func (s *Session) RewiteVersion() int { return s.rewriteVersion }

// IncrementRewite bumps the rewrite version by 1.
func (s *Session) IncrementRewite() { s.rewriteVersion++ }

// IncrementTurn bumps the turn count by 1.
func (s *Session) IncrementTurn() { s.turnCount++ }

// TurnCount returns the number of completed turns.
func (s *Session) TurnCount() int { return s.turnCount }

// CumulativeTokens returns the total tokens across all turns.
func (s *Session) CumulativeTokens() int { return s.cumulativeTokens }

// AddTokens adds n tokens to the cumulative total.
func (s *Session) AddTokens(n int) { s.cumulativeTokens += n }

// HasContent returns true when the session carries at least one user,
// assistant, or tool message — i.e. more than just a system prompt. An
// "empty" conversation that has never been used should not be persisted.
func (s *Session) HasContent() bool {
	for _, m := range s.Messages {
		if m.Role != provider.RoleSystem {
			return true
		}
	}
	return false
}
