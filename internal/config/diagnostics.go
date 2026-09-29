package config

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"reasonix/internal/secrets"
)

type configReadError struct {
	scope, source string
	err           error
}

func (e *configReadError) Error() string { return e.err.Error() }
func (e *configReadError) Unwrap() error { return e.err }

func configSourceError(path string, err error) error {
	scope := "project"
	if isUserConfigPath(path) {
		scope = "user"
	}
	return &configReadError{scope: scope, source: path, err: fmt.Errorf("config %s: %w", path, err)}
}

// LoadFailureDiagnostics is shared by read-only inspection and doctor when a
// loader cannot produce an effective config. Raw parse/error text stays local.
func LoadFailureDiagnostics(root string, err error) []Diagnostic {
	scope, source := "project", filepath.Join(root, "reasonix.toml")
	var readErr *configReadError
	if errors.As(err, &readErr) {
		scope, source = readErr.scope, readErr.source
	}
	c := Default()
	c.addDiagnostic(Diagnostic{Code: "configuration_unavailable", Scope: scope, Source: source, Severity: "warning", Status: "needs_attention", Summary: "Configuration could not be loaded.", Action: "open_config"}, fmt.Sprint(err))
	return c.DiagnosticGroups()
}

// Diagnostic describes configuration behavior without exporting command bodies
// or provider credentials. It is a projection, never an authorization record.
type Diagnostic struct {
	ID            string   `json:"id"`
	Code          string   `json:"code"`
	Scope         string   `json:"scope"`
	Source        string   `json:"source"`
	Field         string   `json:"field"`
	Severity      string   `json:"severity"`
	Status        string   `json:"status"`
	Summary       string   `json:"summary"`
	Action        string   `json:"action"`
	Count         int      `json:"count"`
	Values        []string `json:"values,omitempty"`
	legacyMessage string
	value         string
}

// Diagnostics owns its array, including when the result is empty.
func (c *Config) Diagnostics() []Diagnostic {
	if c == nil {
		return []Diagnostic{}
	}
	return append([]Diagnostic{}, c.diagnostics...)
}

// LoadWarnings returns a copy of the warning/error projection for older callers.
func (c *Config) LoadWarnings() []string {
	out := []string{}
	for _, d := range c.Diagnostics() {
		if d.Severity == "warning" || d.Severity == "error" {
			msg := d.legacyMessage
			if msg == "" {
				msg = d.Summary
			}
			out = append(out, msg)
		}
	}
	return out
}

// HasLoadWarnings reports whether the load used a degraded in-memory fallback.
func (c *Config) HasLoadWarnings() bool {
	return len(c.LoadWarnings()) > 0
}

// Diagnostic IDs are process-local opaque values. Their identities include
// project commands and error text, so an unkeyed digest in exports would let a
// reader confirm guessed secrets. Restarting the process only re-shows issues.
var diagnosticIDKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}()

func diagnosticDigest(parts ...string) string {
	mac := hmac.New(sha256.New, diagnosticIDKey)
	for _, part := range parts {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func (c *Config) addDiagnostic(d Diagnostic, identity string) {
	if c == nil {
		return
	}
	if d.Source != "" {
		if abs, err := filepath.Abs(d.Source); err == nil {
			d.Source = abs
		}
	}
	d.ID = diagnosticDigest(d.Scope, d.Source, d.Field, d.Code, identity)
	d.Count = 1
	if slices.ContainsFunc(c.diagnostics, func(old Diagnostic) bool { return old.ID == d.ID }) {
		return
	}
	c.diagnostics = append(c.diagnostics, d)
}

func (c *Config) warnConfig(code, scope, source, field, summary, detail string, causes ...error) {
	if len(causes) > 0 {
		var sourceErr *configReadError
		if errors.As(causes[0], &sourceErr) {
			scope, source = sourceErr.scope, sourceErr.source
		}
	}
	c.addDiagnostic(Diagnostic{Code: code, Scope: scope, Source: source, Field: field,
		Severity: "warning", Status: "needs_attention", Summary: summary, Action: "open_config", legacyMessage: detail}, detail)
}

func (c *Config) projectDiagnostic(key, value string, reason IgnoredProjectReason) {
	status, severity, action := "needs_attention", "warning", "open_config"
	summary := "A project setting was not applied."
	if key == "permissions.allow" || key == "sandbox.allow_write" || key == "sandbox.workspace_root" {
		status, severity, action = "on_demand", "info", "view_settings"
		summary = "Project permission declarations do not grant access; operations use the current approval policy."
	} else if reason == ProjectAwaitingApproval {
		status, action = "awaiting_approval", "review_programs"
		summary = "A project program is waiting for approval."
	}
	c.addDiagnostic(Diagnostic{Code: string(reason), Scope: "project", Source: filepath.Join(c.diagnosticRoot, "reasonix.toml"),
		Field: key, Severity: severity, Status: status, Summary: summary, Action: action,
		legacyMessage: fmt.Sprintf("project config sets %s; ignored: %s", key, ignoredProjectReasonText[reason]), value: value}, value)
}

// DiagnosticGroups bounds UI/export size without exposing the original values.
func (c *Config) DiagnosticGroups() []Diagnostic {
	return c.diagnosticGroups("")
}

func (c *Config) diagnosticGroups(detailID string) []Diagnostic {
	out := []Diagnostic{}
	diagnostics := c.Diagnostics()
	// Reordering equivalent declarations does not make a dismissed issue new.
	slices.SortFunc(diagnostics, func(a, b Diagnostic) int { return strings.Compare(a.ID, b.ID) })
	for _, d := range diagnostics {
		i := slices.IndexFunc(out, func(old Diagnostic) bool {
			return old.Code == d.Code && old.Source == d.Source && old.Field == d.Field && old.Status == d.Status && old.Scope == d.Scope
		})
		if i < 0 {
			out = append(out, d)
			continue
		}
		out[i].Count += d.Count
		out[i].ID = diagnosticDigest(out[i].ID, d.ID)
	}
	// Only an explicit detail request receives values. Exports and background
	// snapshots contain counts and safe summaries, never historical commands.
	for i := range out {
		if detailID == "" || out[i].ID != detailID {
			continue
		}
		out[i].Values = []string{}
		for _, d := range diagnostics {
			if d.Code == out[i].Code && d.Source == out[i].Source && d.Field == out[i].Field && d.Status == out[i].Status && d.Scope == out[i].Scope && d.value != "" {
				out[i].Values = append(out[i].Values, secrets.RedactCredentials(d.value))
			}
		}
	}
	return out
}
