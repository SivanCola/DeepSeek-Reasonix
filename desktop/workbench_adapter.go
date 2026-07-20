package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/provider"
	"reasonix/internal/remote/broker"
	"reasonix/internal/remote/protocol"
	"reasonix/internal/remote/workbench/client"
	"reasonix/internal/remote/workbench/target"
	"reasonix/internal/remote/workbench/transport"
	"reasonix/internal/remote/workbench/trust"
)

// workbenchKernel owns Local + Remote adapters for the main desktop window.
type workbenchKernel struct {
	mu           sync.Mutex
	targets      *target.Manager
	remote       *client.Client
	remoteGen    uint64
	pendingTrust *ProviderTrustPromptView
	trustAnswer  chan bool
}

// ProviderTrustPromptView is the Wails-facing Provider Broker authorization UI.
// Never includes secrets, base URLs, or env names.
type ProviderTrustPromptView struct {
	HostID       string   `json:"hostId"`
	Host         string   `json:"host"`
	KeyType      string   `json:"keyType"`
	Fingerprint  string   `json:"fingerprint"`
	Workspace    string   `json:"workspace"`
	ProviderRefs []string `json:"providerRefs"`
	Warning      string   `json:"warning"`
}

func newWorkbenchKernel() *workbenchKernel {
	return &workbenchKernel{targets: target.New()}
}

func (a *App) workbench() *workbenchKernel {
	a.remoteMu.Lock()
	defer a.remoteMu.Unlock()
	if a.workbenchKernel == nil {
		a.workbenchKernel = newWorkbenchKernel()
	}
	return a.workbenchKernel
}

// WorkbenchActiveTarget returns the current projection for the status bar.
func (a *App) WorkbenchActiveTarget() map[string]any {
	id, gen, seq := a.workbench().targets.Active()
	return map[string]any{
		"kind": string(id.Kind), "hostId": id.HostID, "workspace": id.Workspace,
		"identityGen": gen, "requestSeq": seq,
	}
}

// WorkbenchLastRemoteHint is the post-restart reconnect entry (no auto-connect).
func (a *App) WorkbenchLastRemoteHint() map[string]string {
	h := a.workbench().targets.LastRemoteHint()
	return map[string]string{"hostId": h.HostID, "workspace": h.Workspace, "label": h.Label}
}

// WorkbenchSwitchLocal projects the permanent Local adapter.
func (a *App) WorkbenchSwitchLocal() map[string]any {
	id, gen, seq := a.workbench().targets.SwitchLocal()
	return map[string]any{"kind": string(id.Kind), "identityGen": gen, "requestSeq": seq}
}

