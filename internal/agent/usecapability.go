package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/capability"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
	"reasonix/internal/tool"
)

// MCPCapabilityRuntime is the session-shared MCP substrate: Host, boot specs,
// provider-visible registry for already-registered tools, schema cache catalog,
// and live connection snapshots. Each agent (executor, planner, task/fleet
// child) gets its own UseCapabilityTool frontend so ledger/audit never cross
// agent boundaries, while process connections remain on the shared Host.
type MCPCapabilityRuntime struct {
	lifeCtx  context.Context
	host     *plugin.Host
	specs    []plugin.Spec
	registry *tool.Registry
	catalog  func() capability.Catalog
	// shared connection observation across all frontends on this session.
	state *mcpProxySharedState
}

type mcpProxySharedState struct {
	mu        sync.Mutex
	connected map[string]bool
	liveTools map[string][]plugin.CachedTool
}

// NewMCPCapabilityRuntime builds the session-shared MCP substrate. lifeCtx owns
// on-demand MCP child process lifetimes; specs must be the boot-converted specs.
func NewMCPCapabilityRuntime(lifeCtx context.Context, host *plugin.Host, specs []plugin.Spec, reg *tool.Registry, catalog func() capability.Catalog) *MCPCapabilityRuntime {
	return &MCPCapabilityRuntime{
		lifeCtx:  lifeCtx,
		host:     host,
		specs:    append([]plugin.Spec(nil), specs...),
		registry: reg,
		catalog:  catalog,
		state:    &mcpProxySharedState{connected: map[string]bool{}},
	}
}

// NewFrontend returns a per-agent use_capability instance. ledger/audit may be
// nil for ordinary sub-agents that do not run Delivery capability gates.
func (r *MCPCapabilityRuntime) NewFrontend(ledger *capability.Ledger, audit *capability.Audit) *UseCapabilityTool {
	if r == nil {
		return NewUseCapabilityTool(context.Background(), nil, nil, nil, ledger, audit, nil)
	}
	return &UseCapabilityTool{
		host:     r.host,
		lifeCtx:  r.lifeCtx,
		specs:    r.specs,
		registry: r.registry,
		ledger:   ledger,
		audit:    audit,
		catalog:  r.catalog,
		state:    r.state,
	}
}

// ConnectedProxyTools returns live tool metadata for servers connected through
// any frontend on this runtime, keyed by server name.
func (r *MCPCapabilityRuntime) ConnectedProxyTools() map[string][]plugin.CachedTool {
	if r == nil || r.state == nil {
		return nil
	}
	return r.state.snapshotLiveTools()
}

// UseCapabilityTool is the stable MCP capability proxy for Delivery, the
// two-model Planner, and task/fleet sub-agents. It lists, inspects, calls, or
// declines catalog capabilities without adding dynamic MCP tools to the
// provider-visible registry — subsequent calls keep using this stable schema.
// Multiple frontends may share one MCPCapabilityRuntime (Host + connection
// state) while keeping independent ledger/audit.
type UseCapabilityTool struct {
	host *plugin.Host
	// lifeCtx is the session-scoped context that owns on-demand MCP child
	// processes (mirrors lazySpawn.ctx): a proxied server must outlive the tool
	// call that started it and die with the session, not with a resolve-phase
	// timeout. nil falls back to context.Background() for direct/test use.
	lifeCtx context.Context
	// specs are the boot-converted plugin specs (env expansion, workspace
	// overrides and timeouts). The proxy never rebuilds
	// specs from raw config entries — that would fork the conversion logic.
	specs    []plugin.Spec
	registry *tool.Registry // live registry for already-exposed MCP tools
	ledger   *capability.Ledger
	audit    *capability.Audit
	catalog  func() capability.Catalog
	// state is session-shared connection observation when built via
	// MCPCapabilityRuntime; nil falls back to a private map for tests.
	state *mcpProxySharedState
}

// NewUseCapabilityTool builds a standalone capability proxy (tests and simple
// boots). Prefer MCPCapabilityRuntime.NewFrontend when multiple agents share
// one session Host.
func NewUseCapabilityTool(lifeCtx context.Context, host *plugin.Host, specs []plugin.Spec, reg *tool.Registry, ledger *capability.Ledger, audit *capability.Audit, catalog func() capability.Catalog) *UseCapabilityTool {
	return &UseCapabilityTool{
		host:     host,
		lifeCtx:  lifeCtx,
		specs:    append([]plugin.Spec(nil), specs...),
		registry: reg,
		ledger:   ledger,
		audit:    audit,
		catalog:  catalog,
		state:    &mcpProxySharedState{connected: map[string]bool{}},
	}
}

