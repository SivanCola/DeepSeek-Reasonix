package boot

// The goal contract has no expiry: the model keeps reading it after the goal
// stops unless the host retires it. These arms read the bytes at the provider
// boundary after a pause, a clear, and a rewind that removed the retraction.

import (
	"strings"
	"testing"

	"reasonix/internal/session/control"
)

const goalTombstoneOpen = `<active-goal contract="stopped" reason="`

// The two stops a user performs while looking at a running goal.
func TestProjectionAStoppedGoalRetractsItsContract(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		stop   func(*testing.T, *control.Controller)
	}{
		{"cleared", "cleared", func(_ *testing.T, c *control.Controller) { c.ClearGoal() }},
		{"paused", "paused", func(t *testing.T, c *control.Controller) {
			if !c.PauseGoal() {
				t.Fatal("PauseGoal refused a running goal")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProjectionHarness(t, "ctxproj-goal-"+tc.name, "", "")
			if got := blockOf(projectionOf(t, h.turn("turn-alpha"), "turn-alpha"), "active-goal"); got != "" {
				t.Fatalf("a goal block was sent before any goal was set:\n%s", got)
			}
			h.ctrl.SetGoal("ship the parser")
			running := blockOf(projectionOf(t, h.turn("turn-beta"), "turn-beta"), "active-goal")
			if !strings.HasPrefix(running, `<active-goal contract="full">`) {
				t.Fatalf("the running goal never carried the full contract, so this arm measures nothing:\n%s", running)
			}
			if tc.name == "cleared" {
				h.ctrl.ClearGoal()
			} else {
				tc.stop(t, h.ctrl)
			}

			got := blockOf(projectionOf(t, h.turn("turn-gamma"), "turn-gamma"), "active-goal")
			want := goalTombstoneOpen + tc.reason + `">`
			if !strings.HasPrefix(got, want) {
				t.Fatalf("the goal is %s and the next request does not retire the contract, so the model still reads %s:\ngot %q",
					tc.name, `"pursue this goal autonomously"`, got)
			}
			if !strings.Contains(got, "Anything the goal contract told you earlier no longer applies") {
				t.Fatalf("the tombstone does not say the earlier contract no longer applies:\n%s", got)
			}
			if !strings.Contains(got, "wait for the user") {
				t.Fatalf("the tombstone does not say to wait for the user:\n%s", got)
			}
		})
	}
}

// A running goal must not lose its contract, and its reminder must not repeat
// the whole contract.
func TestProjectionARunningGoalKeepsItsContractWithoutATombstone(t *testing.T) {
	h := newProjectionHarness(t, "ctxproj-goal-running", "", "")
	h.ctrl.SetGoal("ship the parser")

	first := projectionOf(t, h.turn("turn-alpha"), "turn-alpha")
	if strings.Count(first, `<active-goal contract="full">`) != 1 {
		t.Fatalf("the running goal did not carry exactly one full contract:\n%s", first)
	}

	second := projectionOf(t, h.turn("turn-beta"), "turn-beta")
	if strings.Contains(second, goalTombstoneOpen) {
		t.Fatalf("a running goal was retracted:\n%s", second)
	}
	if strings.Contains(second, `<active-goal contract="full">`) {
		t.Fatalf("the full contract was restated while the model still holds it:\n%s", second)
	}
	if !strings.Contains(second, "<active-goal>") {
		t.Fatalf("the running goal lost its reminder entirely:\n%s", second)
	}
}

// The tombstone replaces the contract: after it is read, it is the newest
// active-goal block and nothing is owed again.
func TestProjectionTheGoalTombstoneIsDeliveredOnce(t *testing.T) {
	h := newProjectionHarness(t, "ctxproj-goal-once", "", "")
	h.ctrl.SetGoal("ship the parser")
	h.turn("turn-alpha")
	h.ctrl.ClearGoal()

	first := blockOf(projectionOf(t, h.turn("turn-beta"), "turn-beta"), "active-goal")
	if !strings.HasPrefix(first, goalTombstoneOpen+"cleared"+`">`) {
		t.Fatalf("the first request after the clear did not carry the tombstone, so this arm measures nothing:\n%s", first)
	}
	if again := projectionOf(t, h.turn("turn-gamma"), "turn-gamma"); strings.Contains(again, "<active-goal") {
		t.Fatalf("the tombstone is the newest active-goal block and was sent again:\n%s", again)
	}
}

// The retraction is owed by what the model can see, so a rewind that removes the
// turn carrying it brings back the full contract with nothing to retire it.
func TestProjectionARewindThatDropsTheTombstoneOwesItAgain(t *testing.T) {
	h := newProjectionHarness(t, "ctxproj-goal-rewind", "", "")
	h.ctrl.EnsureSessionPath()
	h.ctrl.SetGoal("ship the parser")
	h.turn("turn-alpha")
	h.ctrl.ClearGoal()
	h.turn("turn-beta")
	if again := projectionOf(t, h.turn("turn-gamma"), "turn-gamma"); strings.Contains(again, "<active-goal") {
		t.Fatalf("the tombstone was repeated, so this arm measures nothing:\n%s", again)
	}

	// Keep the turn that carried the contract, drop the one that retired it.
	if err := h.ctrl.Rewind(1, control.RewindConversation); err != nil {
		t.Fatalf("Rewind(1): %v", err)
	}

	got := blockOf(projectionOf(t, h.turn("turn-delta"), "turn-delta"), "active-goal")
	if !strings.HasPrefix(got, goalTombstoneOpen+"cleared"+`">`) {
		t.Fatalf("the rewind removed the only tombstone, the full contract is back, and the next request does not re-owe it:\n%s", got)
	}
}
