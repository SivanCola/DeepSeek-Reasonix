package control

import (
	"context"
	"errors"
	"strings"
	"time"

	"reasonix/internal/contract/config"
	"reasonix/internal/model/providerhealth"
)

type ProviderHealthReading = providerhealth.Reading

var ErrProviderHealthNotFound = errors.New("provider health: saved provider not found")

// ProviderHealth checks only saved endpoints, with no completion or configuration writes.
func (c *Controller) ProviderHealth(ctx context.Context, name string) (ProviderHealthReading, error) {
	cfg, err := config.Load()
	if err != nil {
		return ProviderHealthReading{}, err
	}
	p, ok := cfg.Provider(strings.TrimSpace(name))
	if !ok {
		return ProviderHealthReading{}, ErrProviderHealthNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	return c.providerHealth.Read(ctx, p, cfg.NetworkProxySpec()), nil
}
