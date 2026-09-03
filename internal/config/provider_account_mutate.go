package config

import (
	"fmt"
	"strings"
)

func (c *Config) AddProviderAccount(providerID, presetID, label, apiKeyEnv string) (ProviderAccount, error) {
	if c == nil {
		return ProviderAccount{}, fmt.Errorf("add provider account: nil config")
	}
	ensureProviderAccounts(c)
	providerID = strings.TrimSpace(providerID)
	presetID = strings.TrimSpace(presetID)
	label = strings.TrimSpace(label)
	apiKeyEnv = strings.TrimSpace(apiKeyEnv)
	if providerID == "" && presetID != "" {
		if preset, ok := CuratedProviderPreset(presetID); ok {
			providerID = preset.resolvedAccountGroupID()
		} else {
			providerID = accountGroupIDForPresetID(presetID)
		}
	}
	if providerID == "" {
		return ProviderAccount{}, fmt.Errorf("provider account: provider_id is required")
	}
	if label == "" {
		label = defaultAccountLabel(MainProviderAccountID)
	}
	usedIDs := c.providerAccountUsedIDs()
	id := SuggestProviderAccountID(providerID, label)
	if !c.hasProviderFamilyAccount(providerID) {
		id = MainProviderAccountID
	}
	id = uniqueProviderAccountID(providerID, id, usedIDs)
	if apiKeyEnv == "" {
		apiKeyEnv = SuggestAccountAPIKeyEnv(baseAPIKeyEnvForGroup(providerID), id, c.usedAPIKeyEnvs())
	}
	account := ProviderAccount{
		ProviderID: providerID,
		PresetID:   presetID,
		ID:         id,
		Label:      label,
		APIKeyEnv:  apiKeyEnv,
		Default:    !c.hasProviderFamilyDefault(providerID),
	}
	if err := validateProviderAccount(account); err != nil {
		return ProviderAccount{}, err
	}
	c.ProviderAccounts = append(c.ProviderAccounts, account)
	if _, _, err := ReconcileProviderAccounts(c); err != nil {
		c.ProviderAccounts = c.ProviderAccounts[:len(c.ProviderAccounts)-1]
		return ProviderAccount{}, err
	}
	return account, nil
}

func (c *Config) SetProviderAccountDefault(providerID, accountID string) error {
	idx, account, ok := c.lookupProviderAccount(providerID, accountID)
	if !ok {
		return fmt.Errorf("set default account: no account %s/%s", providerID, accountID)
	}
	if account.Retired || !account.IsEnabled() {
		return fmt.Errorf("set default account: %s/%s is not available", providerID, accountID)
	}
	for i := range c.ProviderAccounts {
		if c.ProviderAccounts[i].ProviderID == account.ProviderID {
			c.ProviderAccounts[i].Default = i == idx
		}
	}
	return nil
}

func (c *Config) SetProviderAccountEnabled(providerID, accountID string, enabled bool) error {
	idx, account, ok := c.lookupProviderAccount(providerID, accountID)
	if !ok {
		return fmt.Errorf("set account enabled: no account %s/%s", providerID, accountID)
	}
	if account.Retired {
		return fmt.Errorf("set account enabled: %s/%s is retired", providerID, accountID)
	}
	c.ProviderAccounts[idx].Enabled = boolPointer(enabled)
	if !enabled && account.Default {
		c.ProviderAccounts[idx].Default = false
		c.ensureFamilyDefault(account.ProviderID)
	}
	return nil
}

func (c *Config) RenameProviderAccount(providerID, accountID, label string) error {
	idx, account, ok := c.lookupProviderAccount(providerID, accountID)
	if !ok {
		return fmt.Errorf("rename account: no account %s/%s", providerID, accountID)
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Errorf("rename account: label is required")
	}
	c.ProviderAccounts[idx].Label = label
	for i := range c.Providers {
		if c.Providers[i].AccountProviderID == account.ProviderID && c.Providers[i].AccountID == account.ID {
			c.Providers[i].AccountLabel = label
		}
	}
	return nil
}

func (c *Config) RetireProviderAccount(providerID, accountID string) error {
	idx, account, ok := c.lookupProviderAccount(providerID, accountID)
	if !ok {
		return fmt.Errorf("retire account: no account %s/%s", providerID, accountID)
	}
	if refs := c.ProviderAccountConfigRefs(providerID, accountID); len(refs) > 0 {
		return fmt.Errorf("retire account %s/%s: still referenced by %s", providerID, accountID, strings.Join(refs, ", "))
	}
	c.ProviderAccounts[idx].Retired = true
	c.ProviderAccounts[idx].Enabled = boolPointer(false)
	c.ProviderAccounts[idx].Default = false
	c.ensureFamilyDefault(account.ProviderID)
	return nil
}

func (c *Config) SetProviderAccountKeyEnv(providerID, accountID, apiKeyEnv string) error {
	idx, account, ok := c.lookupProviderAccount(providerID, accountID)
	if !ok {
		return fmt.Errorf("set account key: no account %s/%s", providerID, accountID)
	}
	apiKeyEnv = strings.TrimSpace(apiKeyEnv)
	if apiKeyEnv == "" || !IsValidCredentialKey(apiKeyEnv) {
		return fmt.Errorf("set account key: api_key_env %q is not a valid environment variable name", apiKeyEnv)
	}
	c.ProviderAccounts[idx].APIKeyEnv = apiKeyEnv
	for i := range c.Providers {
		if c.Providers[i].AccountProviderID == account.ProviderID && c.Providers[i].AccountID == account.ID {
			c.Providers[i].APIKeyEnv = apiKeyEnv
		}
	}
	return nil
}

func (c *Config) ProviderAccountConfigRefs(providerID, accountID string) []string {
	entries, ok := c.ResolveAccountProvider(providerID, accountID)
	if !ok {
		return nil
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	var refs []string
	add := func(field, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		entry, found := c.ResolveModel(value)
		if !found || !names[entry.Name] {
			return
		}
		refs = append(refs, field)
	}
	add("default_model", c.DefaultModel)
	add("agent.planner_model", c.Agent.PlannerModel)
	add("agent.vision_model", c.Agent.VisionModel)
	add("agent.subagent_model", c.Agent.SubagentModel)
	add("agent.guardian_model", c.Agent.GuardianModel)
	add("agent.recovery_model", c.Agent.RecoveryModel)
	for skill, ref := range c.Agent.SubagentModels {
		add("agent.subagent_models."+skill, ref)
	}
	add("bot.model", c.Bot.Model)
	for _, conn := range c.Bot.Connections {
		add("bot.connections."+conn.ID, conn.Model)
	}
	return refs
}

func (c *Config) hasProviderFamilyAccount(providerID string) bool {
	for _, account := range c.ProviderAccounts {
		if account.ProviderID == providerID && !account.Retired {
			return true
		}
	}
	return false
}

func (c *Config) hasProviderFamilyDefault(providerID string) bool {
	for _, account := range c.ProviderAccounts {
		if account.ProviderID == providerID && account.Default && account.IsEnabled() {
			return true
		}
	}
	return false
}

func (c *Config) ensureFamilyDefault(providerID string) {
	if c.hasProviderFamilyDefault(providerID) {
		return
	}
	for i, account := range c.ProviderAccounts {
		if account.ProviderID == providerID && account.IsEnabled() {
			c.ProviderAccounts[i].Default = true
			return
		}
	}
}

func (c *Config) SetProviderEffort(name, effort string) error {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			c.Providers[i].Effort = normalizeStoredEffort(effort)
			return nil
		}
	}
	return fmt.Errorf("set provider effort: no provider %q", name)
}
