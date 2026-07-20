package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/remote/broker"
	"reasonix/internal/remote/gateway"
	"reasonix/internal/remote/protocol"
)

// remoteNativeKernel holds parent-process remote desktop services.
type remoteNativeKernel struct {
	mu          sync.Mutex
	gateway     *gateway.Server
	broker      *broker.Server
	gatewayBase string
	svcCancel   context.CancelFunc
}

var appRemoteNative = &remoteNativeKernel{}

func (k *remoteNativeKernel) ensureServices(ctx context.Context) (*gateway.Server, *broker.Server, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.gateway != nil && k.broker != nil {
		return k.gateway, k.broker, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	resolver := boot.NewLocalProviderResolver(cfg, cfg.NetworkProxySpec())
	b := broker.NewServer(broker.Options{Resolver: resolver})
	g := gateway.New()
	svcCtx, cancel := context.WithCancel(context.Background())
	baddr, err := b.ListenAndServe(svcCtx, "127.0.0.1:0")
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("start provider broker: %w", err)
	}
	_ = baddr
	gaddr, err := g.ListenAndServe(svcCtx)
	if err != nil {
		cancel()
		b.Close()
		return nil, nil, fmt.Errorf("start remote gateway: %w", err)
	}
	k.gateway = g
	k.broker = b
	k.gatewayBase = "http://" + gaddr.String()
	k.svcCancel = cancel
	return g, b, nil
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

	var fingerprint, keyType string
	for _, s := range rt.Statuses() {
		if s.HostID == hostID && s.Fingerprint != nil {
			fingerprint = s.Fingerprint.SHA256
			keyType = s.Fingerprint.KeyType
		}
	}
	if fingerprint == "" {
		fingerprint = "pending:" + hostID
	}

	gw, brk, err := appRemoteNative.ensureServices(a.bootContext())
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
	trust := broker.DefaultTrustStore()
	if !strings.HasPrefix(fingerprint, "pending:") {
		if miss := trust.MissingRefs(hostID, fingerprint, refs); len(miss) > 0 {
			if err := trust.AuthorizeAll(hostID, keyType, fingerprint, refs); err != nil {
				return fmt.Errorf("provider authorization: %w", err)
			}
		}
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

	// Bootstrap remote binary + local forward. The long-term remote-runtime
	// launch replaces serve; during the transition we still use EnsureServer for
	// install/forward plumbing, then refuse Serve HTML and require /remote/v1.
	view, remoteToken, err := rt.EnsureServer(a.bootContext(), hostID, workspace)
	if err != nil {
		brk.Revoke(tok)
		return fmt.Errorf("start remote runtime (bootstrap): %w", err)
	}
	remoteBase := strings.TrimRight(view.LocalURL, "/")
	if remoteBase == "" {
		brk.Revoke(tok)
		return fmt.Errorf("remote runtime did not report a local URL")
	}

	// Best-effort protocol probe; continue so the native window can show status.
	_ = probeRemoteProtocol(a.bootContext(), remoteBase, remoteToken)

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

	a.saveLastRemoteWorkspace(hostID, workspace)
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

func (k *remoteNativeKernel) gatewayBaseLocked() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.gatewayBase
}

func probeRemoteProtocol(ctx context.Context, base, token string) error {
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
		return fmt.Errorf("%w: remote-runtime hello status %d", protocol.ErrProtocolMismatch, resp.StatusCode)
	}
	return nil
}

func randomHexID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
