package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/remote/protocol"
	"reasonix/internal/rpcwire"
)

type pipePair struct {
	clientR io.Reader
	clientW io.Writer
	hostR   io.Reader
	hostW   io.Writer
}

func newPipePair() pipePair {
	c2hR, c2hW := io.Pipe()
	h2cR, h2cW := io.Pipe()
	return pipePair{
		clientR: h2cR,
		clientW: c2hW,
		hostR:   c2hR,
		hostW:   h2cW,
	}
}

type stubProvider struct {
	chunks []provider.Chunk
}

func (s stubProvider) Name() string { return "stub" }
func (s stubProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, len(s.chunks))
	go func() {
		defer close(ch)
		for _, c := range s.chunks {
			select {
			case <-ctx.Done():
				return
			case ch <- c:
			}
		}
	}()
	return ch, nil
}
func (s stubProvider) RequiresToolCallReasoning() bool { return true }

func TestDesktopBrokerCatalogAndStreamRoundTrip(t *testing.T) {
	pipes := newPipePair()
	desktopConn := rpcwire.NewConn(pipes.clientR, pipes.clientW, rpcwire.Options{
		Name: "desktop", StrictJSONRPC: true, MaxInboundBytes: 1 << 20, MaxOutboundBytes: 1 << 20,
	})
	hostConn := rpcwire.NewConn(pipes.hostR, pipes.hostW, rpcwire.Options{
		Name: "host", StrictJSONRPC: true, MaxInboundBytes: 1 << 20, MaxOutboundBytes: 1 << 20,
	})

	var gotChunks []provider.Chunk
	var end protocol.BrokerStreamEndParams
	var mu sync.Mutex
	hostConn.HandleNotify(string(protocol.MethodBrokerStreamChunk), func(ctx context.Context, params json.RawMessage) {
		var p protocol.BrokerStreamChunkParams
		if err := json.Unmarshal(params, &p); err != nil {
			t.Errorf("chunk params: %v", err)
			return
		}
		var c wireChunk
		if err := json.Unmarshal(p.Chunk, &c); err != nil {
			t.Errorf("chunk body: %v", err)
			return
		}
		mu.Lock()
		gotChunks = append(gotChunks, provider.Chunk{Type: c.Type, Text: c.Text})
		mu.Unlock()
	})
	hostConn.HandleNotify(string(protocol.MethodBrokerStreamEnd), func(ctx context.Context, params json.RawMessage) {
		_ = json.Unmarshal(params, &end)
	})

	prov := stubProvider{chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "hi"},
		{Type: provider.ChunkText, Text: " there"},
	}}
	d, err := Attach(desktopConn, Options{
		Catalog: func(ctx context.Context, allowed map[string]struct{}) ([]protocol.BrokerProviderDescriptor, error) {
			return []protocol.BrokerProviderDescriptor{
				DescriptorFromProvider("deepseek/chat", "DeepSeek", "chat", prov, nil, "", false),
			}, nil
		},
		Open: func(ctx context.Context, ref string, req provider.Request) (<-chan provider.Chunk, error) {
			if ref != "deepseek/chat" {
				t.Fatalf("ref = %q", ref)
			}
			// Request must round-trip byte-equivalent for cache safety.
			return prov.Stream(ctx, req)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go desktopConn.Serve(ctx)
	go hostConn.Serve(ctx)

	// Catalog.
	raw, err := hostConn.Request(ctx, string(protocol.MethodBrokerCatalog), protocol.BrokerCatalogParams{})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	var cat protocol.BrokerCatalogResult
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatal(err)
	}
	if len(cat.Providers) != 1 || !cat.Providers[0].ToolCallReasoning {
		t.Fatalf("catalog = %+v", cat.Providers)
	}

	// Stream with a structured request that must survive JSON marshal.
	req := provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hello"}},
	}
	reqRaw, _ := json.Marshal(req)
	// Ensure re-marshal is stable for the test assertion path.
	var req2 provider.Request
	_ = json.Unmarshal(reqRaw, &req2)
	again, _ := json.Marshal(req2)
	if !bytes.Equal(reqRaw, again) {
		t.Fatalf("provider.Request not stable under marshal")
	}

	openRaw, err := hostConn.Request(ctx, string(protocol.MethodBrokerStreamOpen), protocol.BrokerStreamOpenParams{
		StreamID: "s1", ProviderRef: "deepseek/chat", Request: reqRaw,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var open protocol.BrokerStreamOpenResult
	if err := json.Unmarshal(openRaw, &open); err != nil || !open.Accepted {
		t.Fatalf("open result = %s err=%v", openRaw, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(gotChunks)
		mu.Unlock()
		if n >= 2 && end.StreamID == "s1" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotChunks) != 2 {
		t.Fatalf("chunks = %d", len(gotChunks))
	}
	if gotChunks[0].Text+gotChunks[1].Text != "hi there" {
		t.Fatalf("text = %q%q", gotChunks[0].Text, gotChunks[1].Text)
	}
	if end.Interrupted || end.Error != "" {
		t.Fatalf("end = %+v", end)
	}
}

func TestDesktopBrokerCancelsStream(t *testing.T) {
	pipes := newPipePair()
	desktopConn := rpcwire.NewConn(pipes.clientR, pipes.clientW, rpcwire.Options{
		Name: "desktop", StrictJSONRPC: true, MaxInboundBytes: 1 << 20, MaxOutboundBytes: 1 << 20,
	})
	hostConn := rpcwire.NewConn(pipes.hostR, pipes.hostW, rpcwire.Options{
		Name: "host", StrictJSONRPC: true, MaxInboundBytes: 1 << 20, MaxOutboundBytes: 1 << 20,
	})

	started := make(chan struct{})
	d, err := Attach(desktopConn, Options{
		Catalog: func(context.Context, map[string]struct{}) ([]protocol.BrokerProviderDescriptor, error) {
			return nil, nil
		},
		Open: func(ctx context.Context, ref string, req provider.Request) (<-chan provider.Chunk, error) {
			ch := make(chan provider.Chunk)
			go func() {
				defer close(ch)
				close(started)
				<-ctx.Done()
			}()
			return ch, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go desktopConn.Serve(ctx)
	go hostConn.Serve(ctx)

	reqRaw, _ := json.Marshal(provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}}})
	if _, err := hostConn.Request(ctx, string(protocol.MethodBrokerStreamOpen), protocol.BrokerStreamOpenParams{
		StreamID: "c1", ProviderRef: "stub/m", Request: reqRaw,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}
	raw, err := hostConn.Request(ctx, string(protocol.MethodBrokerStreamCancel), protocol.BrokerStreamCancelParams{StreamID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	var res protocol.BrokerStreamCancelResult
	if err := json.Unmarshal(raw, &res); err != nil || !res.Cancelled {
		t.Fatalf("cancel = %s", raw)
	}
}
