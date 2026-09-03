package config

import (
	"fmt"
	"reflect"
	"strings"
)

// ProviderAccountChange is an atomic before/after patch for one curated account.
// Provider entries remain a derived compatibility projection.
type ProviderAccountChange struct {
	FamilyID             string
	AccountID            string
	Before               *ProviderAccount
	After                *ProviderAccount
	BeforeDefaultModel   string
	AfterDefaultModel    string
	BeforeProviderAccess []string
	AfterProviderAccess  []string
}

// ApplyProviderAccountChange validates and applies an account patch, then
// materializes its provider routes. It is intentionally side-effect free on
// validation or reconcile failure.
func (c *Config) ApplyProviderAccountChange(change ProviderAccountChange) (err error) {
	if c == nil {
		return fmt.Errorf("apply provider account change: nil config")
	}
	accountsBefore := cloneProviderAccounts(c.ProviderAccounts)
	providersBefore := cloneProviderEntries(c.Providers)
	accessBefore := append([]string(nil), c.Desktop.ProviderAccess...)
	defaultBefore := c.DefaultModel
	committed := false
	defer func() {
		if committed || err == nil {
			return
		}
		c.ProviderAccounts = accountsBefore
		c.Providers = providersBefore
		c.Desktop.ProviderAccess = accessBefore
		c.DefaultModel = defaultBefore
	}()
	familyID := strings.TrimSpace(change.FamilyID)
	accountID := strings.TrimSpace(change.AccountID)
	if change.After != nil {
		if familyID == "" {
			familyID = strings.TrimSpace(change.After.ProviderID)
		}
		if accountID == "" {
			accountID = strings.TrimSpace(change.After.ID)
		}
	}
	if familyID == "" || accountID == "" {
		return fmt.Errorf("apply provider account change: family and account are required")
	}
	if _, ok := curatedFamilyByID(familyID); !ok {
		return fmt.Errorf("apply provider account change: provider family %q is not curated", familyID)
	}
	idx, current, exists := c.lookupProviderAccount(familyID, accountID)
	if change.Before != nil {
		if !exists || !reflect.DeepEqual(current, *change.Before) {
			return fmt.Errorf("provider account %s/%s changed concurrently", familyID, accountID)
		}
	}
	if change.Before == nil && exists {
		return fmt.Errorf("provider account %s/%s already exists", familyID, accountID)
	}
	if change.After == nil && !exists {
		return fmt.Errorf("provider account %s/%s does not exist", familyID, accountID)
	}
	if change.After != nil {
		after := cloneProviderAccount(*change.After)
		after.ProviderID, after.ID = familyID, accountID
		if err := validateProviderAccount(after); err != nil {
			return err
		}
		if !after.Retired && !after.IsEnabled() && after.Default {
			return fmt.Errorf("provider account %s/%s cannot be default while disabled", familyID, accountID)
		}
		if exists {
			c.ProviderAccounts[idx] = after
		} else {
			c.ProviderAccounts = append(c.ProviderAccounts, after)
		}
	}
	if change.After == nil {
		c.ProviderAccounts = append(c.ProviderAccounts[:idx], c.ProviderAccounts[idx+1:]...)
	}
	if change.AfterDefaultModel != "" {
		if change.BeforeDefaultModel != "" && c.DefaultModel != change.BeforeDefaultModel {
			return fmt.Errorf("default_model changed concurrently")
		}
		c.DefaultModel = strings.TrimSpace(change.AfterDefaultModel)
	}
	if change.AfterProviderAccess != nil {
		if change.BeforeProviderAccess != nil && !reflect.DeepEqual(c.Desktop.ProviderAccess, change.BeforeProviderAccess) {
			return fmt.Errorf("desktop.provider_access changed concurrently")
		}
		c.Desktop.ProviderAccess = append([]string(nil), change.AfterProviderAccess...)
	}
	if _, _, err := ReconcileProviderAccounts(c); err != nil {
		return err
	}
	committed = true
	return nil
}

func curatedFamilyByID(id string) (ProviderFamilyDefinition, bool) {
	for _, family := range CuratedProviderFamilies() {
		if family.ID == strings.TrimSpace(id) {
			return family, true
		}
	}
	return ProviderFamilyDefinition{}, false
}
