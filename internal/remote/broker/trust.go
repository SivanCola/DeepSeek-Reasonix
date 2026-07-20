package broker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
)

// TrustStore persists RemoteProviderTrust records under the Reasonix home.
type TrustStore struct {
	Dir string // default: <Reasonix home>/remote-provider-trust
}

// DefaultTrustStore returns the user-global trust store.
func DefaultTrustStore() TrustStore {
	base := config.MemoryUserDir()
	if base == "" {
		return TrustStore{}
	}
	return TrustStore{Dir: filepath.Join(base, "remote-provider-trust")}
}

func (s TrustStore) path(hostID, fingerprint string) string {
	if s.Dir == "" {
		return ""
	}
	key := FingerprintHash(hostID + "\x00" + fingerprint)
	return filepath.Join(s.Dir, key+".json")
}

// Get loads a trust record. ok is false when missing.
func (s TrustStore) Get(hostID, fingerprint string) (TrustRecord, bool, error) {
	path := s.path(hostID, fingerprint)
	if path == "" {
		return TrustRecord{}, false, fmt.Errorf("trust store path unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return TrustRecord{}, false, nil
		}
		return TrustRecord{}, false, err
	}
	var rec TrustRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return TrustRecord{}, false, err
	}
	return rec, true, nil
}

// Put writes a trust record atomically.
func (s TrustStore) Put(rec TrustRecord) error {
	if strings.TrimSpace(rec.HostID) == "" || strings.TrimSpace(rec.HostKeyFingerprint) == "" {
		return fmt.Errorf("trust record requires hostId and fingerprint")
	}
	if rec.ApprovedAt.IsZero() {
		rec.ApprovedAt = time.Now().UTC()
	}
	path := s.path(rec.HostID, rec.HostKeyFingerprint)
	if path == "" {
		return fmt.Errorf("trust store path unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, data, 0o600)
}

// Allows reports whether ref is already authorized for the host/fingerprint pair.
func (s TrustStore) Allows(hostID, fingerprint, ref string) bool {
	rec, ok, err := s.Get(hostID, fingerprint)
	if err != nil || !ok {
		return false
	}
	ref = strings.TrimSpace(ref)
	for _, allowed := range rec.AllowedProviderRefs {
		if allowed == ref {
			return true
		}
	}
	return false
}

// AuthorizeAll writes or updates a trust record allowing the given provider refs.
// New providers are merged; the fingerprint binding is authoritative.
func (s TrustStore) AuthorizeAll(hostID, algo, fingerprint string, refs []string) error {
	rec, ok, err := s.Get(hostID, fingerprint)
	if err != nil {
		return err
	}
	if !ok {
		rec = TrustRecord{
			HostID:             hostID,
			HostKeyAlgorithm:   algo,
			HostKeyFingerprint: fingerprint,
		}
	}
	// Fingerprint change is a different path key — callers must re-prompt.
	if rec.HostKeyFingerprint != "" && rec.HostKeyFingerprint != fingerprint {
		return fmt.Errorf("host key fingerprint changed; re-authorization required")
	}
	rec.HostKeyAlgorithm = algo
	rec.HostKeyFingerprint = fingerprint
	rec.ApprovedAt = time.Now().UTC()
	seen := map[string]struct{}{}
	merged := make([]string, 0, len(rec.AllowedProviderRefs)+len(refs))
	for _, r := range rec.AllowedProviderRefs {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		merged = append(merged, r)
	}
	for _, r := range refs {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		merged = append(merged, r)
	}
	rec.AllowedProviderRefs = merged
	return s.Put(rec)
}

// MissingRefs returns provider refs not yet authorized.
func (s TrustStore) MissingRefs(hostID, fingerprint string, refs []string) []string {
	var missing []string
	for _, r := range refs {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !s.Allows(hostID, fingerprint, r) {
			missing = append(missing, r)
		}
	}
	return missing
}
