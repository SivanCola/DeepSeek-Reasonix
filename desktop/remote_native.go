package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/remote/broker"
	"reasonix/internal/remote/gateway"
	"reasonix/internal/remote/protocol"
	"reasonix/internal/remote/target"
)

// remoteNativeKernel holds parent-process remote desktop services.
type remoteNativeKernel struct {
	mu          sync.Mutex
	gateway     *gateway.Server
	broker      *broker.Server
	brokerAddr  string // host:port of local broker listener
	gatewayBase string
	svcCancel   context.CancelFunc
	// tokensBySession revokes broker tokens when a remote window closes.
	tokensBySession map[string]broker.CapabilityToken
}

var appRemoteNative = &remoteNativeKernel{
	tokensBySession: map[string]broker.CapabilityToken{},
}

func (k *remoteNativeKernel) ensureServices(ctx context.Context) (*gateway.Server, *broker.Server, string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.gateway != nil && k.broker != nil && k.brokerAddr != "" {
		return k.gateway, k.broker, k.brokerAddr, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, "", err
	}
	resolver := boot.NewLocalProviderResolver(cfg, cfg.NetworkProxySpec())
	b := broker.NewServer(broker.Options{Resolver: resolver})
	g := gateway.New()
	svcCtx, cancel := context.WithCancel(context.Background())
	baddr, err := b.ListenAndServe(svcCtx, "127.0.0.1:0")
	if err != nil {
		cancel()
		return nil, nil, "", fmt.Errorf("start provider broker: %w", err)
	}
	gaddr, err := g.ListenAndServe(svcCtx)
	if err != nil {
		cancel()
		b.Close()
		return nil, nil, "", fmt.Errorf("start remote gateway: %w", err)
	}
	k.gateway = g
	k.broker = b
	k.brokerAddr = baddr.String()
	k.gatewayBase = "http://" + gaddr.String()
	k.svcCancel = cancel
	return g, b, k.brokerAddr, nil
}

func (k *remoteNativeKernel) gatewayBaseLocked() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.gatewayBase
}

func (k *remoteNativeKernel) rememberToken(sessionID string, tok broker.CapabilityToken) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.tokensBySession == nil {
		k.tokensBySession = map[string]broker.CapabilityToken{}
	}
	k.tokensBySession[sessionID] = tok
}

func (k *remoteNativeKernel) revokeSession(sessionID string) {
	k.mu.Lock()
	tok, ok := k.tokensBySession[sessionID]
	if ok {
		delete(k.tokensBySession, sessionID)
	}
	brk := k.broker
	k.mu.Unlock()
	if ok && brk != nil {
		brk.Revoke(tok)
	}
}

