package provider

import (
	"fmt"
	"strings"
)

// Descriptor is a non-sensitive description of a configured provider/model that
// may be advertised to a remote runtime. It must never include API keys, base
// URLs, chat URLs, custom auth headers, proxy addresses, or .env variable names.
type Descriptor struct {
	// Ref is the stable selection key, typically "name/model" or a configured
	// provider name that Resolve understands.
	Ref              string   `json:"ref"`
	DisplayName      string   `json:"displayName,omitempty"`
	Model            string   `json:"model,omitempty"`
	ContextWindow    int      `json:"contextWindow,omitempty"`
	PricingCurrency  string   `json:"pricingCurrency,omitempty"`
	InputPerMillion  float64  `json:"inputPerMillion,omitempty"`
	OutputPerMillion float64  `json:"outputPerMillion,omitempty"`
	Vision           bool     `json:"vision,omitempty"`
	Tools            bool     `json:"tools,omitempty"`
	Reasoning        bool     `json:"reasoning,omitempty"`
	Efforts          []string `json:"efforts,omitempty"`
	DefaultEffort    string   `json:"defaultEffort,omitempty"`
	// ToolCallReasoning is true when the provider requires reasoning_content
	// on assistant tool_calls turns (DeepSeek thinking mode).
	ToolCallReasoning bool `json:"toolCallReasoning,omitempty"`
}

// Selection chooses a provider from a Resolver catalog.
type Selection struct {
	Ref    string  `json:"ref"`
	Effort *string `json:"effort,omitempty"`
}

// Resolver creates providers without exposing credential material to callers
// that only need catalog metadata. Local boots use a config-backed
// implementation; remote-runtime uses a Broker-backed implementation that
// streams through an SSH reverse tunnel to the local machine.
type Resolver interface {
	// Catalog returns non-sensitive descriptors the caller may select.
	Catalog() []Descriptor
	// Resolve builds a Provider for selection. Implementations must not
	// return providers that leak secrets via their public methods.
	Resolve(selection Selection) (Provider, error)
}

// StaticResolver is a test double that maps refs to prebuilt providers.
type StaticResolver struct {
	Descriptors []Descriptor
	Providers   map[string]Provider
}

// Catalog implements Resolver.
func (r *StaticResolver) Catalog() []Descriptor {
	if r == nil {
		return nil
	}
	out := make([]Descriptor, len(r.Descriptors))
	copy(out, r.Descriptors)
	return out
}

// Resolve implements Resolver.
func (r *StaticResolver) Resolve(selection Selection) (Provider, error) {
	if r == nil {
		return nil, fmt.Errorf("provider resolver is nil")
	}
	ref := strings.TrimSpace(selection.Ref)
	if ref == "" {
		return nil, fmt.Errorf("provider selection ref is required")
	}
	p, ok := r.Providers[ref]
	if !ok {
		// Allow lookup by bare name when catalog uses name/model refs.
		for k, v := range r.Providers {
			if strings.HasPrefix(k, ref+"/") || strings.HasSuffix(k, "/"+ref) {
				return v, nil
			}
		}
		return nil, fmt.Errorf("unknown provider ref %q", ref)
	}
	return p, nil
}
