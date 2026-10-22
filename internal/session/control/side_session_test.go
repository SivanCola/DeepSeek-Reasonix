package control

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/config"
	"reasonix/internal/contract/event"
)

type fakeSide struct {
	mu      sync.Mutex
	prompts []string
	hold    chan struct{}
	started chan struct{}
	events  []event.Event
	err     error
}

func (f *fakeSide) Ask(ctx context.Context, prompt string, parent event.Sink) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	for _, e := range f.events {
		parent.Emit(e)
	}
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "answer", f.err
}

func sideController(t *testing.T, convs ...*fakeSide) *Controller {
	t.Helper()
	i := 0
	c := New(Options{Runner: &fakeTurnRunner{}, SideFactory: func() (SideConversation, error) {
		conv := convs[i%len(convs)]
		i++
		return conv, nil
	}})
	t.Cleanup(c.Close)
	return c
}

func TestSideAnswersWithoutTouchingTheMainSession(t *testing.T) {
	side := &fakeSide{}
	c := sideController(t, side)
	if err := c.Run(context.Background(), "main question"); err != nil {
		t.Fatal(err)
	}
	before := len(c.History())

	id, err := c.OpenSide()
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.AskSide(context.Background(), id, "what does this do?", []string{"func f() {}", "  "}, nil)
	if err != nil || got != "answer" {
		t.Fatalf("ask = %q %v", got, err)
	}
	if len(c.History()) != before {
		t.Fatalf("the main transcript grew from %d to %d messages", before, len(c.History()))
	}
	want := "Quoted from the main conversation (passage 1):\n<<<\nfunc f() {}\n>>>\n\nwhat does this do?"
	if len(side.prompts) != 1 || side.prompts[0] != want {
		t.Fatalf("the side was asked %q, want %q", side.prompts, want)
	}
}

func TestSideQuotesAreCappedAndEmptyQuestionsRefused(t *testing.T) {
	side := &fakeSide{}
	c := sideController(t, side)
	id, _ := c.OpenSide()
	if _, err := c.AskSide(context.Background(), id, "   ", nil, nil); !errors.Is(err, ErrSideEmpty) {
		t.Fatalf("an empty question = %v", err)
	}
	huge := strings.Repeat("é", maxSideQuoteRunes*2)
	many := make([]string, maxSideQuotes+4)
	for i := range many {
		many[i] = "x"
	}
	if _, err := c.AskSide(context.Background(), id, "q", append([]string{huge}, many...), nil); err != nil {
		t.Fatal(err)
	}
	p := side.prompts[0]
	if strings.Count(p, "Quoted from the main conversation") != maxSideQuotes || !strings.Contains(p, "[…cut]") || len([]rune(p)) > maxSideQuoteRunes+2000 {
		t.Fatalf("quotes were not capped: %d runes, %d passages", len([]rune(p)), strings.Count(p, "Quoted from"))
	}
}

func TestSideLimitsBusyAndUnknown(t *testing.T) {
	slow := &fakeSide{hold: make(chan struct{}), started: make(chan struct{}, 1)}
	c := sideController(t, slow)
	setSideLimit(t, 3)
	first, err := c.OpenSide()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 3; i++ {
		if _, err := c.OpenSide(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.OpenSide(); !errors.Is(err, ErrSideLimit) {
		t.Fatalf("one too many = %v", err)
	}

	done := make(chan error, 1)
	go func() { _, err := c.AskSide(context.Background(), first, "q", nil, nil); done <- err }()
	<-slow.started
	if _, err := c.AskSide(context.Background(), first, "again", nil, nil); !errors.Is(err, ErrSideBusy) {
		t.Fatalf("a second question while answering = %v", err)
	}
	close(slow.hold)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	c.CloseSide(first)
	if _, err := c.AskSide(context.Background(), first, "q", nil, nil); !errors.Is(err, ErrSideUnknown) {
		t.Fatalf("a closed one = %v", err)
	}
	if _, err := c.OpenSide(); err != nil {
		t.Fatalf("closing frees a place: %v", err)
	}
}

func TestClosingASideStopsItsRunningQuestion(t *testing.T) {
	slow := &fakeSide{hold: make(chan struct{}), started: make(chan struct{}, 1)}
	c := sideController(t, slow)
	id, _ := c.OpenSide()
	done := make(chan error, 1)
	go func() { _, err := c.AskSide(context.Background(), id, "q", nil, nil); done <- err }()
	<-slow.started
	c.CloseSide(id)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a closed side's question ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing did not stop the question")
	}
}

func TestClosingTheControllerClosesEverySideAndRefusesNewOnes(t *testing.T) {
	slow := &fakeSide{hold: make(chan struct{}), started: make(chan struct{}, 1)}
	c := New(Options{Runner: &fakeTurnRunner{}, SideFactory: func() (SideConversation, error) { return slow, nil }})
	id, _ := c.OpenSide()
	done := make(chan error, 1)
	go func() { _, err := c.AskSide(context.Background(), id, "q", nil, nil); done <- err }()
	<-slow.started
	c.Close()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("question = %v", err)
	}
	if _, err := c.OpenSide(); !errors.Is(err, ErrSideUnavailable) {
		t.Fatalf("a side opened on a closed controller: %v", err)
	}
}