// CloneForAgent returns a new frontend sharing Host/specs/connection state but
// with independent ledger and audit (nil unless provided).
func (t *UseCapabilityTool) CloneForAgent(ledger *capability.Ledger, audit *capability.Audit) *UseCapabilityTool {
	if t == nil {
		return nil
	}
	state := t.state
	if state == nil {
		state = &mcpProxySharedState{connected: map[string]bool{}}
	}
	return &UseCapabilityTool{
		host:     t.host,
		lifeCtx:  t.lifeCtx,
		specs:    t.specs,
		registry: t.registry,
		ledger:   ledger,
		audit:    audit,
		catalog:  t.catalog,
		state:    state,
	}
}

func (*UseCapabilityTool) Name() string { return "use_capability" }

func (*UseCapabilityTool) Description() string {
	return "Stable capability proxy: list configured MCP servers without starting them, inspect Skill/MCP metadata, call MCP tools (including auto_start=false servers) without changing the provider tool schema, or decline a prefer capability with a non-empty reason. Skills still use run_skill; this tool only proxies MCP. The Planner leaves destructive MCP for the Executor; ordinary writer-capable agents use normal permission rules for destructive tools."
}

func (*UseCapabilityTool) ReadOnly() bool { return true }

func (*UseCapabilityTool) Schema() json.RawMessage {
	// Stable schema — must not change across turns or when MCP connects.
	// capability_id is optional only for action=list; inspect/call/decline still
	// require it at resolve time. One intentional prefix upgrade; thereafter
	// install/connect churn does not change this schema.
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"action":{"type":"string","description":"list | inspect | call | decline"},
			"capability_id":{"type":"string","description":"Capability id such as skill:review, mcp-server:github, or mcp-tool:github/search_issues. Not required for action=list."},
			"arguments":{"type":"object","description":"Raw MCP tool arguments for action=call"},
			"reason":{"type":"string","description":"Required non-empty reason when action=decline"}
		},
		"required":["action"]
	}`)
}

// ResolveCall implements tool.CallResolver so the agent can run permission,
// hooks, and evidence against the real MCP target before execution.
func (t *UseCapabilityTool) ResolveCall(ctx context.Context, args json.RawMessage) (tool.ResolvedCall, error) {
	var p struct {
		Action       string          `json:"action"`
		CapabilityID string          `json:"capability_id"`
		Arguments    json.RawMessage `json:"arguments"`
		Reason       string          `json:"reason"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return tool.ResolvedCall{}, fmt.Errorf("invalid args: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(p.Action))
	id := strings.TrimSpace(p.CapabilityID)
	base := tool.ResolvedCall{
		DisplayName:  "use_capability",
		ProxyAction:  action,
		CapabilityID: id,
		Args:         p.Arguments,
	}
	switch action {
	case "list":
		out, err := t.listServers()
		if err != nil {
			if t.audit != nil {
				t.audit.RecordMCPProxy(true, false, true)
			}
			return tool.ResolvedCall{}, err
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(true, false, false)
		}
		base.SkipExecute = true
		base.Result = out
		base.ReadOnly = true
		return base, nil
	case "inspect":
		if id == "" {
			return tool.ResolvedCall{}, fmt.Errorf("capability_id is required for action=inspect")
		}
		out, err := t.inspect(ctx, id)
		if err != nil {
			if t.audit != nil {
				t.audit.RecordMCPProxy(true, false, true)
			}
			return tool.ResolvedCall{}, err
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(true, false, false)
		}
		base.SkipExecute = true
		base.Result = out
		base.ReadOnly = true
		return base, nil
	case "decline":
		if id == "" {
			return tool.ResolvedCall{}, fmt.Errorf("capability_id is required for action=decline")
		}
		reason := strings.TrimSpace(p.Reason)
		if reason == "" {
			return tool.ResolvedCall{}, fmt.Errorf("reason is required for action=decline")
		}
		// Decline must not skip require. The mutation itself is delayed until the
		// agent has applied its post-resolution host boundary.
		if t.ledger != nil {
			if e, ok := t.ledger.Get(id); ok && e.Policy == capability.AutoUseRequire {
				return tool.ResolvedCall{}, fmt.Errorf("cannot decline a require capability %q", id)
			}
		}
		base.SkipExecute = true
		base.Result = fmt.Sprintf("declined capability %s: %s", id, reason)
		base.ReadOnly = true
		base.Commit = func() error {
			if t.ledger != nil {
				if err := t.ledger.MarkDeclined(id, reason); err != nil {
					return err
				}
			}
			if t.audit != nil {
				t.audit.RecordDecline()
			}
			return nil
		}
		return base, nil
	case "call":
		if id == "" {
			return tool.ResolvedCall{}, fmt.Errorf("capability_id is required for action=call")
		}
		return t.resolveCall(ctx, id, p.Arguments, base)
	default:
		return tool.ResolvedCall{}, fmt.Errorf("unknown action %q; use list, inspect, call, or decline", p.Action)
	}
}

