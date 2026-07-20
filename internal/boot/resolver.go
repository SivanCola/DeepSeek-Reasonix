package boot

import (
	"fmt"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/netclient"
	"reasonix/internal/provider"
)

// LocalProviderResolver resolves providers from the loaded config using the
// same NewProviderWithProxy path local boots always used. It is the default
// when Options.ProviderResolver is nil.
type LocalProviderResolver struct {
	cfg   *config.Config
	proxy netclient.ProxySpec
}

// NewLocalProviderResolver builds a resolver over cfg. cfg must not be nil.
func NewLocalProviderResolver(cfg *config.Config, proxy netclient.ProxySpec) *LocalProviderResolver {
	return &LocalProviderResolver{cfg: cfg, proxy: proxy}
}

// Catalog implements provider.Resolver. Descriptors intentionally omit base
// URLs, API key env names, headers, and proxy settings.
func (r *LocalProviderResolver) Catalog() []provider.Descriptor {
	if r == nil || r.cfg == nil {
		return nil
	}
	out := make([]provider.Descriptor, 0, len(r.cfg.Providers))
	for i := range r.cfg.Providers {
		e := &r.cfg.Providers[i]
		ref := e.Name + "/" + e.Model
		if strings.TrimSpace(e.Model) == "" {
			ref = e.Name
		}
		d := provider.Descriptor{
			Ref:           ref,
			DisplayName:   e.Name,
			Model:         e.Model,
			ContextWindow: e.ContextWindow,
			Vision:        config.EffectiveVision(e),
			Tools:         true,
			DefaultEffort: config.EffectiveEffort(e),
		}
		if price := e.PriceForModel(e.Model); price != nil {
			d.PricingCurrency = price.Currency
			d.InputPerMillion = price.Input
			d.OutputPerMillion = price.Output
		}
		if len(e.SupportedEfforts) > 0 {
			d.Efforts = append([]string(nil), e.SupportedEfforts...)
			d.Reasoning = true
		}
		// DeepSeek thinking protocol requires tool-call reasoning replay.
		if config.ReasoningProtocolForEntry(e) == config.ReasoningProtocolDeepSeek {
			d.ToolCallReasoning = true
			d.Reasoning = true
		}
		out = append(out, d)
	}
	return out
}

// Resolve implements provider.Resolver.
func (r *LocalProviderResolver) Resolve(selection provider.Selection) (provider.Provider, error) {
	if r == nil || r.cfg == nil {
		return nil, fmt.Errorf("local provider resolver is not configured")
	}
	ref := strings.TrimSpace(selection.Ref)
	if ref == "" {
		return nil, fmt.Errorf("provider selection ref is required")
	}
	entry, ok := r.cfg.ResolveModel(ref)
	if !ok {
		// Also try name-only and name/model splits already handled by ResolveModel.
		return nil, fmt.Errorf("%w %q", ErrUnknownModel, ref)
	}
	if selection.Effort != nil {
		entry.Effort = *selection.Effort
	}
	return NewProviderWithProxy(entry, r.proxy)
}

// resolveProvider uses opts.ProviderResolver when set, otherwise builds a local
// resolver from cfg. All boot provider construction paths should call this.
func resolveProvider(opts Options, cfg *config.Config, proxy netclient.ProxySpec, selection provider.Selection) (provider.Provider, error) {
	if opts.ProviderResolver != nil {
		return opts.ProviderResolver.Resolve(selection)
	}
	return NewLocalProviderResolver(cfg, proxy).Resolve(selection)
}

func modelRefFromEntry(e *config.ProviderEntry) string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Model) == "" {
		return e.Name
	}
	return e.Name + "/" + e.Model
}

// resolveModelEntry picks a ProviderEntry for metadata (context window, pricing,
// balance URL). When a ProviderResolver is injected and the local config has no
// matching provider, a synthetic entry is built from the catalog descriptor so
// remote-runtime can boot without local API keys or provider URLs.
func resolveModelEntry(opts Options, cfg *config.Config, modelName string) (*config.ProviderEntry, string, error) {
	if entry, ok := cfg.ResolveModel(modelName); ok {
		return entry, modelRefFromEntry(entry), nil
	}
	if opts.ProviderResolver != nil {
		entry := syntheticEntryFromResolver(opts.ProviderResolver, modelName)
		if strings.TrimSpace(entry.Name) != "" {
			return entry, modelRefFromEntry(entry), nil
		}
	}
	return nil, "", fmt.Errorf("%w %q (configured: %s); note: defining [[providers]] replaces the built-in presets, so add a [[providers]] entry for it or use a configured name, or run `reasonix setup` to reconfigure", ErrUnknownModel, modelName, providerNames(cfg))
}

func resolveOptionalEntry(opts Options, cfg *config.Config, ref string) (*config.ProviderEntry, bool) {
	if entry, ok := cfg.ResolveModel(ref); ok {
		return entry, true
	}
	if opts.ProviderResolver == nil {
		return nil, false
	}
	entry := syntheticEntryFromResolver(opts.ProviderResolver, ref)
	if strings.TrimSpace(entry.Name) == "" {
		return nil, false
	}
	return entry, true
}

func syntheticEntryFromResolver(r provider.Resolver, ref string) *config.ProviderEntry {
	ref = strings.TrimSpace(ref)
	if r == nil || ref == "" {
		return &config.ProviderEntry{}
	}
	var match *provider.Descriptor
	for _, d := range r.Catalog() {
		if d.Ref == ref || d.DisplayName == ref || d.Model == ref || strings.HasPrefix(d.Ref, ref+"/") {
			dd := d
			match = &dd
			break
		}
	}
	if match == nil {
		// Still allow Resolve to succeed later; metadata defaults are fine.
		name, model := splitRef(ref)
		return &config.ProviderEntry{Name: name, Model: model, ContextWindow: 128_000}
	}
	name, model := splitRef(match.Ref)
	if model == "" {
		model = match.Model
	}
	if name == "" {
		name = match.DisplayName
	}
	entry := &config.ProviderEntry{
		Name:             name,
		Model:            model,
		ContextWindow:    match.ContextWindow,
		SupportedEfforts: append([]string(nil), match.Efforts...),
		DefaultEffort:    match.DefaultEffort,
		Vision:           match.Vision,
	}
	if match.InputPerMillion > 0 || match.OutputPerMillion > 0 {
		entry.Price = &provider.Pricing{
			Input:    match.InputPerMillion,
			Output:   match.OutputPerMillion,
			Currency: match.PricingCurrency,
		}
	}
	return entry
}

func splitRef(ref string) (name, model string) {
	ref = strings.TrimSpace(ref)
	if i := strings.IndexByte(ref, '/'); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}