func TestNoSideWithoutAFactory(t *testing.T) {
	c := New(Options{Runner: &fakeTurnRunner{}})
	defer c.Close()
	if _, err := c.OpenSide(); !errors.Is(err, ErrSideUnavailable) {
		t.Fatalf("OpenSide = %v", err)
	}
}

func TestAQuestionBegunAfterTheSideWasClosedIsCancelled(t *testing.T) {
	slow := &fakeSide{hold: make(chan struct{})}
	c := sideController(t, slow)
	id, _ := c.OpenSide()
	entry, _ := c.side.get(id)
	c.CloseSide(id)
	entry.stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entry.setRunning(cancel)
	if ctx.Err() == nil {
		t.Fatal("a closed side let a new question run")
	}
}

func setSideLimit(t *testing.T, n int) {
	t.Helper()
	t.Setenv("REASONIX_HOME", testenv.TempDir(t))
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Agent.MaxSideSessions = n
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
}

// Nothing limits side conversations until the user's own file says so.
func TestSideConversationsAreUnlimitedByDefault(t *testing.T) {
	t.Setenv("REASONIX_HOME", testenv.TempDir(t))
	c := sideController(t, &fakeSide{})
	for i := range 25 {
		if _, err := c.OpenSide(); err != nil {
			t.Fatalf("side conversation %d refused: %v", i+1, err)
		}
	}
}

func TestSideRoutesUsageAndProgressToSeparateSinks(t *testing.T) {
	side := &fakeSide{events: []event.Event{
		{Kind: event.Text, Text: "private answer"},
		{Kind: event.Reasoning, Text: "private reasoning"},
		{Kind: event.ToolDispatch, Tool: event.Tool{Name: "read_file"}},
		{Kind: event.Usage, UsageSource: event.UsageSourceExecutor},
	}}
	var parent, progress []event.Event
	c := New(Options{
		Runner:      &fakeTurnRunner{},
		Sink:        event.FuncSink(func(e event.Event) { parent = append(parent, e) }),
		SideFactory: func() (SideConversation, error) { return side, nil },
	})
	defer c.Close()
	id, err := c.OpenSide()
	if err != nil {
		t.Fatal(err)
	}
	before := len(parent)
	_, err = c.AskSide(context.Background(), id, "q", nil, event.FuncSink(func(e event.Event) {
		progress = append(progress, e)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := parent[before:]; len(got) != 1 || got[0].Kind != event.Usage || got[0].UsageSource != event.UsageSourceSide {
		t.Fatalf("parent received side events: %+v", got)
	}
	if len(progress) != 3 {
		t.Fatalf("progress = %+v", progress)
	}
	for _, e := range progress {
		if e.Kind == event.Usage {
			t.Fatal("billing leaked to progress")
		}
	}
	before = len(parent)
	if _, err := c.AskSide(context.Background(), id, "again", nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(parent) != before+1 || parent[before].Kind != event.Usage {
		t.Fatalf("nil progress leaked events: %+v", parent[before:])
	}
}
