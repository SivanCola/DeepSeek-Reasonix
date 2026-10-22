package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"reasonix/internal/contract/config"
	"reasonix/internal/contract/event"
)

// A side conversation is a question asked beside the session: read-only, kept
// only in memory, and gone when closed. It never touches the main transcript or
// its history; what it costs is billed to this session under its own source.

const (
	maxSideQuoteRunes = 20000
	maxSideQuotes     = 8
)

var (
	// ErrSideUnavailable: this build assembled no side conversation runner.
	ErrSideUnavailable = errors.New("side conversations are not available in this session")
	// ErrSideUnknown is a side conversation id nobody holds (closed, or never opened).
	ErrSideUnknown = errors.New("no such side conversation")
	// ErrSideBusy: one question at a time per side conversation.
	ErrSideBusy = errors.New("this side conversation is still answering")
	// ErrSideLimit: the user's max_side_sessions is reached.
	ErrSideLimit = errors.New("too many side conversations are open")
	// ErrSideEmpty is a question with nothing in it.
	ErrSideEmpty = errors.New("a side conversation needs a question")
)

// SideConversation is one side conversation as the assembly builds it. Ask
// answers one question and remembers it; the controller routes its events.
type SideConversation interface {
	Ask(ctx context.Context, prompt string, sink event.Sink) (string, error)
}

// SideFactory opens a new, empty side conversation.
type SideFactory func() (SideConversation, error)

type sideEntry struct {
	conv SideConversation
	// asking is held for the duration of one question: a second is refused.
	asking sync.Mutex
	// cancelMu guards running, which stop reaches while a question holds asking.
	cancelMu sync.Mutex
	running  context.CancelFunc
	closed   bool
}

// sideSet owns the open side conversations, behind its own lock so a question
// that takes a minute never stalls an approval or a status poll.
type sideSet struct {
	mu      sync.Mutex
	factory SideFactory
	open    map[string]*sideEntry
	seq     int
	closed  bool
}

func (s *sideSet) openOne(limit int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.factory == nil {
		return "", ErrSideUnavailable
	}
	if s.closed {
		return "", ErrSideUnavailable
	}
	if limit > 0 && len(s.open) >= limit {
		return "", ErrSideLimit
	}
	conv, err := s.factory()
	if err != nil {
		return "", err
	}
	s.seq++
	id := fmt.Sprintf("side-%d", s.seq)
	if s.open == nil {
		s.open = map[string]*sideEntry{}
	}
	s.open[id] = &sideEntry{conv: conv}
	return id, nil
}

func (s *sideSet) get(id string) (*sideEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.open[id]
	return e, ok
}

// close drops one side conversation and stops any question it is answering.
func (s *sideSet) close(id string) {
	s.mu.Lock()
	e, ok := s.open[id]
	delete(s.open, id)
	s.mu.Unlock()
	if ok {
		e.stop()
	}
}

func (s *sideSet) closeAll() {
	s.mu.Lock()
	s.closed = true
	all := s.open
	s.open = nil
	s.mu.Unlock()
	for _, e := range all {
		e.stop()
	}
}

func (e *sideEntry) stop() {
	e.cancelMu.Lock()
	e.closed = true
	cancel := e.running
	e.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *sideEntry) setRunning(cancel context.CancelFunc) {
	e.cancelMu.Lock()
	e.running = cancel
	closed := e.closed
	e.cancelMu.Unlock()
	// A question that begins after the side was closed never gets to run.
	if closed && cancel != nil {
		cancel()
	}
}

// OpenSide starts an empty side conversation and returns its id.
func (c *Controller) OpenSide() (string, error) {
	return c.side.openOne(config.LoadForEdit(config.UserConfigPath()).SideSessionLimit())
}

// CloseSide discards a side conversation: its history, and any question still running.
func (c *Controller) CloseSide(id string) { c.side.close(id) }

// AskSide puts a question to a side conversation. quotes are passages the
// person selected in the main conversation; they ride this one question as
// quoted text and are not added to the main session.
// Progress is request-scoped; only usage is sent to the main session sink.
func (c *Controller) AskSide(ctx context.Context, id, question string, quotes []string, progress event.Sink) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", ErrSideEmpty
	}
	entry, ok := c.side.get(id)
	if !ok {
		return "", ErrSideUnknown
	}
	if !entry.asking.TryLock() {
		return "", ErrSideBusy
	}
	defer entry.asking.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	entry.setRunning(cancel)
	defer func() {
		cancel()
		entry.setRunning(nil)
	}()
	return entry.conv.Ask(ctx, sidePrompt(question, quotes), event.FuncSink(func(e event.Event) {
		if e.Kind == event.Usage {
			e.UsageSource = event.UsageSourceSide
			if c.sink != nil {
				c.sink.Emit(e)
			}
		} else if progress != nil {
			progress.Emit(e)
		}
	}))
}

// sidePrompt is the one message a question becomes: the quoted passages, fenced
// and capped, then the question.
func sidePrompt(question string, quotes []string) string {
	var b strings.Builder
	n := 0
	for _, q := range quotes {
		q = strings.TrimSpace(q)
		if q == "" || n >= maxSideQuotes {
			continue
		}
		n++
		if r := []rune(q); len(r) > maxSideQuoteRunes {
			q = string(r[:maxSideQuoteRunes]) + "\n[…cut]"
		}
		fmt.Fprintf(&b, "Quoted from the main conversation (passage %d):\n<<<\n%s\n>>>\n\n", n, q)
	}
	b.WriteString(question)
	return b.String()
}
