// Package broker implements the Desktop side of the bidirectional Provider
// Broker over rpcwire. Host → Desktop requests open catalog/stream; Desktop →
// Host notifications deliver chunks. API keys never leave this process.
package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"reasonix/internal/provider"
	"reasonix/internal/remote/protocol"
	"reasonix/internal/rpcwire"
)

// CatalogSource returns non-secret descriptors authorized for a Host scope.
type CatalogSource func(ctx context.Context, allowed map[string]struct{}) ([]protocol.BrokerProviderDescriptor, error)

// StreamOpener resolves a provider ref and starts a stream. Implementations
// must use the local Provider (keys on Desktop only).
type StreamOpener func(ctx context.Context, ref string, req provider.Request) (<-chan provider.Chunk, error)

// Desktop is the Desktop-side Broker endpoint bound to one SSH/rpcwire connection.
type Desktop struct {
	conn    *rpcwire.Conn
	catalog CatalogSource
	open    StreamOpener

	mu      sync.Mutex
	streams map[string]*streamState
	// maxConcurrent bounds simultaneous provider streams per connection.
	maxConcurrent int
	// gen increments on catalog-changed notifications.
	gen atomic.Int64
	// closed is closed when the connection is torn down.
	closed chan struct{}
}

type streamState struct {
	cancel context.CancelFunc
	seq    atomic.Int64
}

// Options configures a Desktop Broker endpoint.
type Options struct {
	Catalog       CatalogSource
	Open          StreamOpener
	MaxConcurrent int
}

// Attach registers Host-request handlers on conn and returns the Desktop Broker.
func Attach(conn *rpcwire.Conn, opts Options) (*Desktop, error) {
	if conn == nil {
		return nil, fmt.Errorf("broker: nil conn")
	}
	if opts.Catalog == nil || opts.Open == nil {
		return nil, fmt.Errorf("broker: catalog and open are required")
	}
	max := opts.MaxConcurrent
	if max <= 0 {
		max = 4
	}
	d := &Desktop{
		conn:          conn,
		catalog:       opts.Catalog,
		open:          opts.Open,
		streams:       map[string]*streamState{},
		maxConcurrent: max,
		closed:        make(chan struct{}),
	}
	conn.Handle(string(protocol.MethodBrokerCatalog), d.handleCatalog)
	conn.Handle(string(protocol.MethodBrokerStreamOpen), d.handleStreamOpen)
	conn.Handle(string(protocol.MethodBrokerStreamCancel), d.handleStreamCancel)
	return d, nil
}

// Close cancels all streams for this connection.
func (d *Desktop) Close() {
	select {
	case <-d.closed:
		return
	default:
		close(d.closed)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, st := range d.streams {
		st.cancel()
		delete(d.streams, id)
	}
}

// NotifyCatalogChanged pushes a generation bump after re-authorization.
func (d *Desktop) NotifyCatalogChanged() error {
	gen := d.gen.Add(1)
	return d.conn.Notify(string(protocol.MethodBrokerCatalogChanged), protocol.BrokerCatalogChangedParams{
		Generation: gen,
	})
}

func (d *Desktop) handleCatalog(ctx context.Context, params json.RawMessage) (any, error) {
	var p protocol.BrokerCatalogParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInvalidParams, Message: "invalid catalog params"}
	}
	allowed := map[string]struct{}{}
	for _, ref := range p.AllowedRefs {
		if ref = trim(ref); ref != "" {
			allowed[ref] = struct{}{}
		}
	}
	list, err := d.catalog(ctx, allowed)
	if err != nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInternal, Message: redactErr(err)}
	}
	if list == nil {
		list = []protocol.BrokerProviderDescriptor{}
	}
	return protocol.BrokerCatalogResult{Providers: list}, nil
}