// WorkbenchConnectRemote opens SSH stdio workbench + local Provider Broker.
func (a *App) WorkbenchConnectRemote(hostID, workspace string) error {
	hostID = strings.TrimSpace(hostID)
	workspace = strings.TrimSpace(workspace)
	if hostID == "" || workspace == "" {
		return fmt.Errorf("host and workspace are required")
	}
	k := a.workbench()
	_, gen, err := k.targets.BeginRemoteConnect(hostID, workspace)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	entry, ok := cfg.RemoteHost(hostID)
	if !ok {
		return fmt.Errorf("unknown remote host %q", hostID)
	}
	fp, keyType, hostLabel, err := a.workbenchHostIdentity(hostID)
	if err != nil {
		return err
	}
	refs := localProviderRefs(cfg)
	store := trust.DefaultStore()
	missing, err := store.MissingRefs(hostID, fp, refs)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		if err := a.workbenchRequestTrust(hostID, hostLabel, keyType, fp, workspace, missing); err != nil {
			return err
		}
		if err := store.AuthorizeAll(hostID, keyType, fp, missing); err != nil {
			return err
		}
	}
	rec, _, _ := store.Get(hostID, fp)
	allowed := map[string]struct{}{}
	for _, r := range rec.AllowedProviderRefs {
		allowed[r] = struct{}{}
	}
	if len(allowed) == 0 {
		for _, r := range refs {
			allowed[r] = struct{}{}
		}
	}

	factory, err := a.workbenchTransportFactory(entry)
	if err != nil {
		return err
	}
	brokerOpts := broker.Options{
		Catalog: func(ctx context.Context, filter map[string]struct{}) ([]protocol.BrokerProviderDescriptor, error) {
			return catalogDescriptors(cfg, allowed, filter)
		},
		Open: func(ctx context.Context, ref string, req provider.Request) (<-chan provider.Chunk, error) {
			if _, ok := allowed[ref]; !ok {
				return nil, fmt.Errorf("provider %q not authorized for this host", ref)
			}
			return openLocalProviderStream(ctx, cfg, ref, req)
		},
	}
	buildID := map[string]any{
		"productVersion":  version,
		"protocolVersion": protocol.ProtocolVersion,
		"schemaHash":      protocol.SchemaHash(),
		"sourceRevision":  "workbench",
	}
	cli, err := client.Connect(a.bootContext(), factory, gen, brokerOpts, buildID, workspace)
	if err != nil {
		return err
	}
	if err := k.targets.MarkRemoteConnected(gen); err != nil {
		cli.Close()
		return err
	}
	k.mu.Lock()
	if k.remote != nil {
		k.remote.Close()
	}
	k.remote = cli
	k.remoteGen = gen
	k.mu.Unlock()
	k.targets.RememberRemote(target.RemoteHint{HostID: hostID, Workspace: workspace, Label: hostLabel})
	if _, _, _, err := k.targets.ActivateRemote(gen); err != nil {
		return err
	}
	a.saveLastRemoteWorkspace(hostID, workspace)
	return nil
}

// WorkbenchDisconnectRemote detaches when idle and revokes the Broker channel.
func (a *App) WorkbenchDisconnectRemote() error {
	k := a.workbench()
	if err := k.targets.DetachRemote(); err != nil {
		return err
	}
	k.mu.Lock()
	if k.remote != nil {
		k.remote.Close()
		k.remote = nil
	}
	k.mu.Unlock()
	return nil
}

// WorkbenchRemoteRequest proxies a RuntimeAPI method to the connected remote.
func (a *App) WorkbenchRemoteRequest(method string, paramsJSON string) (string, error) {
	k := a.workbench()
	id, gen, _ := k.targets.Active()
	if id.Kind != target.KindRemote {
		return "", fmt.Errorf("CAPABILITY_UNAVAILABLE: active target is local")
	}
	k.mu.Lock()
	cli := k.remote
	cliGen := k.remoteGen
	k.mu.Unlock()
	if cli == nil || cliGen != gen {
		return "", fmt.Errorf("CAPABILITY_UNAVAILABLE: remote not connected")
	}
	var params any
	if strings.TrimSpace(paramsJSON) != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
			return "", err
		}
	} else {
		params = map[string]any{}
	}
	raw, err := cli.Request(a.bootContext(), method, params)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// WorkbenchResolveProviderTrust answers a pending trust prompt.
func (a *App) WorkbenchResolveProviderTrust(accept bool) error {
	k := a.workbench()
	k.mu.Lock()
	ch := k.trustAnswer
	k.trustAnswer = nil
	k.pendingTrust = nil
	k.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("no pending provider trust prompt")
	}
	select {
	case ch <- accept:
	default:
	}
	return nil
}

// WorkbenchPendingProviderTrust returns the current prompt or nil.
func (a *App) WorkbenchPendingProviderTrust() *ProviderTrustPromptView {
	k := a.workbench()
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.pendingTrust
}

