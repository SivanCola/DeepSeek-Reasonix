package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/provider"
)

type CapabilityState string

const (
	CapabilitySupported   CapabilityState = "supported"
	CapabilityUnsupported CapabilityState = "unsupported"
	CapabilityUnknown     CapabilityState = "unknown"
)

var modelCapabilityCacheWriteMu sync.Mutex

type CapabilitySource string

const (
	CapabilitySourceOverride CapabilitySource = "override"
	CapabilitySourcePreset   CapabilitySource = "preset"
	CapabilitySourceLegacy   CapabilitySource = "legacy"
	CapabilitySourceAdapter  CapabilitySource = "adapter"
	CapabilitySourceCache    CapabilitySource = "cache"
	CapabilitySourceDefault  CapabilitySource = "adapter_default"
	CapabilitySourceUnknown  CapabilitySource = "unknown"
)

type ResolvedModelCapability struct {
	Model           string
	InputModalities []provider.ModelModality
	State           CapabilityState
	Source          CapabilitySource
	ModelInfo       provider.ModelInfo
}

type ModelCapabilityCacheFile struct {
	Version int                         `json:"version"`
	Entries []ModelCapabilityCacheEntry `json:"entries"`
}

type ModelCapabilityCacheEntry struct {
	ProviderFingerprint string                   `json:"providerFingerprint"`
	ModelID             string                   `json:"modelID"`
	InputModalities     []provider.ModelModality `json:"inputModalities"`
	Source              CapabilitySource         `json:"source"`
	FetchedAt           time.Time                `json:"fetchedAt"`
	ExpiresAt           time.Time                `json:"expiresAt"`
}

const (
	modelCapabilityCacheVersion  = 1
	modelCapabilityCacheTTL      = 24 * time.Hour
	modelCapabilityCacheMaxSize  = 2 << 20
	modelCapabilityCacheMaxItems = 4096
)

// ModelCapabilityResolver owns the single capability decision used by boot,
// controller, and settings. Dynamic entries are process-local until explicitly
// hydrated from the sidecar cache; user config remains the higher-priority
// source and is never rewritten by discovery.
type ModelCapabilityResolver struct {
	mu      sync.RWMutex
	entries map[string]ModelCapabilityCacheEntry
	path    string
}

func NewModelCapabilityResolver() *ModelCapabilityResolver {
	r := &ModelCapabilityResolver{entries: map[string]ModelCapabilityCacheEntry{}}
	if dir := CacheDir(); dir != "" {
		r.path = filepath.Join(dir, "model-capabilities-v1.json")
		r.load()
	}
	return r
}

func (r *ModelCapabilityResolver) Resolve(entry *ProviderEntry) ResolvedModelCapability {
	if entry == nil {
		return ResolvedModelCapability{State: CapabilityUnknown, Source: CapabilitySourceUnknown}
	}
	model := strings.TrimSpace(entry.Model)
	if model == "" {
		return ResolvedModelCapability{State: CapabilityUnknown, Source: CapabilitySourceUnknown}
	}
	if entry.visionOverride != nil {
		return capabilityFromBool(model, *entry.visionOverride, CapabilitySourceOverride)
	}
	if info, ok := presetModelInfo(entry, model); ok {
		resolved := capabilityFromModalities(model, info.InputModalities, CapabilitySourcePreset)
		resolved.ModelInfo = info
		return resolved
	}
	if entry.Vision {
		return capabilityFromBool(model, true, CapabilitySourceLegacy)
	}
	if entry.HasVisionModel(model) {
		return capabilityFromBool(model, true, CapabilitySourceLegacy)
	}
	if info, ok := provider.PiCatalogModelInfoForProvider(entry.Name, entry.Kind, entry.BaseURL, model); ok {
		resolved := capabilityFromModalities(model, info.InputModalities, CapabilitySourceAdapter)
		resolved.ModelInfo = info
		return resolved
	}
	if info, ok := provider.BuiltinModelInfo(entry.Kind, entry.BaseURL, model); ok {
		resolved := capabilityFromModalities(model, info.InputModalities, CapabilitySourceAdapter)
		resolved.ModelInfo = info
		return resolved
	}
	if r != nil {
		key := r.entryKey(entry, model)
		r.mu.RLock()
		cached, ok := r.entries[key]
		r.mu.RUnlock()
		if ok && time.Now().Before(cached.ExpiresAt) {
			return capabilityFromModalities(model, cached.InputModalities, cached.Source)
		}
	}
	// Match the Harness adapter contract: an exact model resolved by a generic
	// adapter is explicitly text-only when no positive declaration exists.
	return capabilityFromBool(model, false, CapabilitySourceDefault)
}