func (d *Desktop) handleStreamOpen(ctx context.Context, params json.RawMessage) (any, error) {
	var p protocol.BrokerStreamOpenParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInvalidParams, Message: "invalid stream open params"}
	}
	if err := p.Validate(); err != nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInvalidParams, Message: err.Error()}
	}
	var req provider.Request
	if err := json.Unmarshal(p.Request, &req); err != nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInvalidParams, Message: "invalid provider request"}
	}

	d.mu.Lock()
	if len(d.streams) >= d.maxConcurrent {
		d.mu.Unlock()
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInternal, Message: "broker stream concurrency limit"}
	}
	if _, exists := d.streams[p.StreamID]; exists {
		d.mu.Unlock()
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInvalidParams, Message: "streamId already in use"}
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	st := &streamState{cancel: cancel}
	d.streams[p.StreamID] = st
	d.mu.Unlock()

	ch, err := d.open(streamCtx, p.ProviderRef, req)
	if err != nil {
		d.finishStream(p.StreamID)
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInternal, Message: redactErr(err)}
	}
	go d.pump(p.StreamID, st, ch)
	return protocol.BrokerStreamOpenResult{Accepted: true}, nil
}

func (d *Desktop) handleStreamCancel(ctx context.Context, params json.RawMessage) (any, error) {
	var p protocol.BrokerStreamCancelParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcwire.RPCError{Code: rpcwire.ErrInvalidParams, Message: "invalid cancel params"}
	}
	d.mu.Lock()
	st, ok := d.streams[p.StreamID]
	d.mu.Unlock()
	if ok {
		st.cancel()
	}
	return protocol.BrokerStreamCancelResult{Cancelled: ok}, nil
}

func (d *Desktop) pump(streamID string, st *streamState, ch <-chan provider.Chunk) {
	defer d.finishStream(streamID)
	for {
		select {
		case <-d.closed:
			_ = d.notifyEnd(streamID, "connection closed", true)
			return
		case chunk, ok := <-ch:
			if !ok {
				_ = d.notifyEnd(streamID, "", false)
				return
			}
			seq := st.seq.Add(1)
			raw, err := marshalChunk(chunk)
			if err != nil {
				_ = d.notifyEnd(streamID, "chunk marshal failed", false)
				return
			}
			err = d.conn.Notify(string(protocol.MethodBrokerStreamChunk), protocol.BrokerStreamChunkParams{
				StreamID: streamID,
				Seq:      seq,
				Chunk:    raw,
			})
			if err != nil {
				return
			}
		}
	}
}

func (d *Desktop) notifyEnd(streamID, errMsg string, interrupted bool) error {
	return d.conn.Notify(string(protocol.MethodBrokerStreamEnd), protocol.BrokerStreamEndParams{
		StreamID:    streamID,
		Error:       errMsg,
		Interrupted: interrupted,
	})
}

// wireChunk is a JSON-safe view of provider.Chunk (error is a string).
type wireChunk struct {
	Type      provider.ChunkType `json:"type"`
	Text      string             `json:"text,omitempty"`
	Signature string             `json:"signature,omitempty"`
	ToolCall  *provider.ToolCall `json:"toolCall,omitempty"`
	ArgChars  int                `json:"argChars,omitempty"`
	Usage     *provider.Usage    `json:"usage,omitempty"`
	Err       string             `json:"err,omitempty"`
}

func marshalChunk(c provider.Chunk) (json.RawMessage, error) {
	w := wireChunk{
		Type: c.Type, Text: c.Text, Signature: c.Signature,
		ToolCall: c.ToolCall, ArgChars: c.ArgChars, Usage: c.Usage,
	}
	if c.Err != nil {
		w.Err = redactErr(c.Err)
	}
	return json.Marshal(w)
}

func (d *Desktop) finishStream(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if st, ok := d.streams[id]; ok {
		st.cancel()
		delete(d.streams, id)
	}
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// redactErr returns a safe, non-secret error string for RPC.
func redactErr(err error) string {
	if err == nil {
		return "error"
	}
	msg := err.Error()
	// Never echo values that look like keys.
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}

// DescriptorFromProvider builds a non-secret catalog row from a live Provider
// and a configured model ref. Does not include base URLs or credentials.
func DescriptorFromProvider(ref, display, model string, p provider.Provider, efforts []string, defaultEffort string, vision bool) protocol.BrokerProviderDescriptor {
	return protocol.BrokerProviderDescriptor{
		Ref:                            ref,
		DisplayName:                    display,
		Model:                          model,
		SupportsVision:                 vision,
		SupportedEfforts:               append([]string(nil), efforts...),
		DefaultEffort:                  defaultEffort,
		ToolCallReasoning:              provider.RequiresToolCallReasoning(p),
		WarnOnMissingToolCallReasoning: provider.WarnOnMissingToolCallReasoning(p),
	}
}
