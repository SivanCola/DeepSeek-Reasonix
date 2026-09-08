package config

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"reasonix/internal/provider"
)

func normalizeRuntimeConfigWithMigrationJournal(cfg *Config) error {
	cfg.loadOpenCodeGoJournal(userConfigLoadPath())
	return normalizeLoadedConfig(cfg)
}

func (c *Config) loadOpenCodeGoJournal(path string) {
	if c == nil || path == "" {
		return
	}
	resolved, exists, err := statConfigPath(path)
	if err != nil || !exists {
		return
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return
	}
	c.openCodeGoJournal = readOpenCodeGoJournal(resolved, raw)
}

// Project files are never rewritten by startup. Resolve their legacy routes in
// memory, while version-10 global connections retain later manual API choices.
func normalizeOpenCodeGoRuntimeCompatibility(c *Config) {
	if c == nil {
		return
	}
	previous := c.openCodeGoJournal
	j, _ := planOpenCodeGoUpgradeFiltered(c, func(p ProviderEntry) bool {
		return c.ConfigVersion < openCodeGoUpgradeVersion || c.providerSources[providerMergeKey(p)] == providerSourceProject
	})
	if previous != nil {
		for ref, alias := range previous.Aliases {
			j.Aliases[ref] = alias
		}
		for ref, alias := range previous.SearchAliases {
			j.SearchAliases[ref] = alias
		}
		j.Connections = append(j.Connections, previous.Connections...)
		j.Skipped = append(j.Skipped, previous.Skipped...)
	}
	if len(j.Aliases) > 0 || len(j.SearchAliases) > 0 || previous != nil {
		c.openCodeGoJournal = &j
	}
}

func (c *Config) resolveOpenCodeGoAlias(ref string, search bool) (string, error) {
	if c == nil || c.openCodeGoJournal == nil {
		return ref, nil
	}
	aliases := c.openCodeGoJournal.Aliases
	if search {
		aliases = c.openCodeGoJournal.SearchAliases
	}
	alias, ok := aliases[strings.TrimSpace(ref)]
	if !ok {
		return ref, nil
	}
	name, model, ok := strings.Cut(alias.Target, "/")
	p, found := c.Provider(name)
	if !ok || !found || !p.HasModel(model) {
		return "", fmt.Errorf("MIGRATED_MODEL_UNAVAILABLE: %q moved to %q; restore that OpenCode Go connection in model settings", ref, alias.Target)
	}
	if _, official := provider.OpenCodeGoRequestRoute(p.Kind, p.BaseURL, p.RequestURL, p.ChatURL); !official || openCodeGoIdentity(*p) != alias.Identity {
		return "", fmt.Errorf("MIGRATED_MODEL_UNAVAILABLE: account or endpoint for %q has changed; restore the original connection for %q", alias.Target, ref)
	}
	return alias.Target, nil
}

// ModelReferenceError distinguishes an unavailable migrated identity from an
// ordinary stale selection. Callers must not substitute a default in this case.
func (c *Config) ModelReferenceError(ref string) error {
	_, err := c.resolveOpenCodeGoAlias(ref, false)
	return err
}

// OpenCodeGoUpgradeSummary is consumed by the existing startup notice channel.
func (c *Config) OpenCodeGoUpgradeSummary() string {
	if c == nil || c.openCodeGoJournal == nil {
		return ""
	}
	j := c.openCodeGoJournal
	var parts []string
	if len(j.Connections) > 0 {
		parts = append(parts, "OpenCode Go connections organized by model API: "+strings.Join(j.Connections, ", "))
	}
	if len(j.Skipped) > 0 {
		parts = append(parts, "Preserved custom connections: "+strings.Join(j.Skipped, "; "))
	}
	return strings.Join(parts, ". ")
}

func (c *Config) resolveOpenCodeGoAutomaticSearch(current *ProviderEntry, resolve func(*ProviderEntry) *ProviderEntry) *ProviderEntry {
	if isOpenCodeGoEntry(current) {
		// A migrated chat connection keeps its auxiliary search on the same
		// credential reference, even when another account appears earlier.
		if c.openCodeGoJournal != nil {
			for _, modelSpecific := range []bool{true, false} {
				refs := make([]string, 0, len(c.openCodeGoJournal.SearchAliases))
				for ref := range c.openCodeGoJournal.SearchAliases {
					refs = append(refs, ref)
				}
				slices.Sort(refs)
				for _, ref := range refs {
					alias := c.openCodeGoJournal.SearchAliases[ref]
					_, model, _ := strings.Cut(alias.Target, "/")
					if alias.Identity != openCodeGoIdentity(*current) || (modelSpecific && model != current.Model) {
						continue
					}
					if entry, err := c.ResolveWebSearchModel(ref); err == nil {
						if selected := resolve(entry); selected != nil {
							return selected
						}
					}
				}
			}
		}
		for i := range c.Providers {
			if openCodeGoIdentity(c.Providers[i]) != openCodeGoIdentity(*current) || !isOpenCodeGoEntry(&c.Providers[i]) {
				continue
			}
			entry, ok := c.ResolveModel(c.Providers[i].Name)
			if ok {
				if selected := resolve(entry); selected != nil {
					return selected
				}
			}
		}
	}
	return nil
}