func (t *UseCapabilityTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	resolved, err := t.ResolveCall(ctx, args)
	if err != nil {
		return "", err
	}
	if resolved.SkipExecute {
		if resolved.Commit != nil {
			if err := resolved.Commit(); err != nil {
				return "", err
			}
		}
		if resolved.ProxyAction == "call" && !resolved.Unavailable {
			if t.ledger != nil {
				t.ledger.MarkSucceeded(resolved.CapabilityID)
			}
			if t.audit != nil {
				t.audit.RecordMCPProxy(false, true, false)
			}
		}
		return resolved.Result, nil
	}
	if resolved.Unavailable {
		if t.ledger != nil {
			t.ledger.MarkUnavailable(resolved.CapabilityID, resolved.UnavailableReason)
		}
		return "", fmt.Errorf("capability unavailable: %s", resolved.UnavailableReason)
	}
	if resolved.Target == nil {
		return "", fmt.Errorf("no target tool resolved for %s", resolved.CapabilityID)
	}
	if t.ledger != nil {
		t.ledger.MarkInvoked(resolved.CapabilityID)
	}
	if t.audit != nil {
		t.audit.RecordMCPProxy(false, true, false)
	}
	out, err := resolved.Target.Execute(ctx, resolved.Args)
	if err != nil {
		if t.ledger != nil {
			t.ledger.MarkFailed(resolved.CapabilityID, err.Error())
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(false, true, true)
		}
		return out, err
	}
	if t.ledger != nil {
		t.ledger.MarkSucceeded(resolved.CapabilityID)
	}
	return out, nil
}

// listServerInfo is one configured MCP server entry returned by action=list.
// It never starts a server or opens a network connection.
type listServerInfo struct {
	Name         string `json:"name"`
	CapabilityID string `json:"capability_id"`
	Status       string `json:"status"`
	Authorized   bool   `json:"authorized"`
	Connected    bool   `json:"connected"`
}

