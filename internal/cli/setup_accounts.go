package cli

import (
	"fmt"
	"os"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/i18n"
)

type setupMenuKind uint8

const (
	setupMenuProvider setupMenuKind = iota
	setupMenuAccount
	setupMenuAddAccount
	setupMenuAddOpenAI
	setupMenuAddAnthropic
	setupMenuSave
	setupMenuCancel
)

type setupMenuAction struct {
	kind       setupMenuKind
	provider   int
	providerID string
	accountID  string
}

func providerManagerMenu(s *providerSetupSession) ([]menuItem, []setupMenuAction) {
	items := make([]menuItem, 0, len(s.cfg.Providers)+8)
	actions := make([]setupMenuAction, 0, len(s.cfg.Providers)+8)
	seen := map[string]bool{}
	for _, account := range s.cfg.ProviderAccounts {
		if account.Retired {
			continue
		}
		entries, _ := s.cfg.ResolveAccountProvider(account.ProviderID, account.ID)
		keyStatus := i18n.M.SetupKeyMissing
		if account.APIKeyEnv == "" || config.CredentialIsSet(account.APIKeyEnv) || s.pendingCredentials[account.APIKeyEnv] != "" {
			keyStatus = i18n.M.SetupKeySet
		}
		desc := fmt.Sprintf("%s · %d · %s", account.ProviderID, len(entries), keyStatus)
		if account.Default {
			desc += " · " + i18n.M.SetupDefaultBadge
		}
		if !account.IsEnabled() {
			desc += " · disabled"
		}
		items = append(items, menuItem{name: account.Label, desc: desc})
		actions = append(actions, setupMenuAction{kind: setupMenuAccount, providerID: account.ProviderID, accountID: account.ID})
		for _, e := range entries {
			seen[e.Name] = true
		}
	}
	for i, p := range s.cfg.Providers {
		if seen[p.Name] {
			continue
		}
		models := p.ModelList()
		keyStatus := i18n.M.SetupKeyMissing
		if p.APIKeyEnv == "" || config.CredentialIsSet(p.APIKeyEnv) || s.pendingCredentials[p.APIKeyEnv] != "" {
			keyStatus = i18n.M.SetupKeySet
		}
		desc := fmt.Sprintf("%s · %d %s · %s", p.Kind, len(models), i18n.M.SetupModelsUnit, keyStatus)
		if s.cfg.DefaultModel == p.Name || config.ModelRefsProvider(s.cfg.DefaultModel, p.Name) {
			desc += " · " + i18n.M.SetupDefaultBadge
		}
		items = append(items, menuItem{name: p.Name, desc: desc})
		actions = append(actions, setupMenuAction{kind: setupMenuProvider, provider: i})
	}
	if !s.projectScoped {
		items = append(items, menuItem{name: i18n.M.SetupAddAccount, desc: i18n.M.SetupAddAccountDesc})
		actions = append(actions, setupMenuAction{kind: setupMenuAddAccount})
	}
	items = append(items,
		menuItem{name: i18n.M.SetupAddOpenAI, desc: i18n.M.CustomProviderDesc},
		menuItem{name: i18n.M.SetupAddAnthropic, desc: i18n.M.AnthropicProviderDesc},
		menuItem{name: i18n.M.SetupSaveExit, desc: i18n.M.SetupSaveExitDesc},
		menuItem{name: i18n.M.SetupCancel, desc: i18n.M.SetupCancelDesc},
	)
	actions = append(actions,
		setupMenuAction{kind: setupMenuAddOpenAI},
		setupMenuAction{kind: setupMenuAddAnthropic},
		setupMenuAction{kind: setupMenuSave},
		setupMenuAction{kind: setupMenuCancel},
	)
	return items, actions
}

func manageProviderAccount(s *providerSetupSession, providerID, accountID string) {
	_, account, ok := lookupSessionAccount(s, providerID, accountID)
	if !ok {
		return
	}
	idx, err := selectOne(fmt.Sprintf("%s / %s", account.ProviderID, account.Label), []menuItem{
		{name: i18n.M.SetupUpdateKey},
		{name: i18n.M.SetupSetDefault},
		{name: i18n.M.SetupAccountRename},
		{name: i18n.M.SetupAccountToggle},
		{name: i18n.M.SetupAccountRetire},
		{name: i18n.M.SetupBack},
	})
	if err != nil || idx == 5 {
		return
	}
	switch idx {
	case 0:
		updateAccountKey(s, account)
	case 1:
		_ = s.cfg.SetProviderAccountDefault(account.ProviderID, account.ID)
	case 2:
		renameSessionAccount(s, account)
	case 3:
		_ = s.cfg.SetProviderAccountEnabled(account.ProviderID, account.ID, !account.IsEnabled())
	case 4:
		if err := s.cfg.RetireProviderAccount(account.ProviderID, account.ID); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
}

func addProviderAccountToSession(s *providerSetupSession) bool {
	if s.projectScoped {
		fmt.Fprintln(os.Stderr, i18n.M.SetupProjectNoAccounts)
		return false
	}
	presets := []config.ProviderPreset{}
	for _, preset := range config.CuratedProviderPresets() {
		if preset.ID == "opencode-go-recommended" || preset.AccountGroupID == "deepseek" && preset.ID == "deepseek-responses" {
			presets = append(presets, preset)
		}
	}
	if len(presets) == 0 {
		return false
	}
	items := make([]menuItem, 0, len(presets))
	for _, preset := range presets {
		items = append(items, menuItem{name: preset.Label, desc: preset.AccountGroupID})
	}
	idx, err := selectOne(i18n.M.SetupAddAccount, items)
	if err != nil || idx < 0 || idx >= len(presets) {
		return false
	}
	preset := presets[idx]
	label := strings.TrimSpace(askLine(i18n.M.SetupAccountLabel, "Team"))
	if label == "" {
		return false
	}
	key := strings.TrimSpace(askLine(fmt.Sprintf(i18n.M.SetupPromptAPIKeyFmt, preset.KeyEnv), ""))
	account, err := s.cfg.AddProviderAccount(preset.AccountGroupID, preset.ID, label, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}
	if key != "" {
		if err := s.setCredential(account.APIKeyEnv, key); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return false
		}
	}
	entries, _ := s.cfg.ResolveAccountProvider(account.ProviderID, account.ID)
	s.addProviderAccess(entries)
	s.promoteDefaultToNewProviders(entries)
	return true
}

func updateAccountKey(s *providerSetupSession, account config.ProviderAccount) {
	if strings.TrimSpace(account.APIKeyEnv) == "" {
		return
	}
	value := strings.TrimSpace(askLine(fmt.Sprintf(i18n.M.SetupPromptAPIKeyFmt, account.APIKeyEnv), ""))
	if value == "" {
		return
	}
	if err := s.setCredential(account.APIKeyEnv, value); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func renameSessionAccount(s *providerSetupSession, account config.ProviderAccount) {
	label := strings.TrimSpace(askLine(i18n.M.SetupAccountLabel, account.Label))
	if label == "" {
		return
	}
	_ = s.cfg.RenameProviderAccount(account.ProviderID, account.ID, label)
}

func lookupSessionAccount(s *providerSetupSession, providerID, accountID string) (int, config.ProviderAccount, bool) {
	for i, account := range s.cfg.ProviderAccounts {
		if account.ProviderID == providerID && account.ID == accountID {
			return i, account, true
		}
	}
	return -1, config.ProviderAccount{}, false
}

func askLine(label, def string) string {
	fmt.Printf("%s [%s]: ", label, def)
	var line string
	_, _ = fmt.Scanln(&line)
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}
