package main

import (
	"fmt"
	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/extension/providerext"
	"strings"
)

// Resolve historical identity before selecting any fallback for a new runtime.
func (a *App) resolveStartupModel(cfg *config.Config, tabModel, startupSessionPath, tabID string) (string, error) {
	model := strings.TrimSpace(tabModel)
	if sessionModel, ok := agent.LoadSessionModel(startupSessionPath); ok {
		config.NormalizeLegacyMimoCustomProvidersForRefs(cfg, sessionModel)
		if err := cfg.ModelReferenceError(sessionModel); err != nil {
			return "", err
		}
		if _, ok := cfg.ResolveModel(sessionModel); ok {
			model = sessionModel
		}
	}
	if model == "" {
		if def := strings.TrimSpace(cfg.DefaultModel); providerext.PluginRefOwner(def) != "" {
			// A plugin-namespaced default_model belongs to an extension
			// sidecar: the config catalog can never resolve it, but boot's
			// merged resolver can. Pass it through untouched.
			model = def
		} else {
			resolved, _, ok := cfg.ResolveDesktopNewSessionModel()
			if !ok {
				return "", errNoDesktopChatModel
			}
			model = resolved
		}
	}
	config.NormalizeLegacyMimoCustomProvidersForRefs(cfg, model)
	requestedModel := model
	if err := cfg.ModelReferenceError(model); err != nil {
		return "", err
	}
	if providerext.PluginRefOwner(model) == "" {
		// Plugin refs skip the config fallback: rerouting an unavailable
		// extension model onto a config provider would silently change the
		// session; boot's unknown-model error is the honest failure.
		if resolved, fallback, ok := cfg.ResolveModelWithFallback(model); ok {
			if fallback && strings.TrimSpace(tabModel) != "" {
				a.noticeForTab(tabID, fmt.Sprintf("model %q is no longer available; switched to %s", requestedModel, resolved))
			}
			model = resolved
		}
	}

	return model, nil
}
