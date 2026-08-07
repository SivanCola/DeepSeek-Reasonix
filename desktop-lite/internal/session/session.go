// Package session drives exactly one Reasonix conversation.
//
// The full desktop multiplexes tabs, projects, and background runtimes, and
// most of its complexity is the bookkeeping that multiplexing demands. The lite
// shell deliberately does none of it: one workspace, one conversation, one
// transcript.
//
// That is a cache decision as much as a UI one. Every controller rebuild risks
// rewriting the provider-visible prefix, and the tab machinery rebuilds
// constantly — on tab switches, project changes, and session restores. A single
// long-lived conversation is the shape that keeps one prefix warm for as long
// as the user keeps working.
//
// Nothing here links cgo. The whole package is unit-testable without a display,
// a provider, or a network: only the outer shell binds the native toolkit.
package session

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"reasonix/internal/boot"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

var (
	// ErrNoConversation is returned when the host has nothing open yet.
	ErrNoConversation = errors.New("session: no conversation is open")
	// ErrBusy is returned when a turn is already running. A single-conversation
	// host has exactly one place for a reply to go, so turns never overlap.
	ErrBusy = errors.New("session: a turn is already running")
	// ErrClosed is returned once the host has been shut down.
	ErrClosed = errors.New("session: host is closed")
)

// Conversation is the slice of the kernel controller a lite session drives.
// *control.Controller satisfies it. Naming the surface here rather than
// depending on the concrete type is what keeps the lifecycle testable without
// standing up a provider, a workspace, and an MCP host.
type Conversation interface {
	Run(ctx context.Context, input string) error
	Running() bool
	Turn() int
	Close()
}

// Options describe the one conversation a lite host opens.
type Options struct {
	// WorkspaceRoot is the project the conversation is rooted in. Empty means
	// the process working directory.
	WorkspaceRoot string
	// Model overrides the configured model. Empty uses the resolved config,
	// which is the ordinary zero-configuration path.
	Model string
	// Sink receives runtime events for the UI. Nil discards them.
	Sink event.Sink
	// Deferred selects which tools are held back from the provider until the
	// model searches for them. The zero value defers MCP tools.
	Deferred DeferredPolicy
}

// Builder constructs the kernel conversation for a set of options. Production
// uses KernelBuilder; tests substitute a fake.
type Builder func(ctx context.Context, opts Options) (Conversation, error)

// KernelBuilder is the production Builder: the same boot assembly the CLI and
// the full desktop run, with no lite-specific forks in the kernel path.
func KernelBuilder(ctx context.Context, opts Options) (Conversation, error) {
	ctrl, err := boot.Build(ctx, boot.Options{
		WorkspaceRoot: opts.WorkspaceRoot,
		Model:         opts.Model,
		Sink:          opts.Sink,
		StatsSource:   "desktop-lite",
	})
	if err != nil {
		return nil, err
	}
	return ctrl, nil
}

// Host owns the process's single conversation and the rules around replacing
// it. The zero value is not usable; call NewHost.
type Host struct {
	mu    sync.Mutex
	build Builder

	current Conversation
	// generation increments on every successful Open. A turn captures the
	// generation it started under so a reply that lands after its conversation
	// was replaced cannot clear the new conversation's state.
	generation uint64
	running    bool
	closed     bool
	// announced records which deferred tools the model has already been told
	// about. A server that finishes connecting mid-session adds a short
	// follow-up on the next turn rather than repeating the whole roster.
	announced map[string]bool
}

// NewHost returns a host that builds conversations with b. A nil b uses
// KernelBuilder.
func NewHost(b Builder) *Host {
	if b == nil {
		b = KernelBuilder
	}
	return &Host{build: b, announced: map[string]bool{}}
}