// listServers returns sorted configured MCP server names, status, and
// capability IDs without starting servers. Used by Planner discovery when no
// specific capability route was provided.
func (t *UseCapabilityTool) listServers() (string, error) {
	list := make([]listServerInfo, 0, len(t.specs))
	seen := map[string]bool{}
	names := make([]string, 0, len(t.specs))
	byName := map[string]listServerInfo{}
	for _, spec := range t.specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		// Apply stored project grants without process/network side effects so
		// list status matches resolve/execute authorization.
		resolved := plugin.ResolveStoredAuthorization(context.Background(), spec)
		connected := t.host != nil && t.host.HasClient(name)
		status := "configured"
		if connected {
			status = "ready"
		} else if t.host != nil {
			for _, f := range t.host.Failures() {
				if f.Name == name && strings.TrimSpace(f.Error) != "" {
					status = "failed"
					break
				}
			}
		}
		names = append(names, name)
		byName[name] = listServerInfo{
			Name:         name,
			CapabilityID: "mcp-server:" + name,
			Status:       status,
			Authorized:   resolved.ServerAuthorized(),
			Connected:    connected,
		}
	}
	sort.Strings(names)
	for _, name := range names {
		list = append(list, byName[name])
	}
	b, err := json.MarshalIndent(map[string]any{
		"servers": list,
		"note":    "list does not start MCP servers. Call action=call on mcp-server:<name> to connect after authorization, or mcp-tool:<server>/<tool> for a concrete tool.",
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (t *UseCapabilityTool) inspect(ctx context.Context, id string) (string, error) {
	cat := t.currentCatalog()
	if e, ok := cat.Lookup(id); ok {
		b, _ := json.MarshalIndent(map[string]any{
			"id":          e.ID,
			"kind":        e.Kind,
			"name":        e.Name,
			"description": e.Description,
			"status":      e.Status,
			"read_only":   e.ReadOnly,
			"auto_use":    e.AutoUse,
			"requires":    e.Requires,
			"profiles":    e.Profiles,
			"tool_name":   e.ToolName,
			"auto_start":  e.AutoStart,
		}, "", "  ")
		// For MCP entries, list tools without side effects: live tools when the
		// server is already connected, cached schema otherwise. Inspect runs
		// during call resolution — before permission and hook gates — so it must
		// never start a subprocess or open a network connection.
		if e.Kind == capability.KindMCPServer || e.Kind == capability.KindMCPTool {
			server := e.Source
			if server == "" {
				server = e.ConnectName
			}
			toolFilter := ""
			if e.Kind == capability.KindMCPTool {
				parsedServer, raw, err := parseMCPCapabilityID(e.ID)
				if err != nil {
					return string(b), nil
				}
				if server == "" {
					server = parsedServer
				} else if parsedServer != server {
					return string(b), nil
				}
				toolFilter = raw
			}
			if server != "" {
				if t.host != nil && t.host.HasClient(server) {
					// serverTools refreshes the snapshot too: inspecting a
					// server another tab connected restores tool routing here.
					tools, err := t.serverTools(ctx, server)
					if err != nil {
						return string(b) + "\n\nTool listing failed: " + err.Error(), nil
					}
					return string(b) + "\n\nTools:\n" + inspectToolListJSON(server, filterInspectTools(tools, toolFilter)), nil
				}
				if spec, ok := t.specFor(server); ok {
					if cs, ok := plugin.LoadCachedSchemaForSpec(spec); ok && len(cs.Tools) > 0 {
						var list []inspectToolInfo
						for _, ct := range cs.Tools {
							if toolFilter != "" && ct.Name != toolFilter {
								continue
							}
							list = append(list, inspectToolInfo{
								ID:          "mcp-tool:" + server + "/" + ct.Name,
								Name:        plugin.ModelToolName(server, ct.Name),
								Description: ct.Description,
								ReadOnly:    ct.ReadOnly,
								Schema:      ct.Schema,
							})
						}
						extra, _ := json.MarshalIndent(list, "", "  ")
						return string(b) + "\n\nTools (from cached schema; server not started):\n" + string(extra), nil
					}
					return string(b) + "\n\nServer not connected and no cached tool schema; call use_capability(action=\"call\", capability_id=\"mcp-server:" + server + "\") to connect (after approval) and list its tools.", nil
				}
			}
		}
		return string(b), nil
	}
	return "", fmt.Errorf("unknown capability_id %q", id)
}

// filterInspectTools narrows concrete mcp-tool inspection to that exact tool.
// Server inspection intentionally keeps the full directory. This prevents a
// restricted sub-agent allowed one tool from discovering sibling tool schemas
// through action=inspect on its allowed capability ID.
func filterInspectTools(tools []tool.Tool, raw string) []tool.Tool {
	if raw == "" {
		return tools
	}
	filtered := make([]tool.Tool, 0, 1)
	for _, tl := range tools {
		if m, ok := tl.(tool.MCPMetadata); ok && m.MCPRawToolName() == raw {
			filtered = append(filtered, tl)
			break
		}
	}
	return filtered
}

type inspectToolInfo struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ReadOnly    bool            `json:"read_only"`
	Schema      json.RawMessage `json:"input_schema,omitempty"`
}

// inspectToolListJSON renders a server's live tools as the capability-id
// directory shared by inspect and the first-discovery connect result.
func inspectToolListJSON(server string, tools []tool.Tool) string {
	var list []inspectToolInfo
	for _, tl := range tools {
		raw := ""
		if m, ok := tl.(tool.MCPMetadata); ok {
			raw = m.MCPRawToolName()
		}
		list = append(list, inspectToolInfo{
			ID:          "mcp-tool:" + server + "/" + raw,
			Name:        tl.Name(),
			Description: tl.Description(),
			ReadOnly:    tl.ReadOnly(),
			Schema:      tl.Schema(),
		})
	}
	extra, _ := json.MarshalIndent(list, "", "  ")
	return string(extra)
}

func (t *UseCapabilityTool) resolveCall(ctx context.Context, id string, args json.RawMessage, base tool.ResolvedCall) (tool.ResolvedCall, error) {
	// Server-level call is the first-discovery path for servers with no
	// schema cache: it resolves to a gated connect-and-list target so the
	// model can learn tool names without inspect ever starting a process.
	if server, ok := parseMCPServerCapabilityID(id); ok {
		return t.resolveServerConnect(ctx, server, base)
	}
	server, raw, err := parseMCPCapabilityID(id)
	if err != nil {
		// Skills must use run_skill.
		if strings.HasPrefix(id, "skill:") {
			return tool.ResolvedCall{}, fmt.Errorf("call only proxies MCP tools; use run_skill for %s", id)
		}
		return tool.ResolvedCall{}, err
	}
	// Prefer already-exposed registry tool (auto-started MCP). The model name
	// MUST come from the plugin layer's canonical constructor: it appends a
	// collision hash for sanitised raw names, and permission/hook rules are
	// written against that executed name — a proxy-local normalization would
	// let them silently miss.
	modelName := plugin.ModelToolName(server, raw)
	if t.registry != nil {
		if tl, ok := t.registry.Get(modelName); ok {
			base.TargetName = modelName
			base.Target = tl
			base.ReadOnly = tl.ReadOnly()
			if len(args) == 0 {
				base.Args = json.RawMessage(`{}`)
			} else {
				base.Args = args
			}
			return base, nil
		}
	}
	// Server already connected (auto-started, a previous proxy call, or a
	// sibling tab sharing this host): resolving against live tools is
	// side-effect-free. serverTools also refreshes the catalog snapshot so a
	// cross-tab connect still yields routable mcp-tool entries here.
	if t.host != nil && t.host.HasClient(server) {
		tools, err := t.serverTools(ctx, server)
		if err != nil {
			return t.resolveUnavailable(base, id, modelName, err.Error()), nil
		}
		target := findMCPTool(tools, raw, modelName)
		if target == nil {
			return t.resolveUnavailable(base, id, modelName, fmt.Sprintf("MCP tool %q not found on server %q", raw, server)), nil
		}
		base.Target = target
		base.TargetName = target.Name()
		base.ReadOnly = target.ReadOnly()
		if len(args) == 0 {
			base.Args = json.RawMessage(`{}`)
		} else {
			base.Args = args
		}
		return base, nil
	}
	// Unconnected server: resolution must stay pure — no subprocess, no network.
	// Return a deferred target that connects in Execute, after the permission
	// gate and PreToolUse hooks have approved the real target name/arguments.
	spec, ok := t.specFor(server)
	if !ok {
		return t.resolveUnavailable(base, id, modelName, fmt.Sprintf("MCP server %q is not configured", server)), nil
	}
	spec = plugin.ResolveStoredAuthorization(ctx, spec)
	destructive := false
	if t.catalog != nil {
		if entry, found := t.catalog().Lookup(id); found {
			destructive = entry.Destructive
		}
	}
	readOnly := false
	if cached, found := plugin.CachedToolSafetyForSpec(spec, raw); found {
		destructive = destructive || cached.Destructive
		readOnly = cached.ReadOnly
	}
	lazy := &onDemandMCPTool{proxy: t, spec: spec, server: server, raw: raw, modelName: modelName, destructive: destructive}
	lazy.readOnly = readOnly
	base.Target = lazy
	base.TargetName = modelName
	// Cached server hints control ordinary approval. Strict read-only execution
	// additionally requires server authorization and live read-only metadata.
	base.ReadOnly = lazy.ReadOnly()
	if len(args) == 0 {
		base.Args = json.RawMessage(`{}`)
	} else {
		base.Args = args
	}
	return base, nil
}

// resolveUnavailable fills the host-proven unavailable shape shared by the
// side-effect-free resolution failures (missing config, unknown tool).
func (t *UseCapabilityTool) resolveUnavailable(base tool.ResolvedCall, id, modelName, reason string) tool.ResolvedCall {
	base.Unavailable = true
	base.UnavailableReason = reason
	base.SkipExecute = true
	base.Result = "capability unavailable: " + reason
	base.TargetName = modelName
	base.ReadOnly = false
	base.Commit = func() error {
		if t.ledger != nil {
			t.ledger.MarkUnavailable(id, reason)
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(false, true, true)
		}
		return nil
	}
	return base
}

// findMCPTool matches a server's tool list by raw MCP name or by the
// canonical namespaced model-visible name (plugin.ModelToolName).
func findMCPTool(tools []tool.Tool, raw, modelName string) tool.Tool {
	for _, tl := range tools {
		if m, ok := tl.(tool.MCPMetadata); ok && m.MCPRawToolName() == raw {
			return tl
		}
		if tl.Name() == modelName {
			return tl
		}
	}
	return nil
}

// onDemandMCPTool defers MCP server startup to Execute so permission and hook
// gates always run before any subprocess or network side effect. Before the live
// handshake it remains write-capable until the resolved MCP tool is classified.
type onDemandMCPTool struct {
	proxy     *UseCapabilityTool
	spec      plugin.Spec
	server    string
	raw       string
	modelName string
	// destructive comes from the schema cache when available. A live promotion
	// is detected in Execute so a retry re-enters the current Plan/read-only
	// execution boundary.
	destructive bool
	readOnly    bool
}

func (o *onDemandMCPTool) Name() string { return o.modelName }

func (o *onDemandMCPTool) Description() string {
	return "on-demand MCP tool " + o.server + "/" + o.raw + " (connects when first used)"
}

func (o *onDemandMCPTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (o *onDemandMCPTool) ReadOnly() bool {
	return o.readOnly
}

func (o *onDemandMCPTool) ReadOnlyExecutionHostMutation() bool { return true }

func (o *onDemandMCPTool) MCPServerAuthorized() bool {
	// Spec.Authorized is the single runtime authorization result. Boot/install
	// and ResolveStoredAuthorization set it; this path never invents trust.
	return o.spec.ServerAuthorized()
}

func (o *onDemandMCPTool) ReadOnlyExecutionBlockReason() string {
	return "connect this MCP capability from a parent session first"
}

// MCPServerName/MCPRawToolName expose the deferred target for audit and
// diagnostics (tool.MCPMetadata).
func (o *onDemandMCPTool) MCPServerName() string  { return o.server }
func (o *onDemandMCPTool) MCPRawToolName() string { return o.raw }
func (o *onDemandMCPTool) MCPDestructiveHint() bool {
	return o.destructive
}

func (o *onDemandMCPTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	// Final authorization check before any process/network start.
	if !o.MCPServerAuthorized() {
		msg := fmt.Sprintf("MCP server %q is not authorized; complete project identity approval before connecting", o.server)
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-tool:"+o.server+"/"+o.raw, msg)
		}
		return "", fmt.Errorf("%s", msg)
	}
	tools, err := o.proxy.ensureServerTools(ctx, o.server)
	if err != nil {
		// Audit for the call path is recorded once by the agent loop
		// (noteCapabilityInvocation); only the ledger outcome lands here.
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-tool:"+o.server+"/"+o.raw, err.Error())
		}
		return "", err
	}
	target := findMCPTool(tools, o.raw, o.modelName)
	if target == nil {
		msg := fmt.Sprintf("MCP tool %q not found on server %q", o.raw, o.server)
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-tool:"+o.server+"/"+o.raw, msg)
		}
		return "", fmt.Errorf("%s", msg)
	}
	if _, err := plugin.ReconcileCachedToolSafety(o.server, o.raw, plugin.CachedToolSafety{
		ReadOnly:    o.readOnly,
		Destructive: o.destructive,
	}, target); err != nil {
		return "", err
	}
	// Planner non-destructive lane and reader lane: re-check live metadata
	// before tools/call even when Reconcile did not see a cache promotion.
	if tool.HasNonDestructiveMCPExecutionIntent(ctx) {
		if !mcpServerAuthorized(target) || mcpDestructiveHint(target) {
			return "", fmt.Errorf("MCP server %q changed the authorization or destructive classification for tool %q; the call was blocked before dispatch — retry so Reasonix can re-apply the current Planner MCP safety boundary", o.server, o.raw)
		}
	}
	return target.Execute(ctx, args)
}

