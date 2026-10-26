package teamroster

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"reasonix/internal/base/fileutil"
)

// SecretFile holds the secrets the user gave the team. The host registers it as
// a secret path, so sandboxed commands and the read tools are denied it.
const SecretFile = "secrets.json"

// Bounds on a secret.
const (
	MinSecretBytes        = 4
	MaxSecretBytes        = 16 << 10
	MaxSecretPurposeRunes = 200
	MaxSecretRequests     = 32 // open at once, per agent
	maxSecretNameRunes    = 40
)

var secretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,39}$`)

// Secret is a value the user saved for agents to use without seeing it.
// Agents nil means every agent on the team may use it.
type Secret struct {
	Name        string    `json:"name"`
	Value       string    `json:"value"`
	Purpose     string    `json:"purpose,omitempty"`
	Agents      []string  `json:"agents,omitempty"`
	RequestedBy string    `json:"requestedBy,omitempty"`
	CreatedAt   time.Time `json:"createdAt,omitzero"`
	UpdatedAt   time.Time `json:"updatedAt,omitzero"`
}

// SecretRequest is an agent asking the user for a secret it does not have.
type SecretRequest struct {
	Name    string    `json:"name"`
	Purpose string    `json:"purpose,omitempty"`
	Agent   string    `json:"agent"`
	At      time.Time `json:"at,omitzero"`
}

// RetiredSecret is a value the user deleted or replaced. It reaches no command,
// and is kept only so the copies already in conversations stay masked.
type RetiredSecret struct {
	Name  string    `json:"name"`
	Value string    `json:"value"`
	At    time.Time `json:"at,omitzero"`
}

// SecretBook is the file's whole content.
type SecretBook struct {
	Secrets  []Secret        `json:"secrets"`
	Requests []SecretRequest `json:"requests"`
	Retired  []RetiredSecret `json:"retired,omitempty"`
}

// Retire keeps a value that no longer reaches commands, once per name.
func (b *SecretBook) Retire(name, value string, at time.Time) {
	if value == "" || slices.ContainsFunc(b.Retired, func(r RetiredSecret) bool { return r.Name == name && r.Value == value }) {
		return
	}
	b.Retired = append(b.Retired, RetiredSecret{Name: name, Value: value, At: at})
}

// NormalizeSecretName turns what an agent or the user called a secret into the
// suffix of the environment variable it reaches commands under: upper case,
// with anything else an underscore. A name that cannot start one is empty.
func NormalizeSecretName(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if utf8.RuneCountInString(out) > maxSecretNameRunes {
		out = string([]rune(out)[:maxSecretNameRunes])
	}
	if !secretName.MatchString(out) {
		return ""
	}
	return out
}

func (sc Secret) check() error {
	switch {
	case !secretName.MatchString(sc.Name):
		return fieldErr("name", ErrInvalid)
	case len(sc.Value) < MinSecretBytes:
		return fieldErr("value", ErrEmpty)
	case len(sc.Value) > MaxSecretBytes:
		return fieldErr("value", ErrTooLong)
	case utf8.RuneCountInString(sc.Purpose) > MaxSecretPurposeRunes:
		return fieldErr("purpose", ErrTooLong)
	}
	for _, a := range sc.Agents {
		if !agentID.MatchString(a) {
			return fieldErr("agents", ErrInvalid)
		}
	}
	return nil
}

// SecretPath is where the team's secrets are kept.
func (s Store) SecretPath() string { return filepath.Join(s.Dir, SecretFile) }

// Secrets returns the secrets the user saved and the requests still open.
func (s Store) Secrets() (SecretBook, error) {
	data, err := os.ReadFile(s.SecretPath())
	if errors.Is(err, fs.ErrNotExist) {
		return SecretBook{}, nil
	}
	if err != nil {
		return SecretBook{}, err
	}
	var book SecretBook
	if err := json.Unmarshal(data, &book); err != nil {
		return SecretBook{}, ErrCorrupt
	}
	return book, nil
}

// EditSecrets applies mutate under the lock and writes the book back, readable
// by the user alone, only if every secret still checks.
func (s Store) EditSecrets(ctx context.Context, mutate func(*SecretBook) error) error {
	return s.withLock(ctx, s.Dir, SecretFile, func() error {
		book, err := s.Secrets()
		if err != nil {
			return err
		}
		if err := mutate(&book); err != nil {
			return err
		}
		for _, sc := range book.Secrets {
			if err := sc.check(); err != nil {
				return err
			}
		}
		data, err := json.Marshal(book)
		if err != nil {
			return err
		}
		return fileutil.AtomicWriteFile(s.SecretPath(), data, 0o600)
	})
}
