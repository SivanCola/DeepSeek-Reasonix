package config

import (
	"reflect"
	"testing"
)

func TestProviderFamilyDefinitionsAreDeterministic(t *testing.T) {
	families := CuratedProviderFamilies()
	if len(families) == 0 {
		t.Fatal("expected curated provider families")
	}
	seen := map[string]bool{}
	for _, family := range families {
		if family.ID == "" || seen[family.ID] {
			t.Fatalf("invalid or duplicate family: %+v", family)
		}
		seen[family.ID] = true
		if family.RecommendedPresetID == "" || len(family.Routes) == 0 {
			t.Fatalf("family %q missing recommendation/routes: %+v", family.ID, family)
		}
		for i := 1; i < len(family.Routes); i++ {
			if family.Routes[i-1].DisplayOrder > family.Routes[i].DisplayOrder {
				t.Fatalf("family %q routes not ordered: %+v", family.ID, family.Routes)
			}
		}
	}
}

func TestProviderSelectionParsesCanonicalAndLegacyRefs(t *testing.T) {
	cfg := Default()
	if _, err := cfg.AddProviderAccount("deepseek", "", "Team", "DEEPSEEK_TEAM_KEY"); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		ref  string
		want ProviderSelection
	}{
		{"deepseek/team/deepseek-v4-flash", ProviderSelection{FamilyID: "deepseek", AccountID: "team", Model: "deepseek-v4-flash"}},
		{"deepseek--team/deepseek-v4-flash", ProviderSelection{FamilyID: "deepseek", AccountID: "team", Model: "deepseek-v4-flash"}},
		{"deepseek-flash/deepseek-v4-flash", ProviderSelection{FamilyID: "deepseek", AccountID: "main", Model: "deepseek-v4-flash"}},
	}
	for _, tc := range tests {
		got, err := ParseProviderSelection(cfg, tc.ref)
		if err != nil {
			t.Fatalf("ParseProviderSelection(%q): %v", tc.ref, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("ParseProviderSelection(%q) = %+v, want %+v", tc.ref, got, tc.want)
		}
	}
}

func TestProviderSelectionResolvesFamilyAccountModel(t *testing.T) {
	cfg := Default()
	account, err := cfg.AddProviderAccount("opencode-go", "opencode-go-recommended", "Team", "OPENCODE_TEAM_KEY")
	if err != nil {
		t.Fatal(err)
	}
	selection := ProviderSelection{FamilyID: account.ProviderID, AccountID: account.ID, Model: "grok-4.5"}
	entry, err := cfg.ResolveSelection(selection)
	if err != nil {
		t.Fatal(err)
	}
	if entry.AccountProviderID != account.ProviderID || entry.AccountID != account.ID || entry.Model != selection.Model {
		t.Fatalf("resolved entry = %+v", entry)
	}
	route, err := cfg.RouteForSelection(selection)
	if err != nil {
		t.Fatal(err)
	}
	if route.ID == "" || route.Kind == "" {
		t.Fatalf("invalid route = %+v", route)
	}
}