func (t *UseCapabilityTool) ensureServerTools(ctx context.Context, server string) ([]tool.Tool, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return nil, fmt.Errorf("empty MCP server name")
	}
	if t.host == nil {
		return nil, fmt.Errorf("MCP host unavailable")
	}
	// Reuse shared host if already connected (including auto-started).
	if t.host.HasClient(server) {
		return t.serverTools(ctx, server)
	}
	spec, ok := t.specFor(server)
	if !ok {
		return nil, fmt.Errorf("MCP server %q is not configured", server)
	}
	// Apply stored project grants, then refuse any unauthorized server before
	// process or network start. Spec.Authorized is the only trust bit — boot
	// and install set it for user/host installs; this path never upgrades it.
	// Explicit deny on mcp_connect__* is enforced by the permission gate before
	// Execute reaches here.
	spec = plugin.ResolveStoredAuthorization(ctx, spec)
	if !spec.ServerAuthorized() {
		return nil, fmt.Errorf("MCP server %q is not authorized; install it or complete project identity approval before connecting", server)
	}
	// On-demand connect: the handshake gets a short budget, but the child
	// process lifetime belongs to the session-scoped lifeCtx — canceling the
	// handshake context after connect must not kill a stdio server the tool
	// call is about to use. Tools stay off the main registry.
	life := t.lifeCtx
	if life == nil {
		life = context.Background()
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tools, err := t.host.AddWithLifecycle(life, handshakeCtx, spec)
	if err != nil {
		if plugin.IsServerAlreadyConnected(err) {
			return t.serverTools(ctx, server)
		}
		t.host.RecordFailure(spec, err)
		return nil, fmt.Errorf("connect %q: %w", server, err)
	}
	t.ensureState().markConnected(server)
	// Intentionally do NOT add tools to t.registry — provider schema stays stable.
	_ = tools
	return t.serverTools(ctx, server)
}

