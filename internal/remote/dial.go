package remote

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"

	"reasonix/internal/netclient"
)

// dialConfig carries everything a single dial (or one hop of a jump chain)
// needs. It is assembled by Client.Start from Options.
type dialConfig struct {
	host        ResolvedHost
	auth        *AuthOptions
	hostKeys    *HostKeyPolicy
	dialer      netclient.StreamDialer // first-hop transport; nil => direct
	dialTimeout time.Duration
}

// dialSSH establishes an *ssh.Client to cfg.host, walking any ProxyJump chain
// left-to-right. The netclient proxy (cfg.dialer) applies only to the first
// hop, matching OpenSSH semantics; subsequent hops are dialed through the
// preceding hop's SSH connection. Each hop's host key is verified.
//
// It returns the target client and the ordered list of intermediary clients
// (jump hosts) so the caller can close them when the target connection ends.
func dialSSH(ctx context.Context, cfg dialConfig) (*ssh.Client, []*ssh.Client, error) {
	timeout := cfg.dialTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	base := cfg.dialer
	if base == nil {
		base = netclient.DialerFunc((&net.Dialer{Timeout: timeout}).DialContext)
	}

	var hops []*ssh.Client
	// dialThrough dials addr using either the base transport (first hop) or the
	// previous SSH hop's Dial.
	dialThrough := func(prev *ssh.Client, addr string) (net.Conn, error) {
		if prev == nil {
			dctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return base.DialContext(dctx, "tcp", addr)
		}
		return prev.Dial("tcp", addr)
	}

	var prev *ssh.Client
	// Resolve and connect each jump host in order.
	for i, jump := range cfg.host.ProxyJump {
		ju, jhost, jport, err := ParseTarget(jump)
		if err != nil {
			closeAll(hops)
			return nil, nil, fmt.Errorf("proxy jump %q: %w", jump, err)
		}
		hop := ResolvedHost{Name: jump, HostName: jhost, Port: jport, User: ju}
		applyHostDefaults(&hop)
		conn, derr := dialThrough(prev, hop.Addr())
		if derr != nil {
			closeAll(hops)
			return nil, nil, fmt.Errorf("proxy jump %d (%s): %w", i+1, hop.Label(), derr)
		}
		// Jump hosts reuse the same auth material and host-key policy.
		client, cerr := newSSHClient(ctx, conn, hop, cfg)
		if cerr != nil {
			closeAll(hops)
			return nil, nil, fmt.Errorf("proxy jump %d (%s): %w", i+1, hop.Label(), cerr)
		}
		hops = append(hops, client)
		prev = client
	}

	conn, err := dialThrough(prev, cfg.host.Addr())
	if err != nil {
		closeAll(hops)
		return nil, nil, fmt.Errorf("dial %s: %w", cfg.host.Label(), err)
	}
	target, err := newSSHClient(ctx, conn, cfg.host, cfg)
	if err != nil {
		closeAll(hops)
		return nil, nil, err
	}
	return target, hops, nil
}

func newSSHClient(ctx context.Context, conn net.Conn, host ResolvedHost, cfg dialConfig) (*ssh.Client, error) {
	methods, err := buildAuthMethods(ctx, host, cfg.auth)
	if err != nil {
		conn.Close()
		return nil, err
	}
	hkCallback, err := cfg.hostKeys.Callback(ctx, host.Label())
	if err != nil {
		conn.Close()
		return nil, err
	}
	clientCfg := &ssh.ClientConfig{
		User:            host.User,
		Auth:            methods,
		HostKeyCallback: hkCallback,
		Timeout:         cfg.dialTimeout,
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, host.Addr(), clientCfg)
	if err != nil {
		conn.Close()
		return nil, classifyDialError(err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func closeAll(clients []*ssh.Client) {
	for i := len(clients) - 1; i >= 0; i-- {
		_ = clients[i].Close()
	}
}
