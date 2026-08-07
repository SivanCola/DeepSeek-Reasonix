package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/tool"
)

// cmdConv is a conversation that also implements Commandable, the way
// *control.Controller does.
type cmdConv struct {
	regConv

	mu         sync.Mutex
	cancels    int
	newSession int
	compacts   int
	newErr     error
	compactErr error
}

func (c *cmdConv) Cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancels++
}

func (c *cmdConv) NewSession() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.newSession++
	return c.newErr
}

func (c *cmdConv) Compact(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.compacts++
	return c.compactErr
}

func (c *cmdConv) ModelRef() string { return "test/model" }

func (c *cmdConv) counts() (cancels, sessions, compacts int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancels, c.newSession, c.compacts
}

func newCmdConv(names ...string) *cmdConv {
	return &cmdConv{regConv: newRegConv(names...)}
}

func findCommand(t *testing.T, cmds []Command, id string) Command {
	t.Helper()
	for _, c := range cmds {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("command %q missing from the catalog: %+v", id, cmds)
	return Command{}
}

func TestCommandsCatalogIsComplete(t *testing.T) {
	h := NewHost(builderFor(newCmdConv("bash")))
	mustOpen(t, h)
	defer h.Close()

	cmds := h.Commands()
	for _, id := range []string{CommandNewConversation, CommandCancelTurn, CommandCompact, CommandShowTools} {
		cmd := findCommand(t, cmds, id)
		if cmd.Title == "" {
			t.Errorf("command %q has no title", id)
		}
	}
}

// A disabled command stays listed so its existence and shortcut remain
// discoverable; only Enabled changes with state.
func TestCommandAvailabilityFollowsTurnState(t *testing.T) {
	conv := newCmdConv("bash")
	conv.release = make(chan struct{}) // block the turn
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer func() {
		close(conv.release)
		h.Close()
	}()

	idle := h.Commands()
	if !findCommand(t, idle, CommandNewConversation).Enabled {
		t.Error("New conversation should be available while idle")
	}
	if findCommand(t, idle, CommandCancelTurn).Enabled {
		t.Error("Stop should not be available while idle")
	}

	go func() { _ = h.Send(context.Background(), "hello") }()
	conv.awaitStart(t)

	busy := h.Commands()
	if !findCommand(t, busy, CommandCancelTurn).Enabled {
		t.Error("Stop should be available while a turn runs")
	}
	if findCommand(t, busy, CommandNewConversation).Enabled {
		t.Error("New conversation should not be available mid-turn")
	}
	if findCommand(t, busy, CommandCompact).Enabled {
		t.Error("Compact should not be available mid-turn")
	}
	if len(busy) != len(idle) {
		t.Fatalf("the catalog changed size with state: %d then %d", len(idle), len(busy))
	}
}

func TestRunCommandRejectsADisabledCommand(t *testing.T) {
	conv := newCmdConv("bash")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	_, err := h.RunCommand(context.Background(), CommandCancelTurn)
	if err == nil {
		t.Fatal("Stop should be refused while idle")
	}
	if !strings.Contains(err.Error(), "Stop") {
		t.Fatalf("error %q does not name the command", err)
	}
	if cancels, _, _ := conv.counts(); cancels != 0 {
		t.Fatalf("a refused command still reached the kernel (%d cancels)", cancels)
	}
}

func TestRunCommandNewConversationResetsAnnouncements(t *testing.T) {
	conv := newCmdConv("bash", "mcp__figma__get_screenshot")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	if err := h.Send(context.Background(), "first"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if _, err := h.RunCommand(context.Background(), CommandNewConversation); err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if _, sessions, _ := conv.counts(); sessions != 1 {
		t.Fatalf("NewSession called %d times, want 1", sessions)
	}

	if err := h.Send(context.Background(), "second"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	conv.mu.Lock()
	second := conv.inputs[1]
	conv.mu.Unlock()

	// A fresh context has never seen the roster, so it must be re-announced.
	if !strings.Contains(second, "mcp__figma__get_screenshot") {
		t.Fatalf("the roster was not re-announced to a new conversation:\n%s", second)
	}
}

func TestRunCommandCancelsARunningTurn(t *testing.T) {
	conv := newCmdConv("bash")
	conv.release = make(chan struct{})
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer func() {
		close(conv.release)
		h.Close()
	}()

	go func() { _ = h.Send(context.Background(), "hello") }()
	conv.awaitStart(t)

	if _, err := h.RunCommand(context.Background(), CommandCancelTurn); err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if cancels, _, _ := conv.counts(); cancels != 1 {
		t.Fatalf("Cancel called %d times, want 1", cancels)
	}
}

func TestRunCommandCompacts(t *testing.T) {
	conv := newCmdConv("bash")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	msg, err := h.RunCommand(context.Background(), CommandCompact)
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if msg == "" {
		t.Error("compact should report what it did")
	}
	if _, _, compacts := conv.counts(); compacts != 1 {
		t.Fatalf("Compact called %d times, want 1", compacts)
	}
}

func TestRunCommandSurfacesKernelFailure(t *testing.T) {
	conv := newCmdConv("bash")
	conv.compactErr = errors.New("nothing to compact")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	if _, err := h.RunCommand(context.Background(), CommandCompact); err == nil {
		t.Fatal("a failing kernel call should surface")
	}
}

func TestShowToolsExplainsTheDeferredTier(t *testing.T) {
	conv := newCmdConv("bash", "mcp__figma__get_screenshot", "mcp__slack__post_message")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	msg, err := h.RunCommand(context.Background(), CommandShowTools)
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if !strings.Contains(msg, "held back") {
		t.Fatalf("Show tools does not explain what is withheld:\n%s", msg)
	}
	if !strings.Contains(msg, "mcp__figma__get_screenshot") {
		t.Fatalf("Show tools does not name the withheld tools:\n%s", msg)
	}
	if !strings.Contains(msg, tool.SearchToolName) {
		t.Fatalf("Show tools does not say how they get loaded:\n%s", msg)
	}
}

func TestShowToolsWithNothingDeferred(t *testing.T) {
	conv := newCmdConv("bash", "read_file")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	msg, err := h.RunCommand(context.Background(), CommandShowTools)
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if !strings.Contains(msg, "none held back") {
		t.Fatalf("unexpected message with an empty deferred tier:\n%s", msg)
	}
}

func TestRunCommandRejectsUnknownAndClosed(t *testing.T) {
	conv := newCmdConv("bash")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)

	if _, err := h.RunCommand(context.Background(), "not-a-command"); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("RunCommand = %v, want ErrUnknownCommand", err)
	}

	h.Close()
	if _, err := h.RunCommand(context.Background(), CommandCompact); !errors.Is(err, ErrClosed) {
		t.Fatalf("RunCommand after Close = %v, want ErrClosed", err)
	}
}

// A conversation that is not Commandable must not crash the palette.
func TestCommandsDegradeWithoutAKernelController(t *testing.T) {
	h := NewHost(builderFor(newInstantConv()))
	mustOpen(t, h)
	defer h.Close()

	for _, cmd := range h.Commands() {
		if cmd.ID != CommandShowTools && cmd.Enabled {
			t.Errorf("command %q reported enabled without a controller", cmd.ID)
		}
	}
}
