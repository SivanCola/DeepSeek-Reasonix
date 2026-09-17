package providerprobe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/netclient"
	"reasonix/internal/provider"
)

const Timeout = 20 * time.Second

// TestConnection performs a request-local, tool-free chat probe. The supplied
// credential is frozen into a copy of entry and is never persisted or exported
// to the process environment.
func TestConnection(ctx context.Context, entry config.ProviderEntry, key string, proxy netclient.ProxySpec) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	if strings.TrimSpace(key) != "" {
		entry = entry.WithAPIKeyForProbe(key)
	}
	client, err := boot.NewProviderWithProxy(&entry, proxy)
	if err != nil {
		return err
	}
	chunks, err := client.Stream(ctx, provider.Request{
		Messages:  []provider.Message{{Role: provider.RoleUser, Content: "Reply with OK."}},
		MaxTokens: 16,
	})
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, open := <-chunks:
			if !open {
				return fmt.Errorf("provider closed the connection without a response")
			}
			if chunk.Err != nil {
				return chunk.Err
			}
			if chunk.Type == provider.ChunkText && strings.TrimSpace(chunk.Text) != "" {
				return nil
			}
			if chunk.Type == provider.ChunkDone {
				return nil
			}
		}
	}
}