// Open builds a conversation and makes it the live one, closing whatever it
// replaces.
//
// The new conversation is built before the old one is touched: a build that
// fails — bad workspace, unreachable provider, broken config — leaves the user
// exactly where they were instead of dropping them into an empty shell with
// their session gone. Recovery is retrying, not restarting.
func (h *Host) Open(ctx context.Context, opts Options) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrClosed
	}
	h.mu.Unlock()

	// Built outside the lock: assembly spawns MCP subprocesses and talks to the
	// network, and blocking every status read for its duration would freeze the
	// UI on startup.
	conv, err := h.build(ctx, opts)
	if err != nil {
		return fmt.Errorf("open conversation: %w", err)
	}

	// Reshape the tool set before the conversation becomes reachable. Deferring
	// is only free before the first request, and doing it here means no caller
	// can slip a turn in against a half-wired tool list.
	wireDeferredTools(conv, opts.Deferred)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		conv.Close()
		return ErrClosed
	}
	previous := h.current
	h.current = conv
	h.generation++
	// A turn still running belongs to the conversation being replaced; the
	// generation bump is what stops its completion from clearing this flag for
	// the new one.
	h.running = false
	// A new conversation carries a new tool registry, so nothing has been
	// announced to it yet.
	h.announced = map[string]bool{}
	h.mu.Unlock()

	if previous != nil {
		previous.Close()
	}
	return nil
}

// Send runs one turn against the live conversation.
func (h *Host) Send(ctx context.Context, input string) error {
	h.mu.Lock()
	switch {
	case h.closed:
		h.mu.Unlock()
		return ErrClosed
	case h.current == nil:
		h.mu.Unlock()
		return ErrNoConversation
	case h.running:
		h.mu.Unlock()
		return ErrBusy
	}
	conv := h.current
	gen := h.generation
	h.running = true
	h.mu.Unlock()

	// Deferred tools are announced in the turn itself rather than the system
	// prompt: the roster grows as MCP servers finish connecting, and dynamic
	// state in the prefix would rewrite it on every late handshake. Prepending
	// to the user turn keeps the conversation append-only.
	if notice := h.rosterNotice(conv); notice != "" {
		input = notice + "\n\n" + input
	}

	// Run is not held under the lock: a turn lasts as long as the model takes,
	// and status reads must stay responsive throughout.
	err := conv.Run(ctx, input)

	h.mu.Lock()
	if h.generation == gen {
		h.running = false
	}
	h.mu.Unlock()
	return err
}

// rosterNotice returns the announcement for deferred tools the model has not
// been told about yet, marking them announced. It returns "" when there is
// nothing new, which is the steady state after the first turn.
func (h *Host) rosterNotice(conv Conversation) string {
	reg := registryOf(conv)
	if reg == nil {
		return ""
	}
	// DeferredRoster reaches into tool callbacks, and a lazy MCP placeholder
	// takes its spawn mutex there; the host lock stays out of that path.
	roster := reg.DeferredRoster()
	if len(roster) == 0 {
		return ""
	}

	h.mu.Lock()
	fresh := make([]tool.DeferredEntry, 0, len(roster))
	for _, entry := range roster {
		if entry.Activated || h.announced[entry.Name] {
			continue
		}
		h.announced[entry.Name] = true
		fresh = append(fresh, entry)
	}
	h.mu.Unlock()

	return tool.RenderDeferredRoster(fresh)
}

// Ready reports whether a conversation is open and usable.
//
// Assembly finishes on its own schedule, and the frame announcing it is a
// one-shot event: a webview that had not finished mounting when it fired would
// wait forever. A frontend polls this instead of trusting that it was listening
// at the right moment.
func (h *Host) Ready() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.current != nil && !h.closed
}

// Running reports whether a turn is in flight.
func (h *Host) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.running
}

// Turn returns the live conversation's turn number, or 0 when nothing is open.
func (h *Host) Turn() int {
	h.mu.Lock()
	conv := h.current
	h.mu.Unlock()

	if conv == nil {
		return 0
	}
	// Called outside the lock: it reaches into controller state that takes its
	// own locks, and the host lock must never be held across kernel callbacks.
	return conv.Turn()
}

// Close shuts the host down. It is idempotent, so a window close racing an
// application quit cannot double-close the kernel.
func (h *Host) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	conv := h.current
	h.current = nil
	h.mu.Unlock()

	if conv != nil {
		conv.Close()
	}
}
