package control

// A controller test that drives Compose has no agent to ask, so these helpers
// give it the same question a real turn asks: what does the model-visible
// history still carry? Delivering a turn is appending its composed text.

import (
	"slices"
	"strings"
	"sync"

	"reasonix/internal/contract/provider"
	"reasonix/internal/state/sessionstore"
)

type projectionHistory struct {
	mu    sync.Mutex
	turns []map[string]string
}

func (h *projectionHistory) deliver(composed string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.turns = append(h.turns, leadingBlocks(composed))
}

func (h *projectionHistory) newest(tag string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, turn := range slices.Backward(h.turns) {
		if block, ok := turn[tag]; ok {
			return block, true
		}
	}
	return "", false
}

func (h *projectionHistory) sees(s string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, turn := range h.turns {
		for _, block := range turn {
			if strings.Contains(block, s) {
				return true
			}
		}
	}
	return false
}

func (h *projectionHistory) forget() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.turns = nil
}

// leadingBlocks parses the host-injected blocks that open a composed turn.
func leadingBlocks(composed string) map[string]string {
	blocks := map[string]string{}
	rest := composed
	for {
		loc := sessionstore.ReTransientUserBlock.FindStringIndex(rest)
		if loc == nil || loc[0] != 0 {
			return blocks
		}
		head := strings.TrimLeft(rest[:loc[1]], " \t\r\n")
		for _, tag := range sessionstore.TransientUserBlockTags {
			if strings.HasPrefix(head, "<"+tag+">") || strings.HasPrefix(head, "<"+tag+" ") {
				blocks[tag] = strings.TrimRight(rest[:loc[1]], " \t\r\n")
				break
			}
		}
		rest = rest[loc[1]:]
	}
}

// seesSession answers notes from a session log, for tests that drive a runner
// rather than an agent.
func seesSession(sess *sessionstore.Session) func(string) bool {
	return func(s string) bool {
		for _, m := range sess.Snapshot() {
			if m.Role == provider.RoleUser && !m.LocalOnly && strings.Contains(m.Content, s) {
				return true
			}
		}
		return false
	}
}
