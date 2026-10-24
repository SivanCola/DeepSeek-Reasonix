package boot

// A model switch must restate the standing blocks to the new model once: the
// conversation it adopts was composed for the model being replaced.

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
)

func TestProjectionModelSwitchReOwesTheStandingBlocksOnce(t *testing.T) {
	t.Parallel()
	home := statedBootHome(t)
	dir := testenv.TempDir(t)
	rec := &effectRecordingProvider{}
	provider.Register("boot-projection-switch", func(provider.Config) (provider.Provider, error) { return rec, nil })
	writeFile(t, dir, ".reasonix/skills/ledger-audit/SKILL.md", `---
name: ledger-audit
description: Audits the evidence ledger for receipts nothing reads.
---

Read the ledger and report.
`)
	writeFile(t, dir, "reasonix.toml", `
default_model = "model-a"

[agent]
system_prompt = "BASE"

[[providers]]
name = "model-a"
kind = "boot-projection-switch"
model = "a"

[[providers]]
name = "model-b"
kind = "boot-projection-switch"
model = "b"
`)
	approveWorkspaceAt(t, home, dir)

	from, err := Build(context.Background(), Options{Home: home, WorkspaceRoot: dir, Sink: event.Discard})
	if err != nil {
		t.Fatalf("Build model-a: %v", err)
	}
	defer from.Close()
	from.EnsureSessionPath()
	if err := from.Run(context.Background(), "turn-first"); err != nil {
		t.Fatalf("Run(turn-first): %v", err)
	}
	first := rec.requests()[0]
	if !strings.Contains(projectionOf(t, first, "turn-first"), "<available-skills>") {
		t.Fatalf("the first model's request carried no listing, so this test measures nothing:\n%s", projectionOf(t, first, "turn-first"))
	}
	if err := from.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	to, err := Build(context.Background(), Options{Home: home, WorkspaceRoot: dir, Model: "model-b", Sink: event.Discard})
	if err != nil {
		t.Fatalf("Build model-b: %v", err)
	}
	defer to.Close()
	if err := ApplyRuntimeMigration(to, from, CaptureRuntimeMigration(from)); err != nil {
		t.Fatalf("ApplyRuntimeMigration: %v", err)
	}
	// The adopted conversation already holds the listing the old model was sent;
	// that copy is not a delivery to this model.
	before := len(rec.requests())
	if err := to.Run(context.Background(), "turn-switched"); err != nil {
		t.Fatalf("Run(turn-switched): %v", err)
	}
	switched := rec.requests()[before:]
	if len(switched) != 1 {
		t.Fatalf("switch request count = %d, want 1", len(switched))
	}
	if got := projectionOf(t, switched[0], "turn-switched"); !strings.Contains(got, "<available-skills>") {
		t.Fatalf("the switched model's first request carries no listing — it inherits another model's delivery:\n%s", got)
	}

	// One restatement, not a repeat on every turn: this runtime has now put the
	// listing in front of its model, and the visible history says so.
	before = len(rec.requests())
	if err := to.Run(context.Background(), "turn-second"); err != nil {
		t.Fatalf("Run(turn-second): %v", err)
	}
	second := rec.requests()[before:]
	if got := projectionOf(t, second[len(second)-1], "turn-second"); strings.Contains(got, "<available-skills>") {
		t.Fatalf("the restatement repeated on the next turn:\n%s", got)
	}

	// A rebuild that keeps the model carries the conversation verbatim: the copy
	// the history already holds is delivered, and no arm may re-send it.
	same, err := Build(context.Background(), Options{Home: home, WorkspaceRoot: dir, Model: "model-b", Sink: event.Discard})
	if err != nil {
		t.Fatalf("Build model-b again: %v", err)
	}
	defer same.Close()
	if err := ApplyRuntimeMigration(same, to, CaptureRuntimeMigration(to)); err != nil {
		t.Fatalf("ApplyRuntimeMigration (same model): %v", err)
	}
	before = len(rec.requests())
	if err := same.Run(context.Background(), "turn-rebuilt"); err != nil {
		t.Fatalf("Run(turn-rebuilt): %v", err)
	}
	rebuilt := rec.requests()[before:]
	if got := projectionOf(t, rebuilt[len(rebuilt)-1], "turn-rebuilt"); strings.Contains(got, "<available-skills>") {
		t.Fatalf("a rebuild on the same model re-sent a listing the conversation already carries:\n%s", got)
	}
}
