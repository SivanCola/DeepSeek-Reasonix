package control

import (
	"context"
	"fmt"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/plugin"
	"reasonix/internal/tool"
)

// mcpManager owns the session's live tool/plugin surface: the MCP plugin Host
// (live server connections), tool registries that receive mutations, and the
// session-scoped context a hot-added stdio server binds its subprocess to.
//
// MCP tool mutations always go to RegistryBundle.Execution, and are mirrored to
// Legacy for historical provider-visible MCP on legacy sessions. They NEVER
// write CapabilityClassic/CapabilityHashline — those surfaces stay schema-stable.
//
// mu guards host creation and pointer reads. Registry mutations use each
// registry's own lock. Host network/subprocess I/O runs off mu.
type mcpManager struct {
	mu        sync.Mutex
	host      *plugin.Host
	reg       *tool.Registry // fallback single-registry path (tests / no bundle)
	bundle    *agent.RegistryBundle
	pluginCtx context.Context
}

func newMcpManager(host *plugin.Host, reg *tool.Registry, pluginCtx context.Context) mcpManager {
	return mcpManager{host: host, reg: reg, pluginCtx: pluginCtx}
}

// setBundle installs the boot RegistryBundle used for MCP mutation routing.
func (m *mcpManager) setBundle(b *agent.RegistryBundle) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bundle = b
}

// hostRef returns the live plugin host (nil until one is injected or lazily
// created), for the SessionAPI Host() accessor and the nil-safe read wrappers.
func (m *mcpManager) hostRef() *plugin.Host {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.host
}

// connectSpec connects (or attaches to an already-connected) MCP server and
// registers its tools on MCP write targets (Execution + Legacy), replacing any
// prior tools under the same prefix. Returns the tool count.
func (m *mcpManager) connectSpec(s plugin.Spec) (int, error) {
	m.mu.Lock()
	if m.host == nil {
		m.host = plugin.NewHost()
	}
	host, ctx := m.host, m.pluginCtx
	m.mu.Unlock()

	tools, err := host.Add(ctx, s)
	if err != nil {
		if !plugin.IsServerAlreadyConnected(err) {
			return 0, err
		}
		toolsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tools, err = host.ToolsFor(toolsCtx, s.Name)
		if err != nil {
			return 0, err
		}
	}
	prefix := plugin.ToolPrefix(s.Name)
	for _, reg := range m.mcpWriteTargets() {
		reg.ResumePrefix(prefix)
		reg.RemovePrefix(prefix)
		for _, t := range tools {
			reg.Add(t)
		}
	}
	return len(tools), nil
}

// disconnect drops a live server and its tools from MCP write targets.
func (m *mcpManager) disconnect(name string) bool {
	host := m.hostRef()
	if host == nil {
		return false
	}
	prefix, ok := host.Remove(name)
	if ok {
		for _, reg := range m.mcpWriteTargets() {
			reg.RemovePrefix(prefix)
		}
	}
	return ok
}

// removeToolPrefix drops a server's tools from MCP write targets without
// touching the host — the placeholder / not-connected path.
func (m *mcpManager) removeToolPrefix(name string) int {
	prefix := plugin.ToolPrefix(name)
	n := 0
	for _, reg := range m.mcpWriteTargets() {
		n += reg.RemovePrefix(prefix)
	}
	return n
}

// suspendToolPrefix hides a server's tools on MCP write targets while a shared
// host keeps the client alive for sibling sessions. Suspend blocks later Add
// with the same prefix on those registries (including late tools).
func (m *mcpManager) suspendToolPrefix(name string) bool {
	prefix := plugin.ToolPrefix(name)
	ok := false
	for _, reg := range m.mcpWriteTargets() {
		if reg.SuspendPrefix(prefix) > 0 {
			ok = true
		}
	}
	// Even if no tools were present, mark suspended so late Adds are blocked.
	if !ok {
		for _, reg := range m.mcpWriteTargets() {
			reg.SuspendPrefix(prefix)
			ok = true
		}
	}
	return ok
}

// registerTool publishes a dynamically rebuilt built-in (for example
// slash_command) to Execution + Legacy only. Capability provider surfaces stay
// fixed even when command files change during a session.
func (m *mcpManager) registerTool(t tool.Tool) {
	for _, reg := range m.mcpWriteTargets() {
		reg.Add(t)
	}
}

// registry returns a registry suitable for provider-facing lookups when no
// bundle is present. Prefer Controller.providerRegistry() when a bundle exists.
func (m *mcpManager) registry() *tool.Registry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reg
}

// executionRegistry returns the bundle execution registry (MCP tool home), or
// the single fallback registry.
func (m *mcpManager) executionRegistry() *tool.Registry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bundle != nil {
		if exec := m.bundle.ExecutionRegistry(); exec != nil {
			return exec
		}
	}
	return m.reg
}

// mcpWriteTargets: Execution always; Legacy when distinct. Never capability surfaces.
func (m *mcpManager) mcpWriteTargets() []*tool.Registry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bundle != nil {
		return m.bundle.MCPWriteTargets()
	}
	if m.reg != nil {
		return []*tool.Registry{m.reg}
	}
	return nil
}

// serverNames lists the live server names (nil when no host is connected).
func (m *mcpManager) serverNames() []string {
	if h := m.hostRef(); h != nil {
		return h.ServerNames()
	}
	return nil
}

// hasServer reports whether a server is live.
func (m *mcpManager) hasServer(name string) bool {
	for _, n := range m.serverNames() {
		if n == name {
			return true
		}
	}
	return false
}

// prompts lists the live MCP prompts (nil when no host is connected).
func (m *mcpManager) prompts() []plugin.Prompt {
	if h := m.hostRef(); h != nil {
		return h.Prompts()
	}
	return nil
}

// failures lists the recorded MCP startup failures (nil when no host).
func (m *mcpManager) failures() []plugin.Failure {
	if h := m.hostRef(); h != nil {
		return h.Failures()
	}
	return nil
}

// readResource reads an MCP resource. Errors when no host is connected.
func (m *mcpManager) readResource(ctx context.Context, server, uri string) (string, error) {
	h := m.hostRef()
	if h == nil {
		return "", fmt.Errorf("no MCP servers connected")
	}
	return h.ReadResource(ctx, server, uri)
}
