package evidence

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// RiskLevel classifies a post-mutation change set for adaptive review.
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// highRiskPathHints elevate ordinary production edits to High when the path
// touches auth, crypto, networking, providers, plugins, sandbox, config,
// migrations, persistence, or concurrency.
var highRiskPathHints = []string{
	"auth", "permission", "secret", "credential", "token", "password", "oauth",
	"crypto", "encrypt", "decrypt", "tls", "ssl", "keyring",
	"network", "proxy", "http", "websocket", "provider",
	"plugin", "mcp", "tool", "schema", "sandbox",
	"config", "migrate", "migration", "persist", "store", "database", "db",
	"concurrent", "mutex", "race", "lock", "atomic",
}

// highRiskToolHints elevate opaque or privileged mutation surfaces.
var highRiskToolHints = []string{
	"mcp__", "install_source", "install_skill", "plugin",
}

// ClassifyMutationRisk scores the change set after the latest mutation.
// Low: docs/tests/i18n/pure presentation only, with no opaque writes.
// Medium: ordinary production code or limited multi-file edits.
// High: security-sensitive surfaces, opaque mutations, or 10+ paths.
func ClassifyMutationRisk(receipts []Receipt, after int) RiskLevel {
	start := max(after+1, 0)
	var paths []string
	seen := map[string]bool{}
	opaque := false
	hasProd := false
	onlyLow := true

	// Include the mutation receipt itself.
	if after >= 0 && after < len(receipts) {
		r := receipts[after]
		if r.Success && r.Mutation {
			if len(r.Paths) == 0 && !memoryOnlyMutation(r.ToolName) {
				opaque = true
			}
			for _, p := range r.Paths {
				if !seen[p] {
					seen[p] = true
					paths = append(paths, p)
				}
			}
			if toolLooksHighRisk(r.ToolName) {
				return RiskHigh
			}
		}
	}
	for i := start; i < len(receipts); i++ {
		r := receipts[i]
		if !r.Success || !r.Mutation {
			continue
		}
		if len(r.Paths) == 0 && !memoryOnlyMutation(r.ToolName) {
			opaque = true
		}
		if toolLooksHighRisk(r.ToolName) {
			return RiskHigh
		}
		for _, p := range r.Paths {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	if opaque {
		return RiskHigh
	}
	if len(paths) == 0 {
		return RiskLow
	}
	if len(paths) >= 10 {
		return RiskHigh
	}
	for _, p := range paths {
		if pathLooksHighRisk(p) {
			return RiskHigh
		}
		if !pathLooksLowRisk(p) {
			onlyLow = false
			hasProd = true
		}
	}
	if onlyLow && !hasProd {
		return RiskLow
	}
	return RiskMedium
}

// ClassifyToolCallMutationRisk projects the risk of a concrete tool call
// before it is allowed to mutate state. It uses the same receipt/path rules as
// post-mutation classification, but the projected receipt is never recorded:
// callers can ratchet host policy before permission or execution without an
// auxiliary model request or a false success in the evidence ledger.
func ClassifyToolCallMutationRisk(toolName string, args json.RawMessage, readOnly bool) RiskLevel {
	if readOnly {
		return RiskLow
	}
	receipt := ReceiptFromToolCall(toolName, args, true, readOnly)
	if !receipt.Mutation {
		return RiskLow
	}
	return ClassifyMutationRisk([]Receipt{receipt}, 0)
}

// MutationRiskAfter classifies risk from the ledger starting at one mutation.
func (l *Ledger) MutationRiskAfter(after int) RiskLevel {
	if l == nil {
		return RiskLow
	}
	l.mu.Lock()
	receipts := append([]Receipt(nil), l.receipts...)
	l.mu.Unlock()
	return ClassifyMutationRisk(receipts, after)
}

// MutationRisk classifies all successful mutations in the current ledger. Risk
// ratchets must use the complete turn scope: a later low-risk edit must never
// hide an earlier security-sensitive or opaque mutation.
func (l *Ledger) MutationRisk() RiskLevel {
	return l.MutationRiskAfter(-1)
}

// PathsSince returns distinct paths from successful mutation/write receipts at
// or after the given index (inclusive of the mutation itself when after >= 0).
func (l *Ledger) PathsSince(after int) []string {
	if l == nil {
		return nil
	}
	start := max(after, 0)
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for i := start; i < len(l.receipts); i++ {
		r := l.receipts[i]
		if !r.Success || (!r.Mutation && !r.Write) {
			continue
		}
		for _, p := range r.Paths {
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func pathLooksHighRisk(path string) bool {
	lower := strings.ToLower(riskRelevantPath(path))
	base := strings.ToLower(filepath.Base(path))
	for _, hint := range highRiskPathHints {
		if strings.Contains(lower, hint) || strings.Contains(base, hint) {
			return true
		}
	}
	return false
}

// riskRelevantPath keeps relative paths intact and bounds absolute paths to
// their semantic tail. Absolute workspace/temp ancestors are deployment
// details, not change scope: a checkout named "toolbox" must not make every
// edit high-risk. Three tail components still retain conventional surfaces
// such as internal/auth/session.go and db/migrations/001.sql.
func riskRelevantPath(path string) string {
	normalized := strings.ReplaceAll(filepath.ToSlash(strings.TrimSpace(path)), `\`, "/")
	abs := strings.HasPrefix(normalized, "/") || (len(normalized) >= 3 && normalized[1] == ':' && normalized[2] == '/')
	if !abs {
		return normalized
	}
	parts := strings.FieldsFunc(normalized, func(r rune) bool { return r == '/' })
	if len(parts) >= 3 && strings.HasPrefix(strings.ToLower(parts[len(parts)-3]), "test") && allDecimal(parts[len(parts)-2]) {
		return parts[len(parts)-1]
	}
	if len(parts) > 3 {
		parts = parts[len(parts)-3:]
	}
	return strings.Join(parts, "/")
}

func allDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func pathLooksLowRisk(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	if strings.HasSuffix(lower, "_test.go") || strings.HasSuffix(lower, "_test.ts") ||
		strings.HasSuffix(lower, ".test.ts") || strings.HasSuffix(lower, ".test.tsx") ||
		strings.HasSuffix(lower, "_spec.ts") || strings.HasSuffix(lower, ".spec.ts") {
		return true
	}
	if strings.Contains(lower, "/testdata/") || strings.Contains(lower, "/__tests__/") ||
		strings.Contains(lower, "/fixtures/") {
		return true
	}
	switch {
	case strings.HasSuffix(base, ".md"), strings.HasSuffix(base, ".mdx"),
		strings.HasSuffix(base, ".txt"), strings.HasSuffix(base, ".rst"):
		return true
	case strings.Contains(lower, "/docs/"), strings.Contains(lower, "/locales/"),
		strings.Contains(lower, "/i18n/"), strings.HasPrefix(base, "readme"):
		return true
	case strings.HasSuffix(base, ".css") && !strings.Contains(lower, "sandbox"):
		// Pure presentation styles are low risk unless mixed with other paths.
		return true
	}
	return false
}

func memoryOnlyMutation(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "remember", "forget":
		return true
	default:
		return false
	}
}

func toolLooksHighRisk(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, hint := range highRiskToolHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}