// serverTools fetches the live tools for a connected server and refreshes the
// shared catalog snapshot so mcp-tool entries stay routable once the server
// is StatusReady (its tools are absent from the provider-visible registry).
func (t *UseCapabilityTool) serverTools(ctx context.Context, server string) ([]tool.Tool, error) {
	tools, err := t.host.ToolsFor(ctx, server)
	if err != nil {
		return nil, err
	}
	snap := make([]plugin.CachedTool, 0, len(tools))
	for _, tl := range tools {
		m, ok := tl.(tool.MCPMetadata)
		if !ok || m.MCPRawToolName() == "" {
			continue
		}
		snap = append(snap, plugin.CachedTool{
			Name:        m.MCPRawToolName(),
			Description: tl.Description(),
			Schema:      tl.Schema(),
			ReadOnly:    tl.ReadOnly(),
			Destructive: mcpDestructiveHint(tl),
		})
	}
	t.ensureState().setLiveTools(server, snap)
	return tools, nil
}

// ConnectedProxyTools returns raw tool metadata for servers connected through
// any frontend sharing this proxy state, keyed by server name. Catalog builders
// consume it so concrete mcp-tool capabilities survive an on-demand connect
// without ever touching the provider-visible registry.
func (t *UseCapabilityTool) ConnectedProxyTools() map[string][]plugin.CachedTool {
	if t == nil {
		return nil
	}
	return t.ensureState().snapshotLiveTools()
}

