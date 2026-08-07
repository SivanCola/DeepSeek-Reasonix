package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/intent/corpus"
)

// This is the live accuracy harness for a model-backed Classifier. It answers
// the only question that decides whether the keyword tables can be deleted:
// does a small model reproduce the golden corpus at least as well?
//
// It is skipped unless credentials are supplied, so ordinary `go test ./...`
// and CI stay hermetic and free:
//
//	REASONIX_INTENT_EVAL_KEY=<key> \
//	REASONIX_INTENT_EVAL_BASE_URL=https://... \
//	REASONIX_INTENT_EVAL_MODEL=<model> \
//	go test ./internal/intent/ -run TestLiveClassifierAccuracy -v -timeout 30m
//
// The key is read from the environment and never logged.

type liveClassifier struct {
	baseURL string
	key     string
	model   string
	client  *http.Client
}

// Classify retries once on an empty reply. Reasoning models on this endpoint
// intermittently return an empty content field with the whole budget spent in
// reasoning_content; measured at roughly 3% of calls, which is too high a
// degradation rate for a hot-path gate to absorb silently.
func (c *liveClassifier) Classify(ctx context.Context, text string) (TurnIntent, error) {
	got, err := c.classifyOnce(ctx, text)
	if err != nil && strings.Contains(err.Error(), "empty reply") {
		return c.classifyOnce(ctx, text)
	}
	return got, err
}

func (c *liveClassifier) classifyOnce(ctx context.Context, text string) (TurnIntent, error) {
	if strings.TrimSpace(text) == "" {
		return TurnIntent{Kind: KindConversation}, nil
	}
	body, err := json.Marshal(map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": SystemPrompt},
			{"role": "user", "content": text},
		},
		"max_tokens":  maxClassifyTokens,
		"temperature": 0,
	})
	if err != nil {
		return TurnIntent{}, err
	}
	url := strings.TrimSuffix(c.baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return TurnIntent{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return TurnIntent{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return TurnIntent{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return TurnIntent{}, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return TurnIntent{}, err
	}
	if len(envelope.Choices) == 0 {
		return TurnIntent{}, fmt.Errorf("no choices")
	}

	return ParseClassifierReply(envelope.Choices[0].Message.Content)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

type caseResult struct {
	c       corpus.Case
	got     TurnIntent
	err     error
	misses  []string
	latency time.Duration
}

func TestLiveClassifierAccuracy(t *testing.T) {
	key := os.Getenv("REASONIX_INTENT_EVAL_KEY")
	if key == "" {
		t.Skip("REASONIX_INTENT_EVAL_KEY not set; skipping live accuracy evaluation")
	}
	baseURL := os.Getenv("REASONIX_INTENT_EVAL_BASE_URL")
	if baseURL == "" {
		t.Fatal("REASONIX_INTENT_EVAL_BASE_URL is required")
	}
	model := os.Getenv("REASONIX_INTENT_EVAL_MODEL")
	if model == "" {
		t.Fatal("REASONIX_INTENT_EVAL_MODEL is required")
	}

	cls := &liveClassifier{
		baseURL: baseURL,
		key:     key,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}

	cases := corpus.All()
	results := make([]caseResult, len(cases))

	const parallel = 6
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, c := range cases {
		wg.Add(1)
		go func(i int, c corpus.Case) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			start := time.Now()
			got, err := cls.Classify(ctx, c.Text)
			results[i] = caseResult{c: c, got: got, err: err, latency: time.Since(start)}
		}(i, c)
	}
	wg.Wait()

	var (
		total, correct, errored int
		totalLatency            time.Duration
		byLabel                 = map[string][2]int{} // label -> [correct, total]
		failures                []caseResult
	)

	for i := range results {
		r := &results[i]
		if r.err != nil {
			errored++
			continue
		}
		totalLatency += r.latency

		check := func(label string, want *bool, got bool) {
			if want == nil {
				return
			}
			total++
			agg := byLabel[label]
			agg[1]++
			if *want == got {
				correct++
				agg[0]++
			} else {
				r.misses = append(r.misses, fmt.Sprintf("%s want=%v got=%v", label, *want, got))
			}
			byLabel[label] = agg
		}

		check("NeedsEvidence", r.c.NeedsEvidence, r.got.NeedsEvidence())
		check("NeedsMutation", r.c.NeedsMutation, r.got.NeedsMutation())
		check("IsTask", r.c.IsTask, r.got.IsTask())
		check("NeedsWriteBudget", r.c.NeedsWriteBudget, r.got.NeedsGoalWriteBudget())

		if len(r.misses) > 0 {
			failures = append(failures, *r)
		}
	}

	t.Logf("model=%s cases=%d errored=%d", model, len(cases), errored)
	if n := len(cases) - errored; n > 0 {
		t.Logf("mean latency: %v", totalLatency/time.Duration(n))
	}
	t.Logf("label accuracy: %d/%d = %.1f%%", correct, total, 100*float64(correct)/float64(total))

	labels := make([]string, 0, len(byLabel))
	for k := range byLabel {
		labels = append(labels, k)
	}
	sort.Strings(labels)
	for _, l := range labels {
		agg := byLabel[l]
		t.Logf("  %-18s %3d/%3d = %5.1f%%", l, agg[0], agg[1], 100*float64(agg[0])/float64(agg[1]))
	}

	sort.Slice(failures, func(i, j int) bool { return failures[i].c.Origin < failures[j].c.Origin })
	if len(failures) > 0 {
		t.Logf("\n%d case(s) disagree with the golden corpus:", len(failures))
		for _, f := range failures {
			t.Logf("  [%s] %q\n      kind=%s fault=%v readonly=%v diagnostic=%v durable=%v\n      %s",
				f.c.Origin, f.c.Text, f.got.Kind, f.got.FaultReport, f.got.ReadOnlyConstraint,
				f.got.DiagnosticIntent, f.got.DurableScope, strings.Join(f.misses, "; "))
		}
	}
	for i := range results {
		if results[i].err != nil {
			t.Logf("ERROR %q: %v", results[i].c.Text, results[i].err)
		}
	}
}
