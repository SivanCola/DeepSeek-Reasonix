package main

import (
	"fmt"
	"strings"

	"reasonix/internal/config"
)

// This narrow edit goes through ApplyModelSettings's compare-and-save and
// existing runtime rebuild owner. It never starts or replays a model request.
func setProviderHTTP1OnlyConfig(c *config.Config, name string, enabled *bool) error {
	if enabled == nil {
		return fmt.Errorf("enabled is required")
	}
	entry, ok := c.Provider(strings.TrimSpace(name))
	if !ok || strings.TrimSpace(name) == "" {
		return fmt.Errorf("provider connection no longer exists; reopen model settings")
	}
	switch entry.Kind {
	case "openai", "anthropic", "responses", "dashscope-responses":
		entry.HTTP1Only = *enabled
		return nil
	default:
		return fmt.Errorf("provider does not support HTTP connection compatibility settings")
	}
}
