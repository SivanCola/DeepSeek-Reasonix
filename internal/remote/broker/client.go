package broker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"reasonix/internal/provider"
)

// Client is a remote-side ProviderResolver that calls a loopback broker through
// the SSH reverse tunnel (baseURL is typically http://127.0.0.1:<port>).
type Client struct {
	BaseURL    string
	Token      CapabilityToken
	HTTPClient *http.Client
}

// Catalog implements provider.Resolver.
func (c *Client) Catalog() []provider.Descriptor {
	if c == nil {
		return nil
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(c.BaseURL, "/")+APIPrefix+"/catalog", nil)
	if err != nil {
		return nil
	}
	c.authorize(req)
	resp, err := c.http().Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var body catalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil
	}
	return body.Providers
}

// Resolve implements provider.Resolver by returning a streaming proxy provider.
func (c *Client) Resolve(selection provider.Selection) (provider.Provider, error) {
	if c == nil {
		return nil, fmt.Errorf("broker client is nil")
	}
	ref := strings.TrimSpace(selection.Ref)
	if ref == "" {
		return nil, fmt.Errorf("provider selection ref is required")
	}
	p := &remoteProvider{client: c, ref: ref, effort: selection.Effort}
	// Propagate non-secret protocol policy from the catalog so Agent request
	// construction (e.g. DeepSeek tool-call reasoning replay) matches local mode.
	for _, d := range c.Catalog() {
		if d.Ref == ref || strings.HasPrefix(d.Ref, ref+"/") || strings.HasSuffix(d.Ref, "/"+ref) {
			p.toolCallReasoning = d.ToolCallReasoning
			break
		}
	}
	return p, nil
}

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("X-Reasonix-Broker-Token", c.Token.String())
	req.Header.Set("Authorization", "Bearer "+c.Token.String())
}

func (c *Client) http() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 0} // stream-controlled by context
}

// remoteProvider implements provider.Provider over the broker NDJSON stream.
type remoteProvider struct {
	client            *Client
	ref               string
	effort            *string
	toolCallReasoning bool
}

func (p *remoteProvider) Name() string {
	// Use the stable ref prefix so UI labels stay meaningful without leaking host info.
	if i := strings.IndexByte(p.ref, '/'); i > 0 {
		return p.ref[:i]
	}
	return p.ref
}

// RequiresToolCallReasoning mirrors the local provider policy advertised in the
// catalog (DeepSeek thinking mode). Without this, Agent request construction
// drops reasoning_content on tool_calls turns under Broker mode.
func (p *remoteProvider) RequiresToolCallReasoning() bool {
	return p != nil && p.toolCallReasoning
}

func (p *remoteProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	body := streamRequest{ProviderRef: p.ref, Effort: p.effort, Request: req}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.client.BaseURL, "/")+APIPrefix+"/stream", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	p.client.authorize(httpReq)

	resp, err := p.client.http().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("broker stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		var er struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&er)
		if er.Code == "" {
			er.Code = "broker_error"
		}
		if er.Message == "" {
			er.Message = resp.Status
		}
		return nil, fmt.Errorf("%s: %s", er.Code, er.Message)
	}

	out := make(chan provider.Chunk, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		// Provider tool args can be large.
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 8<<20)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var wire streamChunkWire
			if err := json.Unmarshal(line, &wire); err != nil {
				select {
				case out <- provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("broker decode: %w", err)}:
				case <-ctx.Done():
				}
				return
			}
			chunk := wireToChunk(wire)
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
			if chunk.Type == provider.ChunkError {
				return
			}
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			select {
			case out <- provider.Chunk{Type: provider.ChunkError, Err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return out, nil
}

func wireToChunk(w streamChunkWire) provider.Chunk {
	c := provider.Chunk{
		Type:      w.Type,
		Text:      w.Text,
		Signature: w.Signature,
		ArgChars:  w.ArgChars,
	}
	if len(w.ToolCall) > 0 {
		var tc provider.ToolCall
		if err := json.Unmarshal(w.ToolCall, &tc); err == nil {
			c.ToolCall = &tc
		}
	}
	if len(w.Usage) > 0 {
		var u provider.Usage
		if err := json.Unmarshal(w.Usage, &u); err == nil {
			c.Usage = &u
		}
	}
	if w.Error != "" {
		c.Type = provider.ChunkError
		c.Err = fmt.Errorf("%s", w.Error)
	}
	return c
}

// WaitReady polls the broker health endpoint until ready or timeout.
func WaitReady(ctx context.Context, baseURL string, token CapabilityToken) error {
	client := &http.Client{Timeout: 2 * time.Second}
	url := strings.TrimRight(baseURL, "/") + APIPrefix + "/health"
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Reasonix-Broker-Token", token.String())
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
