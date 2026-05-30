package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// CheckResult is a single connectivity check result.
type CheckResult struct {
	Name    string
	Status  string // "ok", "warn", "error"
	Message string
	Latency time.Duration
}

// runConnectivityCheck probes a provider's base URL for reachability.
func runConnectivityCheck(ctx context.Context, baseURL, apiKey string) CheckResult {
	start := time.Now()
	client := &http.Client{Timeout: 10 * time.Second}

	endpoint := baseURL + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return CheckResult{Name: baseURL, Status: "error", Message: fmt.Sprintf("invalid URL: %v", err)}
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return CheckResult{Name: baseURL, Status: "error", Message: fmt.Sprintf("unreachable: %v", err), Latency: latency}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return CheckResult{Name: baseURL, Status: "warn", Message: fmt.Sprintf("auth failed (HTTP %d)", resp.StatusCode), Latency: latency}
	case resp.StatusCode >= 500:
		return CheckResult{Name: baseURL, Status: "error", Message: fmt.Sprintf("server error (HTTP %d)", resp.StatusCode), Latency: latency}
	default:
		return CheckResult{Name: baseURL, Status: "ok", Message: fmt.Sprintf("reachable (HTTP %d)", resp.StatusCode), Latency: latency}
	}
}
