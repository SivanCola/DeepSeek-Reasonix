package control

import (
	"errors"
	"strings"
	"testing"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/event"
	"reasonix/internal/state/sessiontemp"
)

func scratchBlockOf(composed string) string {
	open, close := "<"+sessionScratchTag+">", "</"+sessionScratchTag+">"
	i := strings.Index(composed, open)
	if i < 0 {
		return ""
	}
	j := strings.Index(composed[i:], close)
	if j < 0 {
		return composed[i:]
	}
	return composed[i : i+j+len(close)]
}

// The model is told the one spelling bash and the file tools both resolve. The
// host path is never projected: under bwrap bash sees the directory at /tmp, and
// on Windows a backslashed literal does not survive a bash command line.
func TestScratchBlockProjectsTheSpellingNotTheHostPath(t *testing.T) {
	m := sessiontemp.NewWithRoot(testenv.TempDir(t))
	c := New(Options{SessionTemp: m, Sink: event.Discard})
	defer c.Close()
	var visible projectionHistory
	c.bindProjectionSeer(visible.newest, visible.sees)

	firstTurn := c.Compose("turn-one")
	first := scratchBlockOf(firstTurn)
	dir := m.Dir()
	if dir == "" {
		t.Fatal("projecting the scratch block did not create the session's directory")
	}
	if !strings.Contains(first, "`$TMPDIR/<name>`") {
		t.Fatalf("scratch block does not give the $TMPDIR spelling:\n%s", first)
	}
	if strings.Contains(first, dir) || strings.Contains(first, sessiontemp.Prefix()) {
		t.Fatalf("scratch block projected the host path %q:\n%s", dir, first)
	}
	visible.deliver(firstTurn)

	if again := scratchBlockOf(c.Compose("turn-two")); again != "" {
		t.Fatalf("an unchanged block was projected twice:\n%s", again)
	}

	m.Rotate()
	if rotated := scratchBlockOf(c.Compose("turn-three")); rotated != "" {
		t.Fatalf("a rotation re-sent bytes the model already holds:\n%s", rotated)
	}
}

// Composing alone settles nothing: a turn a hook blocks put the block nowhere.
func TestScratchBlockIsOwedUntilATurnCarriesIt(t *testing.T) {
	m := sessiontemp.NewWithRoot(testenv.TempDir(t))
	c := New(Options{SessionTemp: m, Sink: event.Discard})
	defer c.Close()
	var visible projectionHistory
	c.bindProjectionSeer(visible.newest, visible.sees)

	if scratchBlockOf(c.Compose("blocked")) == "" {
		t.Fatal("first turn carried no scratch block")
	}
	retry := c.Compose("retry")
	if scratchBlockOf(retry) == "" {
		t.Fatal("an unsettled scratch block was dropped from the next turn")
	}
	visible.deliver(retry)
	visible.forget() // a fold took the delivered turn out of view
	if scratchBlockOf(c.Compose("after-fold")) == "" {
		t.Fatal("a folded-away delivery was not re-owed")
	}
}

// A directory the model was told about and that can no longer be had — the
// next generation failed to create — is retracted once, rather than left
// reading as current.
func TestScratchBlockRetractsADirectoryThatNoLongerHolds(t *testing.T) {
	m := sessiontemp.NewWithRoot(testenv.TempDir(t))
	c := New(Options{SessionTemp: m, Sink: event.Discard})
	defer c.Close()
	var visible projectionHistory
	c.bindProjectionSeer(visible.newest, visible.sees)

	one := c.Compose("one")
	if scratchBlockOf(one) == "" {
		t.Fatal("first turn carried no scratch block")
	}
	visible.deliver(one)
	m.SetMkDirForTest(func(string) (string, error) { return "", errors.New("disk full") })
	m.Rotate()
	two := c.Compose("two")
	retract := scratchBlockOf(two)
	if retract == "" || strings.Contains(retract, "`$TMPDIR/<name>`") {
		t.Fatalf("a directory that no longer holds was not retracted:\n%s", retract)
	}
	visible.deliver(two)
	if again := scratchBlockOf(c.Compose("three")); again != "" {
		t.Fatalf("the retraction was repeated:\n%s", again)
	}
}

// Only the manager the assembly bound the tools to is announced; a controller
// that made its own has no tool writing there.
func TestScratchBlockNeedsTheAssemblysManager(t *testing.T) {
	c := New(Options{Sink: event.Discard})
	defer c.Close()
	if got := scratchBlockOf(c.Compose("one")); got != "" {
		t.Fatalf("a controller without an assembly-supplied manager announced one:\n%s", got)
	}
}

// A rebuild carries the conversation verbatim, so a block it already holds is
// not sent again, whichever manager the replacement is bound to.
func TestScratchDeliverySurvivesARebuild(t *testing.T) {
	m := sessiontemp.NewWithRoot(testenv.TempDir(t))
	old := New(Options{SessionTemp: m, Sink: event.Discard})
	defer old.Close()
	var visible projectionHistory
	old.bindProjectionSeer(visible.newest, visible.sees)
	one := old.Compose("one")
	if scratchBlockOf(one) == "" {
		t.Fatal("first turn carried no scratch block")
	}
	visible.deliver(one)

	for _, temp := range []*sessiontemp.Manager{m, sessiontemp.NewWithRoot(testenv.TempDir(t))} {
		next := New(Options{SessionTemp: temp, Sink: event.Discard})
		next.bindProjectionSeer(visible.newest, visible.sees)
		if got := scratchBlockOf(next.Compose("two")); got != "" {
			t.Fatalf("a rebuild re-sent the block:\n%s", got)
		}
		next.Close()
	}
}
