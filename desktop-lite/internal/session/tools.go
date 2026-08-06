package session

import (
	"strings"

	"reasonix/internal/tool"
)

// ToolRegistryProvider is the optional capability a Conversation exposes when
// its tool set can be reshaped after boot. *control.Controller satisfies it;
// conversations that do not simply keep whatever tools boot gave them.
type ToolRegistryProvider interface {
	ToolRegistry() *tool.Registry
}

// DeferredPolicy decides which of a freshly built session's tools are held back
// from the provider until the model asks for them.
type DeferredPolicy struct {
	// Prefixes are the tool-name prefixes to defer. A nil slice uses
	// DefaultDeferredPrefixes; an explicitly empty slice defers nothing.
	Prefixes []string
}

// DefaultDeferredPrefixes holds back MCP tools and nothing else.
//
// MCP is where the cost actually is: every server contributes one JSON Schema
// per tool, and a well-equipped host carries a hundred or more. Skills look
// similar from the outside but are not — they reach the model through the
// single run_skill dispatcher against an index in the system prompt, so they
// already cost one schema in total. Deferring them would save nothing and would
// put an extra hop in front of the feature the lite shell most wants to be
// fast.
var DefaultDeferredPrefixes = []string{tool.MCPNamePrefix}

func (p DeferredPolicy) prefixes() []string {
	if p.Prefixes == nil {
		return DefaultDeferredPrefixes
	}
	return p.Prefixes
}

// wireDeferredTools holds back the policy's tools and installs the search tool
// that reaches them, returning how many it deferred.
//
// It runs between building a conversation and its first turn. That timing is
// the whole reason it can demote tools boot already registered: no request has
// gone out yet, so there is no cached prefix to invalidate. Doing the same work
// one turn later would throw that turn's cache away.
func wireDeferredTools(conv Conversation, policy DeferredPolicy) int {
	reg := registryOf(conv)
	if reg == nil {
		return 0
	}
	prefixes := policy.prefixes()
	if len(prefixes) == 0 {
		return 0
	}

	var targets []string
	for _, name := range reg.Names() {
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				targets = append(targets, name)
				break
			}
		}
	}
	deferred := reg.Defer(targets...)
	if len(deferred) == 0 {
		// Nothing was held back, so the search tool would be a schema in the
		// prefix with nothing to find.
		return 0
	}
	reg.Add(tool.NewSearchTool(reg))
	return len(deferred)
}

func registryOf(conv Conversation) *tool.Registry {
	provider, ok := conv.(ToolRegistryProvider)
	if !ok {
		return nil
	}
	return provider.ToolRegistry()
}
