package boot

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"reasonix/internal/contract/agentcomposition"
	"reasonix/internal/ext/plugin"
)

func TestCachedResourceReadWaitsForStartupWithoutChangingSchemas(t *testing.T) {
	gate := make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	s := buildResourceSession(t, true, true, false, "", func(server *resourceMCPServer) {
		t.Cleanup(release)
		server.mu.Lock()
		server.initializeGate = gate
		server.mu.Unlock()
	})
	target, ok := s.registry.Get(plugin.ResourceReadToolName)
	if !ok {
		t.Fatal("cached resource capability was not registered")
	}
	before := agentcomposition.Digest(s.registry.Schemas())
	args := json.RawMessage(`{"server":"res","uri":"doc://handbook"}`)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := target.Execute(ctx, args); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read did not wait for the held startup: %v", err)
	}
	if len(s.server.readURIs()) != 0 {
		t.Fatal("resource read crossed an incomplete handshake")
	}
	release()
	out, err := target.Execute(t.Context(), args)
	if err != nil || out != resourceBody {
		t.Fatalf("read after startup = %q, %v", out, err)
	}
	if agentcomposition.Digest(s.registry.Schemas()) != before {
		t.Fatal("resource connection changed provider-visible schemas")
	}
}