func (a *App) openNativeRemoteWorkspace(hostID, workspace string) error {
	hostID = strings.TrimSpace(hostID)
	workspace = strings.TrimSpace(workspace)
	if hostID == "" || workspace == "" {
		return fmt.Errorf("host and workspace are required")
	}
	rt, err := a.remoteRT()
	if err != nil {
		return err
	}
	// SSH connect
	if err := rt.Connect(hostID); err != nil {
		st := rt.Statuses()
		connected := false
		for _, s := range st {
			if s.HostID == hostID && (s.State == "connected" || s.State == "ready") {
				connected = true
				break
			}
		}
		if !connected {
			return err
		}
	}

	var fingerprint, keyType, hostLabel string
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
	if hostLabel == "" {
		if hosts, herr := rt.Hosts(); herr == nil {
			for _, h := range hosts {
				if h.ID == hostID {
					hostLabel = h.Host
					if h.User != "" {
						hostLabel = h.User + "@" + h.Host
					}
					break
				}
			}
		}
	}
	if fingerprint == "" {
		return fmt.Errorf("host key fingerprint unavailable; accept the host key first")
	}

	gw, brk, brokerAddr, err := appRemoteNative.ensureServices(a.bootContext())
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	localResolver := boot.NewLocalProviderResolver(cfg, cfg.NetworkProxySpec())
	catalog := localResolver.Catalog()
	refs := make([]string, 0, len(catalog))
	allowed := map[string]struct{}{}
	for _, d := range catalog {
		refs = append(refs, d.Ref)
		allowed[d.Ref] = struct{}{}
	}

	// Provider trust: missing refs require explicit user confirmation.
	trust := broker.DefaultTrustStore()
	missing := trust.MissingRefs(hostID, fingerprint, refs)
	if len(missing) > 0 {
		if mgr, ok := rt.(*desktopRemoteManager); ok {
			if err := mgr.RequestProviderTrust(hostID, hostLabel, keyType, fingerprint, workspace, missing); err != nil {
				return err
			}
		}
		if err := trust.AuthorizeAll(hostID, keyType, fingerprint, missing); err != nil {
			return fmt.Errorf("provider authorization: %w", err)
		}
	}
	// Refresh allowed set from durable trust (may be a subset after partial auth).
	if rec, ok, _ := trust.Get(hostID, fingerprint); ok {
		allowed = map[string]struct{}{}
		refs = refs[:0]
		for _, r := range rec.AllowedProviderRefs {
			allowed[r] = struct{}{}
			refs = append(refs, r)
		}
	}
	if len(allowed) == 0 {
		return fmt.Errorf("no providers authorized for remote host %q", hostID)
	}

	tok, err := brk.Issue(broker.Scope{
		HostID:      hostID,
		Fingerprint: fingerprint,
		Workspace:   workspace,
		AllowedRefs: allowed,
	})
	if err != nil {
		return err
	}

	// Pure remote-runtime bootstrap + Broker -R + local forward.
	view, remoteToken, err := rt.EnsureRemoteRuntime(a.bootContext(), hostID, workspace, brokerAddr, tok.String())
	if err != nil {
		brk.Revoke(tok)
		return fmt.Errorf("start remote-runtime: %w", err)
	}
	remoteBase := strings.TrimRight(view.LocalURL, "/")
	if remoteBase == "" {
		brk.Revoke(tok)
		return fmt.Errorf("remote-runtime did not report a local URL")
	}

	// Protocol handshake is required — no Serve HTML fallback.
	if err := probeRemoteProtocol(a.bootContext(), remoteBase, remoteToken); err != nil {
		brk.Revoke(tok)
		return fmt.Errorf("remote protocol handshake failed (upgrade remote reasonix): %w", err)
	}

	connID, err := randomHexID(8)
	if err != nil {
		brk.Revoke(tok)
		return err
	}
	sess := gateway.Session{
		ID:           "gws_" + connID,
		HostID:       hostID,
		Workspace:    workspace,
		ConnectionID: connID,
		RemoteBase:   remoteBase,
		RemoteToken:  remoteToken,
		Fingerprint:  fingerprint,
		BrokerStatus: "ready",
	}
	if _, err := gw.RegisterSession(sess); err != nil {
		brk.Revoke(tok)
		return err
	}
	appRemoteNative.rememberToken(sess.ID, tok)

	a.saveLastRemoteWorkspace(hostID, workspace)
	_ = target.ExecutionTarget{Kind: target.KindSSH, HostID: hostID, Workspace: workspace}

	return a.openRemoteGatewayWindow(remoteWindowLaunch{
		Mode:         "gateway",
		GatewayURL:   appRemoteNative.gatewayBaseLocked(),
		GatewayToken: gw.Token(),
		SessionID:    sess.ID,
		HostID:       hostID,
		Workspace:    workspace,
		Title:        remoteWindowTitle(hostID),
	})
}

func probeRemoteProtocol(ctx context.Context, base, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+protocol.APIPrefix+"/hello", nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("X-Reasonix-Remote-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: hello status %d", protocol.ErrProtocolMismatch, resp.StatusCode)
	}
	var hello protocol.HelloResponse
	if err := json.NewDecoder(resp.Body).Decode(&hello); err != nil {
		return fmt.Errorf("%w: decode hello: %v", protocol.ErrInvalidResponse, err)
	}
	return hello.Compatible()
}

func randomHexID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