func (t *UseCapabilityTool) ensureState() *mcpProxySharedState {
	if t.state == nil {
		t.state = &mcpProxySharedState{connected: map[string]bool{}}
	}
	return t.state
}

func (s *mcpProxySharedState) markConnected(server string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connected == nil {
		s.connected = map[string]bool{}
	}
	s.connected[server] = true
}

func (s *mcpProxySharedState) setLiveTools(server string, snap []plugin.CachedTool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.liveTools == nil {
		s.liveTools = map[string][]plugin.CachedTool{}
	}
	s.liveTools[server] = snap
	if s.connected == nil {
		s.connected = map[string]bool{}
	}
	s.connected[server] = true
}

func (s *mcpProxySharedState) snapshotLiveTools() map[string][]plugin.CachedTool {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.liveTools) == 0 {
		return nil
	}
	out := make(map[string][]plugin.CachedTool, len(s.liveTools))
	for k, v := range s.liveTools {
		out[k] = append([]plugin.CachedTool(nil), v...)
	}
	return out
}

// specFor looks up the boot-converted spec for server. The proxy deliberately
// holds []plugin.Spec, not raw config entries: env expansion, workspace
// overrides, call timeouts, and read-only tool names all live in the
// shared conversion and must not be re-derived here.
func (t *UseCapabilityTool) specFor(server string) (plugin.Spec, bool) {
	for _, s := range t.specs {
		if s.Name == server {
			return s, true
		}
	}
	return plugin.Spec{}, false
}

func (t *UseCapabilityTool) currentCatalog() capability.Catalog {
	if t.catalog != nil {
		return t.catalog()
	}
	return capability.Catalog{}
}

// parseMCPServerCapabilityID extracts the server name from an mcp-server id.
func parseMCPServerCapabilityID(id string) (string, bool) {
	if !strings.HasPrefix(id, "mcp-server:") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(id, "mcp-server:"))
	return name, name != ""
}

// resolveServerConnect resolves action=call on an mcp-server id. A connected
// server lists its tools immediately (side-effect free); an unconnected one
// resolves to a deferred connect target that runs only after the permission
// gate and PreToolUse hooks approve it. Stored project authorization is applied
// at resolve time so unauthorized project MCP never reaches process startup.
func (t *UseCapabilityTool) resolveServerConnect(ctx context.Context, server string, base tool.ResolvedCall) (tool.ResolvedCall, error) {
	id := "mcp-server:" + server
	if t.host != nil && t.host.HasClient(server) {
		out, err := t.listServerTools(ctx, server)
		if err != nil {
			return t.resolveUnavailable(base, id, plugin.ToolPrefix(server), err.Error()), nil
		}
		base.SkipExecute = true
		base.Result = out
		base.ReadOnly = true
		return base, nil
	}
	spec, ok := t.specFor(server)
	if !ok {
		return t.resolveUnavailable(base, id, plugin.ToolPrefix(server), fmt.Sprintf("MCP server %q is not configured", server)), nil
	}
	spec = plugin.ResolveStoredAuthorization(ctx, spec)
	connect := &onDemandMCPConnect{proxy: t, spec: spec, server: server}
	base.Target = connect
	// A dedicated exact identity names the connect for permission and hook
	// rules. It cannot collide with a real mcp__ tool, and rules do not need to
	// rely on unsupported tool-name glob matching.
	base.TargetName = connect.Name()
	// Connecting spawns a subprocess, so it is never a read-only fast path for
	// ordinary Plan/strict agents. PlannerMCPExecution may allow authorized
	// connects; unauthorized specs are blocked before process/network start.
	base.ReadOnly = false
	base.Args = json.RawMessage(`{}`)
	return base, nil
}

