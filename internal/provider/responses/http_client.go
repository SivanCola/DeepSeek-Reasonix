package responses

import (
	"fmt"
	"net/http"
	"time"

	"reasonix/internal/netclient"
)

func newHTTPClient(cfg Config) (*http.Client, error) {
	// Injected credential tunnels own their transport and proxy policy.
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient, nil
	}
	client, err := netclient.NewHTTPClient(cfg.Proxy, netclient.TransportOptions{
		HTTP1Only:   cfg.HTTP1Only,
		DialTimeout: 30 * time.Second, KeepAlive: 30 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 300 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("responses: network: %w", err)
	}
	return client, nil
}
