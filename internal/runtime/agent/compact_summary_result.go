package agent

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/contract/provider"
)

func summaryAtEOF(ctx context.Context, text string, usage *provider.Usage, maxTokens int, reused bool) (string, error) {
	// Providers also close their stream on cancellation; EOF alone cannot
	// distinguish a finished digest from a partial one at the request deadline.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if usage != nil && usage.FinishReason == "length" {
		return "", &summaryTruncation{Cap: maxTokens}
	}
	s := strings.TrimSpace(text)
	if s == "" {
		return "", fmt.Errorf("summarizer returned empty output")
	}
	if reused && !hasDigestHeading(s) {
		return "", errSummaryPrefixNotDigest
	}
	return s, nil
}
