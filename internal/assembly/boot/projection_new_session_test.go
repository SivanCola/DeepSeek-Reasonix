package boot

// A /new session's first request carries the standing blocks: the skills listing,
// the project instructions and the date, because the new history contains none of them.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/contract/provider"
)

func newSessionProjectionHarness(t *testing.T, kind string) *projectionHarness {
	t.Helper()
	home := isolateConfigHome(t)
	t.Setenv("REASONIX_HOME", filepath.Join(home, ".reasonix"))
	t.Setenv("REASONIX_STATE_HOME", "")
	dir := robustTempDir(t)
	t.Chdir(dir)
	rec := &effectRecordingProvider{}
	provider.Register(kind, func(provider.Config) (provider.Provider, error) { return rec, nil })
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"

[[providers]]
name = "test-model"
kind = "`+kind+`"
model = "x"
`)
	approveWorkspace(t, dir)
	h := &projectionHarness{t: t, dir: dir, kind: kind, rec: rec, events: &projectionEventLog{}}
	ctrl, err := Build(context.Background(), Options{
		Sink: h.events, Home: os.Getenv("REASONIX_HOME"), WorkspaceRoot: dir,
		SessionDir: filepath.Join(dir, "sessions"), resolvedShell: pinnedEffectShell(), clock: h.clock,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// The frontends call this after Build, before the first turn.
	ctrl.EnsureSessionPath()
	h.ctrl = ctrl
	t.Cleanup(func() {
		if h.ctrl != nil {
			h.ctrl.Close()
		}
	})
	return h
}

func TestProjectionIsStillOwedAndDeliveredInANewSession(t *testing.T) {
	h := newSessionProjectionHarness(t, "ctxproj-new-session")
	first := h.turn("turn-alpha")
	if !strings.Contains(projectionOf(t, first, "turn-alpha"), "<available-skills>") {
		t.Fatalf("the first session's request carried no listing, so this arm measures nothing")
	}
	if err := h.ctrl.NewSession(); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := h.ctrl.Run(context.Background(), "turn-beta"); err != nil {
		t.Fatalf("turn after /new: %v", err)
	}
	reqs := h.rec.requests()
	got := projectionOf(t, reqs[len(reqs)-1], "turn-beta")
	if !strings.Contains(got, "<available-skills>") {
		t.Fatalf("the new session's first request carries no listing — the model is never told the skills:\n%s", got)
	}
}
