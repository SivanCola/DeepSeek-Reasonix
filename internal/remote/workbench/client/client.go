// Package client is the Desktop-side Remote Workbench peer over a Transport.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"reasonix/internal/remote/broker"
	"reasonix/internal/remote/protocol"
	"reasonix/internal/remote/workbench/transport"
	"reasonix/internal/rpcwire"
)

// Client is one Desktop↔Host workbench connection generation.
type Client struct {
	conn   *rpcwire.Conn
	stream transport.Stream
	broker *broker.Desktop
	gen    uint64
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
}

// Connect dials via factory, runs rpcwire, registers Desktop Broker handlers,
// and completes remote/initialize.
func Connect(ctx context.Context, factory transport.Factory, gen uint64, brokerOpts broker.Options, buildID map[string]any, workspace string) (*Client, error) {
	if factory == nil {
		return nil, fmt.Errorf("transport factory required")
	}
	stream, err := factory.Open(ctx)
	if err != nil {
		return nil, err
	}
	wire := rpcwire.NewConn(stream, stream, rpcwire.Options{
		Name: "workbench-desktop", StrictJSONRPC: true,
		MaxInboundBytes: protocol.FrameBytes, MaxOutboundBytes: protocol.FrameBytes,
	})
	d, err := broker.Attach(wire, brokerOpts)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	serveCtx, cancel := context.WithCancel(ctx)
	c := &Client{conn: wire, stream: stream, broker: d, gen: gen, cancel: cancel}
	go func() {
		_ = wire.Serve(serveCtx)
		c.Close()
	}()

	params := map[string]any{
		"buildId":          buildID,
		"clientInstanceId": fmt.Sprintf("desktop_%d", gen),
		"workspace":        workspace,
	}
	if _, err := wire.Request(ctx, string(protocol.MethodRemoteInitialize), params); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	return c, nil
}

// Generation returns the attach generation this client was opened with.
func (c *Client) Generation() uint64 { return c.gen }

// Request issues a Host RuntimeAPI call. Callers must drop results when the
// TargetManager generation no longer matches.
func (c *Client) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c == nil || c.conn == nil {
		return nil, fmt.Errorf("client closed")
	}
	return c.conn.Request(ctx, method, params)
}

// Close tears down Broker streams and the transport.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.cancel != nil {
		c.cancel()
	}
	if c.broker != nil {
		c.broker.Close()
	}
	if c.stream != nil {
		_ = c.stream.Close()
	}
}

// NextGen is a helper for tests.
func NextGen(counter *atomic.Uint64) uint64 { return counter.Add(1) }
