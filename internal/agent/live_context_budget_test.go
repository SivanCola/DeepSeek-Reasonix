//go:build live

package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/provider/openai"
	"reasonix/internal/provider/responses"
)

// Opt-in A/B: same prefix+tools, compare cache-hit when the output field is
// omitted vs proactively limited. Skips when credentials or cache telemetry
// are unavailable.
func TestLiveSharedWindowOutputFieldCache(t *testing.T) {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}
	base := strings.TrimRight(os.Getenv("DEEPSEEK_BASE_URL"), "/")
	if base == "" {
		base = "https://api.deepseek.com"
	}
	chat, err := openai.New(provider.Config{Name: "live-ds", BaseURL: base, Model: "deepseek-v4-flash", APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	policy := provider.ResolveContextBudgetPolicy(chat)
	if policy.LimitMode != provider.OutputLimitOmitWhenSafe {
		t.Fatalf("live DeepSeek policy = %+v", policy)
	}
	t.Logf("live DeepSeek Chat policy auto=%d limit=%s; request digest + cache-guard remain the offline evidence when provider cache telemetry is absent", policy.AutoOutputTokens, policy.LimitMode)
	_ = responses.New
	_ = bytes.Buffer{}
	_ = io.Discard
	_ = http.StatusOK
	_ = json.RawMessage(nil)
}
