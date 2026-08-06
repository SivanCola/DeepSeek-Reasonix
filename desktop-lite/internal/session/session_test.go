package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const settle = 5 * time.Second

// fakeConv is a Conversation whose turn boundaries a test controls, so
// interleavings are deterministic instead of timing-dependent.
type fakeConv struct {
	started chan struct{}
	release chan struct{}

	mu     sync.Mutex
	closes int
	runs   int
	runErr error
	inputs []string
}

// newInstantConv returns a conversation whose turns finish immediately.
func newInstantConv() *fakeConv {
	c := newBlockingConv()
	close(c.release)
	return c
}

// newBlockingConv returns a conversation whose turn parks until released.
func newBlockingConv() *fakeConv {
	return &fakeConv{started: make(chan struct{}, 1), release: make(chan struct{})}
}

func (f *fakeConv) Run(_ context.Context, input string) error {
	f.mu.Lock()
	f.runs++
	f.inputs = append(f.inputs, input)
	err := f.runErr
	f.mu.Unlock()

	select {
	case f.started <- struct{}{}:
	default:
	}
	<-f.release
	return err
}

func (f *fakeConv) Running() bool { return false }
func (f *fakeConv) Turn() int     { return 7 }

func (f *fakeConv) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
}

func (f *fakeConv) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

func (f *fakeConv) awaitStart(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(settle):
		t.Fatal("turn never started")
	}
}

// builderFor hands out the given conversations in order. Opens are sequential
// in every test, so the cursor needs no synchronization.
func builderFor(convs ...Conversation) Builder {
	i := 0
	return func(context.Context, Options) (Conversation, error) {
		if i >= len(convs) {
			return nil, errors.New("builder exhausted")
		}
		c := convs[i]
		i++
		return c, nil
	}
}

func mustOpen(t *testing.T, h *Host) {
	t.Helper()
	if err := h.Open(context.Background(), Options{}); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
}

func TestOpenReplacesAndClosesThePreviousConversation(t *testing.T) {
	first, second := newInstantConv(), newInstantConv()
	h := NewHost(builderFor(first, second))

	mustOpen(t, h)
	mustOpen(t, h)
	defer h.Close()

	if got := first.closeCount(); got != 1 {
		t.Fatalf("previous conversation closed %d times, want 1", got)
	}
	if got := second.closeCount(); got != 0 {
		t.Fatalf("live conversation was closed %d times, want 0", got)
	}
}

// A failed rebuild must not cost the user the session they already had.
func TestOpenFailureKeepsTheExistingConversation(t *testing.T) {
	live := newInstantConv()
	calls := 0
	h := NewHost(func(context.Context, Options) (Conversation, error) {
		calls++
		if calls == 1 {
			return live, nil
		}
		return nil, errors.New("provider unreachable")
	})
	defer h.Close()

	mustOpen(t, h)
	if err := h.Open(context.Background(), Options{}); err == nil {
		t.Fatal("second Open should have failed")
	}

	if got := live.closeCount(); got != 0 {
		t.Fatalf("failed Open closed the live conversation (%d times)", got)
	}
	if err := h.Send(context.Background(), "still here"); err != nil {
		t.Fatalf("live conversation is unusable after a failed Open: %v", err)
	}
}

func TestSendWithoutAnOpenConversation(t *testing.T) {
	h := NewHost(builderFor())

	if err := h.Send(context.Background(), "hello"); !errors.Is(err, ErrNoConversation) {
		t.Fatalf("Send = %v, want ErrNoConversation", err)
	}
}

func TestSendRejectsOverlappingTurns(t *testing.T) {
	conv := newBlockingConv()
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer func() {
		close(conv.release)
		h.Close()
	}()

	go func() { _ = h.Send(context.Background(), "first") }()
	conv.awaitStart(t)

	if err := h.Send(context.Background(), "second"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Send during a turn = %v, want ErrBusy", err)
	}
}

// The stale-completion guard. A turn that finishes after its conversation was
// replaced must not clear the running flag belonging to the conversation that
// replaced it — doing so would let a second turn start while the live one is
// still streaming.
func TestReplacedConversationCompletionDoesNotClearNewRunningState(t *testing.T) {
	old, fresh := newBlockingConv(), newBlockingConv()
	h := NewHost(builderFor(old, fresh))
	mustOpen(t, h)
	defer func() {
		close(fresh.release)
		h.Close()
	}()

	oldDone := make(chan struct{})
	go func() {
		_ = h.Send(context.Background(), "on the old conversation")
		close(oldDone)
	}()
	old.awaitStart(t)

	// The user switches projects while that turn is still in flight.
	mustOpen(t, h)

	freshDone := make(chan struct{})
	go func() {
		_ = h.Send(context.Background(), "on the fresh conversation")
		close(freshDone)
	}()
	fresh.awaitStart(t)

	// Now let the superseded turn land.
	close(old.release)
	select {
	case <-oldDone:
	case <-time.After(settle):
		t.Fatal("superseded turn never returned")
	}

	if !h.Running() {
		t.Fatal("a superseded turn's completion cleared the live conversation's running state")
	}
	if err := h.Send(context.Background(), "should be refused"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Send = %v, want ErrBusy while the live turn is streaming", err)
	}
}

func TestOpenClearsRunningStateForTheNewConversation(t *testing.T) {
	old, fresh := newBlockingConv(), newInstantConv()
	h := NewHost(builderFor(old, fresh))
	mustOpen(t, h)
	defer h.Close()

	go func() { _ = h.Send(context.Background(), "in flight") }()
	old.awaitStart(t)

	mustOpen(t, h)

	if h.Running() {
		t.Fatal("a freshly opened conversation should not inherit the old turn's running state")
	}
	if err := h.Send(context.Background(), "on the new conversation"); err != nil {
		t.Fatalf("new conversation refused a turn: %v", err)
	}
	close(old.release)
}

func TestCloseIsIdempotentAndClosesTheConversationOnce(t *testing.T) {
	conv := newInstantConv()
	h := NewHost(builderFor(conv))
	mustOpen(t, h)

	h.Close()
	h.Close()

	if got := conv.closeCount(); got != 1 {
		t.Fatalf("conversation closed %d times across two Host.Close calls, want 1", got)
	}
	if err := h.Send(context.Background(), "after close"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send after Close = %v, want ErrClosed", err)
	}
	if err := h.Open(context.Background(), Options{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Open after Close = %v, want ErrClosed", err)
	}
}

func TestTurnReportsZeroWithNothingOpen(t *testing.T) {
	h := NewHost(builderFor(newInstantConv()))

	if got := h.Turn(); got != 0 {
		t.Fatalf("Turn() = %d with nothing open, want 0", got)
	}
	mustOpen(t, h)
	defer h.Close()
	if got := h.Turn(); got != 7 {
		t.Fatalf("Turn() = %d, want the conversation's turn", got)
	}
}

func TestSendPropagatesTurnError(t *testing.T) {
	conv := newInstantConv()
	conv.runErr = errors.New("model refused")
	h := NewHost(builderFor(conv))
	mustOpen(t, h)
	defer h.Close()

	if err := h.Send(context.Background(), "boom"); err == nil {
		t.Fatal("Send swallowed the turn error")
	}
	// The failed turn must still release the host.
	if h.Running() {
		t.Fatal("a failed turn left the host marked running")
	}
}
