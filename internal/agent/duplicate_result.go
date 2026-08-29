package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func (a *Agent) boundProviderVisibleResult(raw, toolName, callID string) (body, notice, original string) {
	summarized := summarizeCIOutput(raw)
	body, notice = truncateToolOutputFor(summarized, toolName, callID)
	body = a.dedupeProviderVisibleResult(callID, body)
	if summarized != raw || notice != "" {
		original = raw
	}
	return body, notice, original
}

func (a *Agent) dedupeProviderVisibleResult(callID, output string) string {
	if a == nil || strings.TrimSpace(output) == "" {
		return output
	}
	sum := sha256.Sum256([]byte(output))
	fp := hex.EncodeToString(sum[:12])
	prev, seen := a.turn.loop.rememberFingerprint(fp, callID)
	if seen && prev != callID {
		return fmt.Sprintf("duplicate tool result omitted (identical to call_id=%s, fingerprint=%s). Full original remains locally; page it with session:tool_result if needed.", prev, fp)
	}
	return output
}
