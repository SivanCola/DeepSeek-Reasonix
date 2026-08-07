package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// The command palette is what lets the lite shell skip a settings panel.
//
// The full desktop spends 7.4k lines of TSX on one, because every capability
// that needs a home gets a control, and every control needs a place to live.
// A palette inverts that: capabilities are listed, searched, and invoked by
// name, so adding one costs an entry here rather than a new panel section.
//
// The catalog lives in Go rather than in the frontend so it stays testable and
// so a new command needs no TypeScript at all.

// Command IDs. They are part of the shell's contract with the webview; renaming
// one breaks a keyboard shortcut a user has learned.
const (
	CommandNewConversation = "new"
	CommandCancelTurn      = "cancel"
	CommandCompact         = "compact"
	CommandShowTools       = "tools"
)

// Command is one palette entry.
type Command struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	// Enabled reports whether the command can run right now. Disabled commands
	// are still listed: hiding them would make the palette's contents depend on
	// timing, and a user who cannot find "Stop" while a turn runs will not trust
	// that it exists at all.
	Enabled bool `json:"enabled"`
}

// Commandable is the slice of the kernel controller the palette drives.
// *control.Controller satisfies it.
type Commandable interface {
	Cancel()
	NewSession() error
	Compact(ctx context.Context, instructions string) error
	ModelRef() string
}

// ErrUnknownCommand is returned for an ID the catalog does not define.
var ErrUnknownCommand = errors.New("session: unknown command")

// Commands returns the palette catalog for the host's current state.
func (h *Host) Commands() []Command {
	h.mu.Lock()
	conv, running, closed := h.current, h.running, h.closed
	h.mu.Unlock()

	live := conv != nil && !closed
	_, commandable := conv.(Commandable)

	return []Command{
		{
			ID:       CommandNewConversation,
			Title:    "New conversation",
			Subtitle: "Clear the transcript and start a fresh context",
			// Starting fresh mid-turn would leave the running turn writing into
			// a context nobody is reading.
			Enabled: live && commandable && !running,
		},
		{
			ID:       CommandCancelTurn,
			Title:    "Stop",
			Subtitle: "Interrupt the running turn",
			Enabled:  live && commandable && running,
		},
		{
			ID:       CommandCompact,
			Title:    "Compact conversation",
			Subtitle: "Summarise earlier turns to reclaim context",
			Enabled:  live && commandable && !running,
		},
		{
			ID:       CommandShowTools,
			Title:    "Show tools",
			Subtitle: "What is loaded, what is held back, and what it costs",
			Enabled:  live,
		},
	}
}

// RunCommand executes a palette command and returns the message to show, if
// any. An empty message means the command's effect speaks for itself.
func (h *Host) RunCommand(ctx context.Context, id string) (string, error) {
	h.mu.Lock()
	conv, running, closed := h.current, h.running, h.closed
	h.mu.Unlock()

	if closed {
		return "", ErrClosed
	}
	if conv == nil {
		return "", ErrNoConversation
	}

	// Report the command's own precondition rather than letting the kernel fail
	// obscurely: the palette lists disabled commands, so this path is reachable
	// whenever the UI's state lags the host's.
	for _, cmd := range h.Commands() {
		if cmd.ID != id {
			continue
		}
		if !cmd.Enabled {
			return "", fmt.Errorf("%q is not available right now", cmd.Title)
		}
		break
	}

	switch id {
	case CommandShowTools:
		return describeTools(conv), nil
	}

	c, ok := conv.(Commandable)
	if !ok {
		return "", ErrUnknownCommand
	}

	switch id {
	case CommandNewConversation:
		if err := c.NewSession(); err != nil {
			return "", fmt.Errorf("new conversation: %w", err)
		}
		h.mu.Lock()
		// A fresh context has never been told about the deferred roster.
		h.announced = map[string]bool{}
		h.mu.Unlock()
		return "Started a new conversation.", nil

	case CommandCancelTurn:
		if !running {
			return "", fmt.Errorf("no turn is running")
		}
		c.Cancel()
		return "", nil

	case CommandCompact:
		if err := c.Compact(ctx, ""); err != nil {
			return "", fmt.Errorf("compact: %w", err)
		}
		return "Compacted the conversation.", nil
	}
	return "", ErrUnknownCommand
}

// describeTools reports what the deferred tier is actually doing, which is the
// one piece of internal state a lite user has a reason to inspect: it explains
// both why a capability is not in the tool list and what holding it back saves.
func describeTools(conv Conversation) string {
	reg := registryOf(conv)
	if reg == nil {
		return "This conversation does not expose a tool registry."
	}

	exported := reg.Schemas()
	roster := reg.DeferredRoster()

	var b strings.Builder
	fmt.Fprintf(&b, "%d tools loaded", len(exported))
	if len(roster) == 0 {
		b.WriteString("; none held back.")
		return b.String()
	}

	var pending, ready int
	for _, entry := range roster {
		if entry.Activated {
			ready++
			continue
		}
		pending++
	}
	fmt.Fprintf(&b, ", %d held back until needed", pending)
	if ready > 0 {
		fmt.Fprintf(&b, " (%d loaded on demand this session)", ready)
	}
	fmt.Fprintf(&b, ".\n\nHeld back — the model loads these with %s:\n", tool.SearchToolName)
	for _, entry := range roster {
		if entry.Activated {
			continue
		}
		fmt.Fprintf(&b, "- %s", entry.Name)
		if entry.Unavailable != "" {
			fmt.Fprintf(&b, " (unavailable: %s)", entry.Unavailable)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
