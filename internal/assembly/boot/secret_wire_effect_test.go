package boot

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/base/secrets/secretstest"
	"reasonix/internal/contract/config"
	"reasonix/internal/contract/provider"
	_ "reasonix/internal/model/responses"
)

// Every wire a provider entry may speak masks a value the host holds for the
// person in the bytes it puts on the wire, whichever path built the request:
// the agent loop, a title, a memory review or an advisor all reach a model
// through one of these. A kind absent from the protocol table cannot be saved.
func TestEveryProviderKindMasksSecretsOnTheWire(t *testing.T) {
	const value = "sk-wire-0123456789abcdef"
	secretstest.Hold("WIRE_KEY", value)
	for _, proto := range config.Protocols() {
		if proto.Answers != config.AnswersChat {
			continue
		}
		kind := proto.Kind
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, string(data))
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"captured","type":"invalid_request_error"}}`))
			}))
			defer srv.Close()
			p, err := newProviderForTest(&config.ProviderEntry{Name: "wire", Kind: kind, BaseURL: srv.URL, Model: "m"})
			if err != nil {
				t.Fatalf("kind %s does not build from a bare entry: %v", kind, err)
			}
			ch, err := p.Stream(context.Background(), provider.Request{Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "you hold " + value},
				{Role: provider.RoleUser, Content: "use " + value},
			}})
			if err == nil {
				for range ch {
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(bodies) == 0 {
				t.Fatalf("kind %s sent nothing (err %v)", kind, err)
			}
			for _, b := range bodies {
				if strings.Contains(b, value) || !strings.Contains(b, "[secret:WIRE_KEY]") {
					t.Fatalf("kind %s put %s on the wire", kind, b)
				}
			}
		})
	}
}
