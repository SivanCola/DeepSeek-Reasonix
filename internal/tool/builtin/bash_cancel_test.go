package builtin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

// TestBashCancelReturnsPromptly proves a cancelled bash run stops fast instead of
// blocking for the command's natural duration — the process-tree kill path.
func TestBashCancelReturnsPromptly(t *testing.T) {
	bt, ok := tool.LookupBuiltin("bash")
	if !ok {
		t.Fatal("bash not registered")
	}
	cmd := "sleep 30"
	if sandbox.ResolveShell().Kind == sandbox.ShellPowerShell {
		cmd = "Start-Sleep -Seconds 30"
	}
	args, _ := json.Marshal(map[string]any{"command": cmd})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()

	start := time.Now()
	_, err := bt.Execute(ctx, args)
	elapsed := time.Since(start)

	// Must have run until the cancel (≥ ~300ms) — not failed instantly — yet stop
	// well before the 30s natural duration.
	if elapsed < 250*time.Millisecond {
		t.Fatalf("command exited too fast (%v) — it didn't actually run; err=%v", elapsed, err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("cancel did not interrupt bash: took %v (want < 10s vs 30s natural)", elapsed)
	}
	if err == nil {
		t.Error("expected an error after cancel, got nil")
	}
	t.Logf("cancelled bash (%q) returned in %v (err=%v)", cmd, elapsed, err)
}