// presetModelInfo turns the repository's curated provider templates into a
// local model catalog. It only applies to an untouched preset identity; an
// explicitly edited vision list remains a user-owned legacy override.
func presetModelInfo(entry *ProviderEntry, model string) (provider.ModelInfo, bool) {
	if entry == nil || strings.TrimSpace(entry.PresetID) == "" {
		return provider.ModelInfo{}, false
	}
	preset, ok := CuratedProviderPreset(entry.PresetID)
	if !ok {
		return provider.ModelInfo{}, false
	}
	for _, candidate := range preset.Entries {
		if candidate.Name != entry.Name || candidate.Kind != entry.Kind || candidate.BaseURL != entry.BaseURL || !candidate.HasModel(model) {
			continue
		}
		if !stringSlicesEqual(candidate.VisionModels, entry.VisionModels) {
			return provider.ModelInfo{}, false
		}
		modalities := []provider.ModelModality{provider.ModalityText}
		if candidate.HasVisionModel(model) {
			modalities = append(modalities, provider.ModalityImage)
		}
		return provider.ModelInfo{ID: model, Name: model, InputModalities: modalities}, true
	}
	return provider.ModelInfo{}, false
}

func capabilityFromBool(model string, vision bool, source CapabilitySource) ResolvedModelCapability {
	if vision {
		return capabilityFromModalities(model, []provider.ModelModality{provider.ModalityText, provider.ModalityImage}, source)
	}
	return capabilityFromModalities(model, []provider.ModelModality{provider.ModalityText}, source)
}

func capabilityFromModalities(model string, modalities []provider.ModelModality, source CapabilitySource) ResolvedModelCapability {
	copyModalities := append([]provider.ModelModality(nil), modalities...)
	state := CapabilityUnsupported
	if slices.Contains(copyModalities, provider.ModalityImage) {
		state = CapabilitySupported
	}
	if modalities == nil {
		state = CapabilityUnknown
	}
	return ResolvedModelCapability{Model: model, InputModalities: copyModalities, State: state, Source: source}
}

// PutCatalog stores adapter results for one provider identity and persists a
// disposable cache. Invalid entries are ignored rather than enabling images.
func (r *ModelCapabilityResolver) PutCatalog(entry ProviderEntry, catalog []provider.ModelInfo) {
	if r == nil {
		return
	}
	now := time.Now()
	r.mu.Lock()
	for _, model := range catalog {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		modalities := normalizeInputModalities(model.InputModalities)
		if modalities == nil {
			modalities = []provider.ModelModality{provider.ModalityText}
		}
		key := r.entryKey(&entry, id)
		r.entries[key] = ModelCapabilityCacheEntry{
			ProviderFingerprint: r.providerFingerprint(entry),
			ModelID:             id,
			InputModalities:     modalities,
			Source:              CapabilitySourceAdapter,
			FetchedAt:           now,
			ExpiresAt:           now.Add(modelCapabilityCacheTTL),
		}
	}
	r.mu.Unlock()
	r.persist()
}