// onDemandMCPConnect is the deferred first-discovery target: it connects the
// server post-approval and returns the live tool directory.
type onDemandMCPConnect struct {
	proxy  *UseCapabilityTool
	spec   plugin.Spec
	server string
}

func (o *onDemandMCPConnect) Name() string { return plugin.MCPConnectPermissionName(o.server) }

func (o *onDemandMCPConnect) Description() string {
	return "connect MCP server " + o.server + " on demand and list its tools"
}

func (o *onDemandMCPConnect) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (o *onDemandMCPConnect) ReadOnly() bool { return false }

// MCPLifecycleConnect marks this target as an MCP connect-and-list lifecycle
// action for Planner authorization (not a remote tools/call).
func (o *onDemandMCPConnect) MCPLifecycleConnect() bool { return true }

func (o *onDemandMCPConnect) MCPServerAuthorized() bool {
	return o.spec.ServerAuthorized()
}

func (o *onDemandMCPConnect) MCPServerName() string { return o.server }

func (o *onDemandMCPConnect) ReadOnlyExecutionHostMutation() bool { return true }

func (o *onDemandMCPConnect) ReadOnlyExecutionBlockReason() string {
	if !o.spec.ServerAuthorized() {
		return "start an unauthorized MCP server (install it or complete project identity approval first)"
	}
	return "connect this MCP server from a parent session first"
}

func (o *onDemandMCPConnect) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	// Zero process/network start when authorization is missing or was revoked
	// after resolve. Spec.Authorized is the only trust bit.
	if !o.spec.ServerAuthorized() {
		msg := fmt.Sprintf("MCP server %q is not authorized; install it or complete project identity approval before connecting", o.server)
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-server:"+o.server, msg)
		}
		return "", fmt.Errorf("%s", msg)
	}
	if _, err := o.proxy.ensureServerTools(ctx, o.server); err != nil {
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-server:"+o.server, err.Error())
		}
		return "", err
	}
	return o.proxy.listServerTools(ctx, o.server)
}

// listServerTools renders the live tool directory of a connected server and
// refreshes the proxy snapshot on the way (via serverTools).
func (t *UseCapabilityTool) listServerTools(ctx context.Context, server string) (string, error) {
	tools, err := t.serverTools(ctx, server)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("connected MCP server %q; %d tools:\n%s", server, len(tools), inspectToolListJSON(server, tools)), nil
}

func parseMCPCapabilityID(id string) (server, raw string, err error) {
	id = strings.TrimSpace(id)
	switch {
	case strings.HasPrefix(id, "mcp-tool:"):
		rest := strings.TrimPrefix(id, "mcp-tool:")
		server, raw, ok := strings.Cut(rest, "/")
		if !ok || server == "" || raw == "" {
			return "", "", fmt.Errorf("invalid mcp-tool id %q; want mcp-tool:<server>/<tool>", id)
		}
		return server, raw, nil
	case strings.HasPrefix(id, "mcp-server:"):
		return "", "", fmt.Errorf("%q is a server id; call it directly to connect and list tools, or use mcp-tool:<server>/<tool>", id)
	default:
		return "", "", fmt.Errorf("action=call requires an mcp-tool capability id, got %q", id)
	}
}

// Ensure UseCapabilityTool satisfies the tool contracts used by the agent.
var (
	_ tool.Tool         = (*UseCapabilityTool)(nil)
	_ tool.CallResolver = (*UseCapabilityTool)(nil)
)

// EmitProxyAudit is a helper for frontends: returns a notice describing the
// proxy name and real target for user audit trails.
func EmitProxyAudit(sink event.Sink, resolved tool.ResolvedCall) {
	if sink == nil || resolved.TargetName == "" {
		return
	}
	sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Text:   fmt.Sprintf("capability proxy: %s → %s", resolved.DisplayName, resolved.TargetName),
		Detail: resolved.CapabilityID,
	})
}
