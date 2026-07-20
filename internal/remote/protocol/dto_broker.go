package protocol

import (
	"encoding/json"
	"strings"
)

// Provider Broker methods run Host → Desktop for requests and Desktop → Host for
// stream chunks. API keys never leave Desktop; catalog entries are non-secret.

// BrokerCatalogParams is Host → Desktop: list authorized non-secret descriptors.
type BrokerCatalogParams struct {
	// AllowedRefs is optional filter; empty means all refs authorized for this scope.
	AllowedRefs []string `json:"allowedRefs,omitempty"`
}

// BrokerProviderDescriptor is a non-secret catalog entry for remote selection.
// Never includes API keys, base URLs, headers, or env names.
type BrokerProviderDescriptor struct {
	Ref                            string   `json:"ref" validate:"nonempty"`
	DisplayName                    string   `json:"displayName,omitempty"`
	Model                          string   `json:"model,omitempty"`
	SupportsVision                 bool     `json:"supportsVision,omitempty"`
	SupportedEfforts               []string `json:"supportedEfforts,omitempty"`
	DefaultEffort                  string   `json:"defaultEffort,omitempty"`
	ToolCallReasoning              bool     `json:"toolCallReasoning,omitempty"`
	WarnOnMissingToolCallReasoning bool     `json:"warnOnMissingToolCallReasoning,omitempty"`
}

// BrokerCatalogResult is the authorized catalog snapshot.
type BrokerCatalogResult struct {
	Providers []BrokerProviderDescriptor `json:"providers"`
}

// BrokerStreamOpenParams opens a provider stream on Desktop for one Host turn.
// Request is the structured provider.Request JSON (messages/tools only; no secrets).
type BrokerStreamOpenParams struct {
	StreamID    string `json:"streamId" validate:"nonempty"`
	ProviderRef string `json:"providerRef" validate:"nonempty"`
	// Request is opaque JSON matching reasonix/internal/provider.Request.
	Request json.RawMessage `json:"request"`
	// Effort is optional override string already resolved into Request when set.
	Effort string `json:"effort,omitempty"`
}

func (p BrokerStreamOpenParams) Validate() error {
	if strings.TrimSpace(p.StreamID) == "" || strings.TrimSpace(p.ProviderRef) == "" {
		return validationError("streamId and providerRef are required")
	}
	if len(p.Request) == 0 || string(p.Request) == "null" {
		return validationError("request body is required")
	}
	return nil
}

// BrokerStreamOpenResult acknowledges the stream; chunks arrive as notifications.
type BrokerStreamOpenResult struct {
	Accepted bool `json:"accepted"`
}

// BrokerStreamCancelParams cancels one in-flight Desktop provider stream.
type BrokerStreamCancelParams struct {
	StreamID string `json:"streamId" validate:"nonempty"`
}

// BrokerStreamCancelResult acknowledges cancel.
type BrokerStreamCancelResult struct {
	Cancelled bool `json:"cancelled"`
}

// BrokerStreamChunkParams is one NDJSON-equivalent provider chunk Desktop → Host.
type BrokerStreamChunkParams struct {
	StreamID string          `json:"streamId" validate:"nonempty"`
	Seq      int64           `json:"seq" validate:"min=1"`
	Chunk    json.RawMessage `json:"chunk"`
}

func (p BrokerStreamChunkParams) Validate() error {
	if strings.TrimSpace(p.StreamID) == "" {
		return validationError("streamId is required")
	}
	if p.Seq < 1 {
		return validationError("seq must be >= 1")
	}
	if len(p.Chunk) == 0 || string(p.Chunk) == "null" {
		return validationError("chunk is required")
	}
	return nil
}

// BrokerStreamEndParams ends a stream (success or error).
type BrokerStreamEndParams struct {
	StreamID string `json:"streamId" validate:"nonempty"`
	// Error is a redacted, non-secret failure message when the stream failed.
	Error string `json:"error,omitempty"`
	// Interrupted is true when the Desktop↔Host connection dropped mid-stream.
	Interrupted bool `json:"interrupted,omitempty"`
}

// BrokerCatalogChangedParams notifies Host that Desktop catalog or authorization changed.
type BrokerCatalogChangedParams struct {
	// Generation is a Desktop-local monotonic counter for drop-stale logic.
	Generation int64 `json:"generation" validate:"min=1"`
}