func normalizeInputModalities(values []provider.ModelModality) []provider.ModelModality {
	if values == nil {
		return nil
	}
	seen := map[provider.ModelModality]bool{}
	out := make([]provider.ModelModality, 0, len(values))
	for _, value := range values {
		value = provider.ModelModality(strings.ToLower(strings.TrimSpace(string(value))))
		if value != provider.ModalityText && value != provider.ModalityImage {
			return nil
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (r *ModelCapabilityResolver) entryKey(entry *ProviderEntry, model string) string {
	return r.providerFingerprint(*entry) + "\x00" + strings.TrimSpace(model)
}

func (r *ModelCapabilityResolver) providerFingerprint(entry ProviderEntry) string {
	h := hmac.New(sha256.New, []byte("reasonix-model-capabilities-cache-v1"))
	for _, value := range []string{
		"reasonix-model-capabilities-v1", entry.Name, entry.Kind, entry.BaseURL,
		entry.ModelsURL, entry.APIKeyEnv, fmt.Sprintf("%t", entry.AuthHeader), fmt.Sprintf("%t", entry.NoProxy),
		CredentialStoreRevision(),
	} {
		_, _ = fmt.Fprintf(h, "%d:", len(value))
		_, _ = h.Write([]byte(value))
	}
	type headerPair struct{ key, value string }
	headers := make([]headerPair, 0, len(entry.Headers))
	for key, value := range entry.Headers {
		headers = append(headers, headerPair{key: strings.ToLower(strings.TrimSpace(key)), value: value})
	}
	sort.Slice(headers, func(i, j int) bool { return headers[i].key < headers[j].key })
	for _, header := range headers {
		key, value := header.key, header.value
		_, _ = fmt.Fprintf(h, "%d:", len(key))
		_, _ = h.Write([]byte(key))
		_, _ = fmt.Fprintf(h, "%d:", len(value))
		_, _ = h.Write([]byte(value))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (r *ModelCapabilityResolver) load() {
	if r.path == "" {
		return
	}
	file, ok := readModelCapabilityCacheFile(r.path)
	if !ok {
		return
	}
	now := time.Now()
	for _, entry := range file.Entries {
		if entry.ProviderFingerprint == "" || entry.ModelID == "" || !now.Before(entry.ExpiresAt) {
			continue
		}
		if normalized := normalizeInputModalities(entry.InputModalities); normalized != nil {
			entry.InputModalities = normalized
			entry.Source = CapabilitySourceCache
			r.entries[entry.ProviderFingerprint+"\x00"+entry.ModelID] = entry
		}
	}
}

func (r *ModelCapabilityResolver) persist() {
	if r == nil || r.path == "" {
		return
	}
	modelCapabilityCacheWriteMu.Lock()
	defer modelCapabilityCacheWriteMu.Unlock()
	r.mu.RLock()
	entries := make([]ModelCapabilityCacheEntry, 0, len(r.entries))
	now := time.Now()
	for _, entry := range r.entries {
		if now.Before(entry.ExpiresAt) {
			entry.InputModalities = append([]provider.ModelModality(nil), entry.InputModalities...)
			entries = append(entries, entry)
		}
	}
	r.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].FetchedAt.After(entries[j].FetchedAt) })
	if len(entries) > modelCapabilityCacheMaxItems {
		entries = entries[:modelCapabilityCacheMaxItems]
	}
	file := ModelCapabilityCacheFile{Version: modelCapabilityCacheVersion, Entries: entries}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	dir := filepath.Dir(r.path)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	release, err := acquireCapabilityFileLock(r.path+".lock", 2*time.Second)
	if err != nil {
		return
	}
	defer release()
	// Merge with the latest on-disk snapshot after taking the cross-process
	// lock. Separate settings refreshes must not erase one another's entries.
	if existing, ok := readModelCapabilityCacheFile(r.path); ok {
		seen := make(map[string]bool, len(entries))
		for _, entry := range entries {
			seen[entry.ProviderFingerprint+"\x00"+entry.ModelID] = true
		}
		for _, entry := range existing.Entries {
			key := entry.ProviderFingerprint + "\x00" + entry.ModelID
			if !seen[key] && time.Now().Before(entry.ExpiresAt) {
				entries = append(entries, entry)
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].FetchedAt.After(entries[j].FetchedAt) })
		if len(entries) > modelCapabilityCacheMaxItems {
			entries = entries[:modelCapabilityCacheMaxItems]
		}
		file = ModelCapabilityCacheFile{Version: modelCapabilityCacheVersion, Entries: entries}
		data, err = json.MarshalIndent(file, "", "  ")
		if err != nil {
			return
		}
	}
	tmp, err := os.CreateTemp(dir, ".model-capabilities-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		if len(data) <= modelCapabilityCacheMaxSize {
			_ = os.Rename(tmpName, r.path)
		}
	}
}

func readModelCapabilityCacheFile(path string) (ModelCapabilityCacheFile, bool) {
	fileHandle, err := os.Open(path)
	if err != nil {
		return ModelCapabilityCacheFile{}, false
	}
	defer fileHandle.Close()
	data, err := io.ReadAll(io.LimitReader(fileHandle, modelCapabilityCacheMaxSize+1))
	if err != nil || len(data) > modelCapabilityCacheMaxSize {
		return ModelCapabilityCacheFile{}, false
	}
	var file ModelCapabilityCacheFile
	if json.Unmarshal(data, &file) != nil || file.Version != modelCapabilityCacheVersion {
		return ModelCapabilityCacheFile{}, false
	}
	return file, true
}

func acquireCapabilityFileLock(path string, wait time.Duration) (func(), error) {
	deadline := time.Now().Add(wait)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