func (a *App) workbenchRequestTrust(hostID, hostLabel, keyType, fp, workspace string, refs []string) error {
	k := a.workbench()
	answer := make(chan bool, 1)
	k.mu.Lock()
	if k.trustAnswer != nil {
		k.mu.Unlock()
		return fmt.Errorf("provider trust prompt already pending")
	}
	k.trustAnswer = answer
	k.pendingTrust = &ProviderTrustPromptView{
		HostID: hostID, Host: hostLabel, KeyType: keyType, Fingerprint: fp,
		Workspace: workspace, ProviderRefs: append([]string(nil), refs...),
		Warning: "This remote host will consume your local model API quota through the Provider Broker until disconnect. API keys never leave this machine.",
	}
	prompt := *k.pendingTrust
	k.mu.Unlock()
	if a.ctx != nil {
		a.runtimeEvents.Emit(a.ctx, "remote:provider-trust", prompt)
	}
	select {
	case accept := <-answer:
		if !accept {
			return fmt.Errorf("provider trust declined for host %q", hostID)
		}
		return nil
	case <-a.bootContext().Done():
		return fmt.Errorf("connection closed while waiting for provider trust")
	}
}

func (a *App) workbenchHostIdentity(hostID string) (fingerprint, keyType, hostLabel string, err error) {
	// Prefer live SSH connection fingerprint from existing remote manager.
	rt, rerr := a.remoteRT()
	if rerr == nil {
		_ = rt.Connect(hostID)
		for _, s := range rt.Statuses() {
			if s.HostID != hostID {
				continue
			}
			if s.Fingerprint != nil {
				fingerprint = s.Fingerprint.SHA256
				keyType = s.Fingerprint.KeyType
				hostLabel = s.Fingerprint.Address
			}
		}
	}
	cfg, _ := config.Load()
	if entry, ok := cfg.RemoteHost(hostID); ok {
		if hostLabel == "" {
			hostLabel = entry.Host
			if entry.User != "" {
				hostLabel = entry.User + "@" + entry.Host
			}
		}
	}
	if fingerprint == "" {
		// Fallback identity for config-mode until host key is accepted.
		fingerprint = "pending:" + hostID
		keyType = "pending"
	}
	return fingerprint, keyType, hostLabel, nil
}

func (a *App) workbenchTransportFactory(entry config.RemoteHostEntry) (transport.Factory, error) {
	// Windows: system OpenSSH. Other platforms: Go SSH stdio session.
	return newWorkbenchSSHFactory(entry)
}

func localProviderRefs(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	var refs []string
	for i := range cfg.Providers {
		pe := &cfg.Providers[i]
		model := pe.Default
		if model == "" && len(pe.Models) > 0 {
			model = pe.Models[0]
		}
		if model == "" {
			model = pe.Model
		}
		if model == "" {
			refs = append(refs, pe.Name)
			continue
		}
		refs = append(refs, pe.Name+"/"+model)
	}
	return refs
}

func findProviderEntry(cfg *config.Config, name string) *config.ProviderEntry {
	if cfg == nil {
		return nil
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == name {
			return &cfg.Providers[i]
		}
	}
	return nil
}

func catalogDescriptors(cfg *config.Config, allowed, filter map[string]struct{}) ([]protocol.BrokerProviderDescriptor, error) {
	var out []protocol.BrokerProviderDescriptor
	for _, ref := range localProviderRefs(cfg) {
		if len(allowed) > 0 {
			if _, ok := allowed[ref]; !ok {
				continue
			}
		}
		if len(filter) > 0 {
			if _, ok := filter[ref]; !ok {
				continue
			}
		}
		provName, model, _ := strings.Cut(ref, "/")
		pe := findProviderEntry(cfg, provName)
		if pe == nil {
			continue
		}
		p, err := boot.NewProvider(pe)
		if err != nil {
			out = append(out, protocol.BrokerProviderDescriptor{Ref: ref, Model: model})
			continue
		}
		out = append(out, broker.DescriptorFromProvider(ref, provName, model, p, nil, pe.Effort, pe.Vision))
	}
	return out, nil
}

func openLocalProviderStream(ctx context.Context, cfg *config.Config, ref string, req provider.Request) (<-chan provider.Chunk, error) {
	provName, _, _ := strings.Cut(ref, "/")
	pe := findProviderEntry(cfg, provName)
	if pe == nil {
		return nil, fmt.Errorf("provider %q not configured", provName)
	}
	p, err := boot.NewProvider(pe)
	if err != nil {
		return nil, err
	}
	return p.Stream(ctx, req)
}
