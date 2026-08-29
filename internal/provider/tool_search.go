package provider

import (
	"net/url"
	"strings"
)

// ToolSearch is a request-level native tool-search experiment. Unsupported
// adapters must not serialize it.
type ToolSearch struct {
	Enabled bool
}

// nativeToolSearchPreview is compiled off for 1.33.0. First-party OpenAI
// Responses and Anthropic may enable it later after the preview experiment.
var nativeToolSearchPreview = false

func NativeToolSearchPreviewEnabled() bool { return nativeToolSearchPreview }

func SetNativeToolSearchPreviewForTest(enabled bool) func() {
	prev := nativeToolSearchPreview
	nativeToolSearchPreview = enabled
	return func() { nativeToolSearchPreview = prev }
}

// NativeToolSearchSupported reports first-party OpenAI Responses or Anthropic.
// DeepSeek, DashScope, and OpenAI-compatible gateways stay on the fixed proxy.
func NativeToolSearchSupported(p Provider) bool {
	if p == nil {
		return false
	}
	return nativeToolSearchSupportedName(p.Name())
}

func nativeToolSearchSupportedName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(name, "deepseek"), strings.Contains(name, "dashscope"),
		strings.Contains(name, "qwen"), strings.Contains(name, "compatible"):
		return false
	case name == "openai" || strings.HasPrefix(name, "openai-") || name == "responses":
		return true
	case name == "anthropic" || strings.HasPrefix(name, "anthropic"):
		return true
	}
	return false
}

func NativeToolSearchEnabled(p Provider) bool {
	return nativeToolSearchPreview && NativeToolSearchSupported(p)
}

// ApplyNativeToolSearch marks extra MCP schemas deferred when the preview is
// active. When disabled it returns visible unchanged so the cache prefix is
// byte-identical to 1.33.0.
func ApplyNativeToolSearch(visible, extra []ToolSchema, p Provider) []ToolSchema {
	if !NativeToolSearchEnabled(p) || len(extra) == 0 {
		return visible
	}
	out := append([]ToolSchema(nil), visible...)
	seen := map[string]bool{}
	for _, schema := range visible {
		seen[schema.Name] = true
	}
	for _, schema := range extra {
		if seen[schema.Name] {
			continue
		}
		schema.Deferred = true
		schema.Strict = true
		out = append(out, schema)
		seen[schema.Name] = true
	}
	return out
}

func IsFirstPartyOpenAI(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.openai.com" || strings.HasSuffix(host, ".openai.com")
}

func IsFirstPartyAnthropic(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.anthropic.com" || strings.HasSuffix(host, ".anthropic.com")
}
