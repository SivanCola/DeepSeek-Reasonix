package config

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	ccSwitchProviderStatusReady       = "ready"
	ccSwitchProviderStatusUnsupported = "unsupported"
	ccSwitchProviderStatusInvalid     = "invalid"
	ccSwitchProviderStatusMissingKey  = "missing_key"
)

// ProviderImportCandidate is safe to show in UI/CLI. It deliberately omits the
// API key value read from cc-switch; the key only flows inside Import.
type ProviderImportCandidate struct {
	ID          string   `json:"id"`
	SourceID    string   `json:"sourceId"`
	AppType     string   `json:"appType"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	BaseURL     string   `json:"baseUrl"`
	Host        string   `json:"host"`
	Models      []string `json:"models"`
	Default     string   `json:"default"`
	TargetName  string   `json:"targetName"`
	APIKeyEnv   string   `json:"apiKeyEnv"`
	AuthScheme  string   `json:"authScheme,omitempty"`
	KeyPresent  bool     `json:"keyPresent"`
	Importable  bool     `json:"importable"`
	Recommended bool     `json:"recommended"`
	Status      string   `json:"status"`
	Reasons     []string `json:"reasons"`

	keyValue string
}

type ProviderImportSkipped struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type ProviderImportResult struct {
	Total             int                     `json:"total"`
	Imported          int                     `json:"imported"`
	Added             int                     `json:"added"`
	Updated           int                     `json:"updated"`
	Skipped           int                     `json:"skipped"`
	KeyImported       int                     `json:"keyImported"`
	KeySkipped        int                     `json:"keySkipped"`
	SkippedCandidates []ProviderImportSkipped `json:"skippedCandidates"`
}

type ccSwitchProviderRow struct {
	ID             string `json:"id"`
	AppType        string `json:"app_type"`
	Name           string `json:"name"`
	SettingsConfig string `json:"settings_config"`
	ProviderType   string `json:"provider_type"`
}

type ccSwitchProviderEndpointRow struct {
	ProviderID string `json:"provider_id"`
	AppType    string `json:"app_type"`
	URL        string `json:"url"`
}

type ccSwitchProviderSource struct {
	ID             string
	AppType        string
	Name           string
	SettingsConfig string
	ProviderType   string
	Endpoints      []string
}

type ccSwitchProviderSettings struct {
	env    map[string]string
	auth   map[string]string
	fields map[string]string
	lists  map[string][]string
	config string
}

type codexProviderConfig struct {
	Model          string `toml:"model"`
	ModelProvider  string `toml:"model_provider"`
	ModelProviders map[string]struct {
		BaseURL string `toml:"base_url"`
	} `toml:"model_providers"`
}

func LoadCCSwitchProviderCandidates() ([]ProviderImportCandidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cc-switch provider import: resolve home: %w", err)
	}
	return loadCCSwitchProviderCandidatesFromRoot(filepath.Join(home, ccSwitchDir))
}

func loadCCSwitchProviderCandidatesFromRoot(root string) ([]ProviderImportCandidate, error) {
	sources, err := loadCCSwitchProviderSourcesFromRoot(root)
	if err != nil {
		return nil, err
	}
	candidates := make([]ProviderImportCandidate, 0, len(sources))
	for _, src := range sources {
		candidates = append(candidates, providerImportCandidateFromSource(src))
	}
	dedupeProviderImportCandidateIDs(candidates)
	return candidates, nil
}

func loadCCSwitchProviderSourcesFromRoot(root string) ([]ccSwitchProviderSource, error) {
	dbPath := filepath.Join(root, "cc-switch.db")
	if _, err := os.Stat(dbPath); err == nil {
		return loadCCSwitchProviderSourcesDB(dbPath)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	for _, name := range []string{"config.json", "config.json.migrated", "config.json.bak"} {
		sources, err := loadCCSwitchProviderLegacyConfig(filepath.Join(root, name))
		if err == nil && len(sources) > 0 {
			return sources, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("cc-switch provider import: no providers found in %s", root)
}

func loadCCSwitchProviderSourcesDB(path string) ([]ccSwitchProviderSource, error) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, fmt.Errorf("cc-switch provider import: sqlite3 not found to read %s", path)
	}
	query := `SELECT id, app_type, name, settings_config, provider_type FROM providers ORDER BY app_type, name, id`
	out, err := exec.Command(sqlite, "-readonly", "-json", path, query).Output()
	if err != nil {
		return nil, fmt.Errorf("cc-switch provider import: read providers from %s: %w", path, err)
	}
	var rows []ccSwitchProviderRow
	if strings.TrimSpace(string(out)) != "" {
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, fmt.Errorf("cc-switch provider import: parse provider rows: %w", err)
		}
	}
	endpoints := map[string][]string{}
	hasEndpointTable, err := ccSwitchSQLiteTableExists(sqlite, path, "provider_endpoints")
	if err != nil {
		return nil, err
	}
	if hasEndpointTable {
		endpointQuery := `SELECT provider_id, app_type, url FROM provider_endpoints ORDER BY provider_id, app_type, url`
		endpointOut, err := exec.Command(sqlite, "-readonly", "-json", path, endpointQuery).Output()
		if err != nil {
			return nil, fmt.Errorf("cc-switch provider import: read provider_endpoints from %s: %w", path, err)
		}
		var endpointRows []ccSwitchProviderEndpointRow
		if strings.TrimSpace(string(endpointOut)) != "" {
			if err := json.Unmarshal(endpointOut, &endpointRows); err != nil {
				return nil, fmt.Errorf("cc-switch provider import: parse endpoint rows: %w", err)
			}
		}
		for _, row := range endpointRows {
			key := ccSwitchProviderSourceKey(row.AppType, row.ProviderID)
			endpoints[key] = append(endpoints[key], row.URL)
		}
	}
	sources := make([]ccSwitchProviderSource, 0, len(rows))
	for _, row := range rows {
		src := ccSwitchProviderSource{
			ID:             strings.TrimSpace(row.ID),
			AppType:        strings.TrimSpace(row.AppType),
			Name:           strings.TrimSpace(row.Name),
			SettingsConfig: row.SettingsConfig,
			ProviderType:   strings.TrimSpace(row.ProviderType),
		}
		src.Endpoints = endpoints[ccSwitchProviderSourceKey(src.AppType, src.ID)]
		sources = append(sources, src)
	}
	return sources, nil
}

func ccSwitchSQLiteTableExists(sqlite, path, table string) (bool, error) {
	query := fmt.Sprintf(`SELECT name FROM sqlite_master WHERE type='table' AND name=%q`, table)
	out, err := exec.Command(sqlite, "-readonly", "-json", path, query).Output()
	if err != nil {
		return false, fmt.Errorf("cc-switch provider import: inspect %s: %w", path, err)
	}
	return strings.Contains(string(out), table), nil
}

func loadCCSwitchProviderLegacyConfig(path string) ([]ccSwitchProviderSource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("cc-switch provider import: parse %s: %w", path, err)
	}
	apps := make([]string, 0, len(doc))
	for app := range doc {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	var sources []ccSwitchProviderSource
	for _, app := range apps {
		var appDoc struct {
			Providers map[string]json.RawMessage `json:"providers"`
		}
		if err := json.Unmarshal(doc[app], &appDoc); err != nil || len(appDoc.Providers) == 0 {
			continue
		}
		keys := make([]string, 0, len(appDoc.Providers))
		for key := range appDoc.Providers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			src := legacyProviderSourceFromRaw(app, key, appDoc.Providers[key])
			if strings.TrimSpace(src.ID) == "" {
				src.ID = key
			}
			if strings.TrimSpace(src.Name) == "" {
				src.Name = key
			}
			sources = append(sources, src)
		}
	}
	return sources, nil
}

func legacyProviderSourceFromRaw(app, key string, raw json.RawMessage) ccSwitchProviderSource {
	src := ccSwitchProviderSource{ID: key, AppType: app, Name: key, SettingsConfig: string(raw)}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return src
	}
	src.ID = firstNonEmptyString(anyString(obj["id"]), anyString(obj["provider_id"]), anyString(obj["providerId"]), key)
	src.Name = firstNonEmptyString(anyString(obj["name"]), anyString(obj["display_name"]), anyString(obj["displayName"]), src.ID)
	src.ProviderType = firstNonEmptyString(anyString(obj["provider_type"]), anyString(obj["providerType"]), anyString(obj["type"]))
	if v, ok := obj["settings_config"]; ok {
		src.SettingsConfig = anyJSONOrString(v)
	} else if v, ok := obj["settingsConfig"]; ok {
		src.SettingsConfig = anyJSONOrString(v)
	} else if v, ok := obj["settings"]; ok {
		src.SettingsConfig = anyJSONOrString(v)
	}
	if urls := anyStringList(obj["endpoints"]); len(urls) > 0 {
		src.Endpoints = urls
	}
	if endpoint := firstNonEmptyString(anyString(obj["endpoint"]), anyString(obj["url"]), anyString(obj["base_url"]), anyString(obj["baseUrl"])); endpoint != "" {
		src.Endpoints = append(src.Endpoints, endpoint)
	}
	return src
}

func providerImportCandidateFromSource(src ccSwitchProviderSource) ProviderImportCandidate {
	settings := parseCCSwitchProviderSettings(src.SettingsConfig)
	name := firstNonEmptyString(src.Name, src.ID, src.AppType)
	c := ProviderImportCandidate{
		ID:       ccSwitchProviderSourceKey(src.AppType, src.ID),
		SourceID: strings.TrimSpace(src.ID),
		AppType:  strings.TrimSpace(src.AppType),
		Name:     name,
		Status:   ccSwitchProviderStatusReady,
	}
	baseURL := detectProviderBaseURL(src, settings)
	host := providerImportHost(baseURL)
	models := []string{}
	defaultModel := ""
	keyValue := ""
	apiKeyEnv := ""
	authScheme := ""

	if isGeminiProviderSource(src, settings) {
		c.Kind = "gemini"
		c.BaseURL = baseURL
		c.Host = host
		c.Status = ccSwitchProviderStatusUnsupported
		c.Reasons = append(c.Reasons, "gemini is not supported yet")
		return finishProviderImportCandidate(c)
	}

	if isOfficialDeepSeekHost(host) {
		c.Kind = "openai"
		c.BaseURL = "https://api.deepseek.com"
		c.Host = "api.deepseek.com"
		models = mergeModelLists([]string{"deepseek-v4-flash", "deepseek-v4-pro"}, openAICompatibleModels(settings))
		defaultModel = "deepseek-v4-flash"
		apiKeyEnv = "DEEPSEEK_API_KEY"
		keyValue = firstNonEmptyString(
			settings.stringValue("DEEPSEEK_API_KEY"),
			settings.stringValue("OPENAI_API_KEY"),
			settings.stringValue("ANTHROPIC_API_KEY"),
			settings.stringValue("ANTHROPIC_AUTH_TOKEN"),
			settings.stringValue("api_key"),
			settings.stringValue("apiKey"),
		)
		c.TargetName = "deepseek"
		c.Reasons = append(c.Reasons, "official deepseek")
	} else if isAnthropicProviderSource(src, settings) {
		c.Kind = "anthropic"
		c.BaseURL = cleanProviderBaseURL(baseURL)
		c.Host = host
		models = anthropicCompatibleModels(settings)
		defaultModel = firstKnownModel(settings.stringValue("ANTHROPIC_MODEL"), models, "")
		if apiKey := settings.stringValue("ANTHROPIC_API_KEY"); strings.TrimSpace(apiKey) != "" {
			keyValue = apiKey
			authScheme = "x-api-key"
		} else if token := settings.stringValue("ANTHROPIC_AUTH_TOKEN"); strings.TrimSpace(token) != "" {
			keyValue = token
			authScheme = "bearer"
		}
		apiKeyEnv = customProviderAPIKeyEnv(candidateProviderTargetName(name, src, host))
		c.TargetName = candidateProviderTargetName(name, src, host)
	} else if isOpenAIProviderSource(src, settings) {
		c.Kind = "openai"
		c.BaseURL = cleanProviderBaseURL(baseURL)
		c.Host = host
		models = openAICompatibleModels(settings)
		defaultModel = firstKnownModel(settings.stringValue("OPENAI_MODEL"), models, "")
		keyValue = firstNonEmptyString(settings.stringValue("OPENAI_API_KEY"), settings.stringValue("api_key"), settings.stringValue("apiKey"))
		apiKeyEnv = customProviderAPIKeyEnv(candidateProviderTargetName(name, src, host))
		c.TargetName = candidateProviderTargetName(name, src, host)
	} else {
		c.BaseURL = cleanProviderBaseURL(baseURL)
		c.Host = host
		c.Status = ccSwitchProviderStatusUnsupported
		c.Reasons = append(c.Reasons, "unknown provider protocol")
		return finishProviderImportCandidate(c)
	}

	c.Models = nonEmptyUniqueStrings(models)
	c.Default = firstKnownModel(defaultModel, c.Models, "")
	c.APIKeyEnv = apiKeyEnv
	c.AuthScheme = authScheme
	c.KeyPresent = strings.TrimSpace(keyValue) != ""
	c.keyValue = keyValue
	if c.TargetName == "" {
		c.TargetName = candidateProviderTargetName(name, src, host)
	}
	if c.APIKeyEnv == "" && c.TargetName != "deepseek" {
		c.APIKeyEnv = customProviderAPIKeyEnv(c.TargetName)
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		c.Status = ccSwitchProviderStatusInvalid
		c.Reasons = append(c.Reasons, "missing base url")
	}
	if len(c.Models) == 0 {
		c.Status = ccSwitchProviderStatusInvalid
		c.Reasons = append(c.Reasons, "missing model")
	}
	if c.Status == ccSwitchProviderStatusReady && !c.KeyPresent {
		c.Status = ccSwitchProviderStatusMissingKey
		c.Reasons = append(c.Reasons, "missing key")
	}
	return finishProviderImportCandidate(c)
}

func finishProviderImportCandidate(c ProviderImportCandidate) ProviderImportCandidate {
	if c.Status == "" {
		c.Status = ccSwitchProviderStatusReady
	}
	c.Importable = c.Status == ccSwitchProviderStatusReady
	c.Recommended = c.Importable
	if c.Importable {
		c.Reasons = append([]string{"recommended"}, c.Reasons...)
	}
	if len(c.Reasons) == 0 {
		c.Reasons = append(c.Reasons, c.Status)
	}
	c.Models = nonEmptyUniqueStrings(c.Models)
	if c.Default == "" && len(c.Models) > 0 {
		c.Default = c.Models[0]
	}
	return c
}

func ImportCCSwitchProviders(ids []string, replaceKeys bool) (ProviderImportResult, error) {
	candidates, err := LoadCCSwitchProviderCandidates()
	if err != nil {
		return ProviderImportResult{}, err
	}
	cfg, err := Load()
	if err != nil {
		return ProviderImportResult{}, err
	}
	result, err := importCCSwitchProvidersIntoConfig(cfg, candidates, ids, replaceKeys)
	if err != nil {
		return result, err
	}
	if result.Imported > 0 {
		if err := cfg.Save(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func ImportCCSwitchProvidersIntoConfig(cfg *Config, ids []string, replaceKeys bool) (ProviderImportResult, error) {
	candidates, err := LoadCCSwitchProviderCandidates()
	if err != nil {
		return ProviderImportResult{}, err
	}
	return importCCSwitchProvidersIntoConfig(cfg, candidates, ids, replaceKeys)
}

func importCCSwitchProvidersIntoConfig(cfg *Config, candidates []ProviderImportCandidate, ids []string, replaceKeys bool) (ProviderImportResult, error) {
	result := ProviderImportResult{Total: len(candidates)}
	selected := map[string]bool{}
	importAll := len(ids) == 0
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = true
		}
	}
	existingBefore := map[string]bool{}
	for _, p := range cfg.Providers {
		existingBefore[p.Name] = true
	}
	usedNames := map[string]bool{}
	for _, p := range cfg.Providers {
		usedNames[p.Name] = true
	}
	for _, candidate := range candidates {
		if !importAll && !selected[candidate.ID] {
			continue
		}
		if !candidate.Importable {
			result.Skipped++
			result.SkippedCandidates = append(result.SkippedCandidates, ProviderImportSkipped{
				ID: candidate.ID, Name: candidate.Name, Reason: strings.Join(candidate.Reasons, ", "),
			})
			continue
		}
		targetName := candidate.TargetName
		if targetName != "deepseek" {
			targetName = resolveCCSwitchCustomProviderName(cfg, candidate, usedNames)
			candidate.TargetName = targetName
			candidate.APIKeyEnv = customProviderAPIKeyEnv(targetName)
		}
		entry := providerEntryFromImportCandidate(cfg, candidate)
		if err := cfg.UpsertProvider(entry); err != nil {
			return result, err
		}
		usedNames[entry.Name] = true
		addDesktopProviderAccess(cfg, entry.Name)
		if existingBefore[entry.Name] {
			result.Updated++
		} else {
			result.Added++
			existingBefore[entry.Name] = true
		}
		result.Imported++
		if strings.TrimSpace(candidate.keyValue) == "" || strings.TrimSpace(candidate.APIKeyEnv) == "" {
			result.KeySkipped++
			continue
		}
		if existing, _, ok := storedCredentialValue(candidate.APIKeyEnv); ok && strings.TrimSpace(existing) != "" && !replaceKeys {
			result.KeySkipped++
			continue
		}
		if _, err := SetCredential(candidate.APIKeyEnv, candidate.keyValue); err != nil {
			return result, err
		}
		result.KeyImported++
	}
	return result, nil
}

func providerEntryFromImportCandidate(cfg *Config, c ProviderImportCandidate) ProviderEntry {
	if c.TargetName == "deepseek" {
		models := mergeModelLists([]string{"deepseek-v4-flash", "deepseek-v4-pro"}, c.Models)
		return ProviderEntry{
			Name:          "deepseek",
			Kind:          "openai",
			BaseURL:       "https://api.deepseek.com",
			Models:        models,
			Default:       firstKnownModel("deepseek-v4-flash", models, "deepseek-v4-flash"),
			APIKeyEnv:     "DEEPSEEK_API_KEY",
			BalanceURL:    "https://api.deepseek.com/user/balance",
			ContextWindow: 1_000_000,
			Prices:        deepSeekV4PricesForConfig(cfg),
		}
	}
	models := nonEmptyUniqueStrings(c.Models)
	return ProviderEntry{
		Name:       c.TargetName,
		Kind:       c.Kind,
		BaseURL:    c.BaseURL,
		Model:      firstKnownModel(c.Default, models, ""),
		Models:     models,
		Default:    firstKnownModel(c.Default, models, ""),
		APIKeyEnv:  c.APIKeyEnv,
		AuthScheme: c.AuthScheme,
	}
}

func addDesktopProviderAccess(cfg *Config, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for _, existing := range cfg.Desktop.ProviderAccess {
		if existing == name {
			return
		}
	}
	cfg.Desktop.ProviderAccess = append(cfg.Desktop.ProviderAccess, name)
}

func resolveCCSwitchCustomProviderName(cfg *Config, c ProviderImportCandidate, used map[string]bool) string {
	name := c.TargetName
	if name == "" {
		name = candidateProviderTargetName(c.Name, ccSwitchProviderSource{ID: c.SourceID, AppType: c.AppType}, c.Host)
	}
	if p, ok := cfg.Provider(name); ok && providerEntryCompatibleWithImport(*p, c) {
		return name
	}
	if !used[name] {
		return name
	}
	suffix := shortCCSwitchProviderID(firstNonEmptyString(c.SourceID, c.ID, c.Name, c.BaseURL))
	if suffix == "" {
		suffix = "import"
	}
	base := strings.TrimSuffix(name, "-"+suffix)
	for i := 0; ; i++ {
		candidate := base + "-" + suffix
		if i > 0 {
			candidate = fmt.Sprintf("%s-%s-%d", base, suffix, i+1)
		}
		if p, ok := cfg.Provider(candidate); ok && providerEntryCompatibleWithImport(*p, c) {
			return candidate
		}
		if !used[candidate] {
			return candidate
		}
	}
}

func providerEntryCompatibleWithImport(p ProviderEntry, c ProviderImportCandidate) bool {
	return strings.EqualFold(strings.TrimSpace(p.Kind), strings.TrimSpace(c.Kind)) &&
		canonicalProviderBaseURL(p.BaseURL) == canonicalProviderBaseURL(c.BaseURL)
}

func parseCCSwitchProviderSettings(raw string) ccSwitchProviderSettings {
	s := ccSwitchProviderSettings{
		env:    map[string]string{},
		auth:   map[string]string{},
		fields: map[string]string{},
		lists:  map[string][]string{},
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return s
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		s.config = raw
		return s
	}
	if str, ok := value.(string); ok {
		str = strings.TrimSpace(str)
		if strings.HasPrefix(str, "{") {
			return parseCCSwitchProviderSettings(str)
		}
		s.config = str
		return s
	}
	if obj, ok := value.(map[string]any); ok {
		if env, ok := obj["env"]; ok {
			s.env = collectStringMap(env)
		}
		if env, ok := obj["environment"]; ok {
			for k, v := range collectStringMap(env) {
				s.env[k] = v
			}
		}
		if auth, ok := obj["auth"]; ok {
			s.auth = collectStringMap(auth)
		}
		if configText := firstNonEmptyString(anyString(obj["config"]), anyString(obj["toml"])); configText != "" {
			s.config = configText
		}
		collectProviderSettings("", obj, &s)
	}
	return s
}

func collectProviderSettings(prefix string, value any, s *ccSwitchProviderSettings) {
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			key := strings.TrimSpace(k)
			if key == "" {
				continue
			}
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			collectProviderSettings(path, child, s)
		}
	case []any:
		if list := anyStringList(v); len(list) > 0 {
			s.lists[prefix] = list
			if last := lastPathSegment(prefix); last != "" {
				s.lists[last] = list
			}
		}
	case string:
		str := strings.TrimSpace(v)
		if str != "" {
			s.fields[prefix] = str
			if last := lastPathSegment(prefix); last != "" {
				s.fields[last] = str
			}
		}
	}
}

func (s ccSwitchProviderSettings) stringValue(keys ...string) string {
	for _, key := range keys {
		for _, source := range []map[string]string{s.env, s.auth, s.fields} {
			if value := lookupProviderSetting(source, key); value != "" {
				return value
			}
		}
	}
	return ""
}

func (s ccSwitchProviderSettings) stringList(keys ...string) []string {
	var out []string
	for _, key := range keys {
		for _, list := range []map[string][]string{s.lists} {
			if values := lookupProviderSettingList(list, key); len(values) > 0 {
				out = append(out, values...)
			}
		}
		if value := s.stringValue(key); value != "" {
			out = append(out, splitProviderModelList(value)...)
		}
	}
	return nonEmptyUniqueStrings(out)
}

func detectProviderBaseURL(src ccSwitchProviderSource, s ccSwitchProviderSettings) string {
	if base := anthropicBaseURL(s); base != "" {
		return base
	}
	if base := openAIBaseURL(s); base != "" {
		return base
	}
	if base := firstNonEmptyString(
		s.stringValue("DEEPSEEK_BASE_URL"),
		s.stringValue("base_url"),
		s.stringValue("baseUrl"),
		s.stringValue("url"),
	); base != "" {
		return base
	}
	for _, endpoint := range src.Endpoints {
		if strings.TrimSpace(endpoint) != "" {
			return endpoint
		}
	}
	return ""
}

func openAIBaseURL(s ccSwitchProviderSettings) string {
	if base := firstNonEmptyString(
		s.stringValue("OPENAI_BASE_URL"),
		s.stringValue("OPENAI_API_BASE"),
		s.stringValue("OPENAI_API_URL"),
	); base != "" {
		return base
	}
	cfg := parseCodexProviderConfig(s.config)
	if cfg.ModelProvider != "" {
		if providerConfig, ok := cfg.ModelProviders[cfg.ModelProvider]; ok {
			return providerConfig.BaseURL
		}
	}
	if len(cfg.ModelProviders) == 1 {
		for _, providerConfig := range cfg.ModelProviders {
			return providerConfig.BaseURL
		}
	}
	return ""
}

func anthropicBaseURL(s ccSwitchProviderSettings) string {
	return firstNonEmptyString(
		s.stringValue("ANTHROPIC_BASE_URL"),
		s.stringValue("ANTHROPIC_API_URL"),
	)
}

func openAICompatibleModels(s ccSwitchProviderSettings) []string {
	var out []string
	if model := s.stringValue("OPENAI_MODEL"); model != "" {
		out = append(out, model)
	}
	cfg := parseCodexProviderConfig(s.config)
	if cfg.Model != "" {
		out = append(out, cfg.Model)
	}
	out = append(out, s.stringList("models", "model", "OPENAI_MODELS")...)
	return nonEmptyUniqueStrings(out)
}

func anthropicCompatibleModels(s ccSwitchProviderSettings) []string {
	keys := []string{
		"ANTHROPIC_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_REASONING_MODEL",
		"ANTHROPIC_SMALL_FAST_MODEL",
	}
	var out []string
	for _, key := range keys {
		if model := s.stringValue(key); model != "" {
			out = append(out, model)
		}
	}
	out = append(out, s.stringList("models", "model", "ANTHROPIC_MODELS")...)
	return nonEmptyUniqueStrings(out)
}

func parseCodexProviderConfig(text string) codexProviderConfig {
	var cfg codexProviderConfig
	text = strings.TrimSpace(text)
	if text == "" {
		return cfg
	}
	_, _ = toml.Decode(text, &cfg)
	return cfg
}

func isGeminiProviderSource(src ccSwitchProviderSource, s ccSwitchProviderSettings) bool {
	app := strings.ToLower(strings.TrimSpace(src.AppType))
	pt := strings.ToLower(strings.TrimSpace(src.ProviderType))
	if strings.Contains(app, "gemini") || strings.Contains(pt, "gemini") {
		return true
	}
	return s.stringValue("GEMINI_API_KEY", "GOOGLE_API_KEY") != ""
}

func isAnthropicProviderSource(src ccSwitchProviderSource, s ccSwitchProviderSettings) bool {
	app := strings.ToLower(strings.TrimSpace(src.AppType))
	if app == "claude" || app == "claude-desktop" {
		return true
	}
	if anthropicBaseURL(s) != "" {
		return true
	}
	return s.stringValue("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL") != ""
}

func isOpenAIProviderSource(src ccSwitchProviderSource, s ccSwitchProviderSettings) bool {
	app := strings.ToLower(strings.TrimSpace(src.AppType))
	if app == "codex" {
		return true
	}
	if openAIBaseURL(s) != "" {
		return true
	}
	return s.stringValue("OPENAI_API_KEY", "OPENAI_MODEL") != ""
}

func candidateProviderTargetName(name string, src ccSwitchProviderSource, host string) string {
	slug := slugForProviderName(firstNonEmptyString(name, src.Name, host, src.ID, src.AppType))
	if slug == "" {
		slug = shortCCSwitchProviderID(firstNonEmptyString(src.ID, name, host, src.AppType))
	}
	if slug == "" {
		slug = "provider"
	}
	if !strings.HasPrefix(slug, "ccswitch-") {
		slug = "ccswitch-" + slug
	}
	return slug
}

func customProviderAPIKeyEnv(targetName string) string {
	slug := strings.TrimPrefix(strings.TrimSpace(targetName), "ccswitch-")
	if slug == "" {
		slug = targetName
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToUpper(slug) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	envSlug := strings.Trim(b.String(), "_")
	if envSlug == "" {
		envSlug = "PROVIDER"
	}
	return "REASONIX_CC_SWITCH_" + envSlug + "_API_KEY"
}

func slugForProviderName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func shortCCSwitchProviderID(value string) string {
	if slug := slugForProviderName(value); slug != "" {
		if len(slug) > 10 {
			return slug[:10]
		}
		return slug
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

func dedupeProviderImportCandidateIDs(candidates []ProviderImportCandidate) {
	seen := map[string]int{}
	for i := range candidates {
		id := candidates[i].ID
		if id == "" {
			id = fmt.Sprintf("%s:%d", candidates[i].AppType, i+1)
		}
		if n := seen[id]; n > 0 {
			seen[id] = n + 1
			candidates[i].ID = fmt.Sprintf("%s#%d", id, n+1)
		} else {
			seen[id] = 1
			candidates[i].ID = id
		}
	}
}

func cleanProviderBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func providerImportHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parse := raw
	if !strings.Contains(parse, "://") {
		parse = "https://" + parse
	}
	u, err := url.Parse(parse)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(u.Hostname()))
}

func canonicalProviderBaseURL(raw string) string {
	raw = cleanProviderBaseURL(raw)
	if raw == "" {
		return ""
	}
	parse := raw
	if !strings.Contains(parse, "://") {
		parse = "https://" + parse
	}
	u, err := url.Parse(parse)
	if err != nil || u.Hostname() == "" {
		return strings.ToLower(raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func isOfficialDeepSeekHost(host string) bool {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.") == "api.deepseek.com"
}

func ccSwitchProviderSourceKey(appType, id string) string {
	return strings.TrimSpace(appType) + ":" + strings.TrimSpace(id)
}

func lookupProviderSetting(values map[string]string, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	candidates := []string{key, strings.ToLower(key), strings.ToUpper(key)}
	for _, candidate := range candidates {
		if value := strings.TrimSpace(values[candidate]); value != "" {
			return value
		}
	}
	for k, value := range values {
		if strings.EqualFold(k, key) && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func lookupProviderSettingList(values map[string][]string, key string) []string {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	for k, value := range values {
		if strings.EqualFold(k, key) && len(value) > 0 {
			return value
		}
	}
	return nil
}

func collectStringMap(value any) map[string]string {
	out := map[string]string{}
	if obj, ok := value.(map[string]any); ok {
		for k, v := range obj {
			if s := anyString(v); s != "" {
				out[k] = s
			}
		}
	}
	return out
}

func anyString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func anyStringList(value any) []string {
	switch v := value.(type) {
	case []string:
		return nonEmptyUniqueStrings(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := anyString(item); s != "" {
				out = append(out, s)
			}
		}
		return nonEmptyUniqueStrings(out)
	case string:
		return splitProviderModelList(v)
	default:
		return nil
	}
}

func splitProviderModelList(value string) []string {
	return nonEmptyUniqueStrings(strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == '\n' || r == '\t'
	}))
}

func nonEmptyUniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func anyJSONOrString(value any) string {
	if s := anyString(value); s != "" {
		return s
	}
	b, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(b)
}

func lastPathSegment(path string) string {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
