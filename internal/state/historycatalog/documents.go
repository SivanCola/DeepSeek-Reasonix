package historycatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"reasonix/internal/base/retrieval"
	"reasonix/internal/contract/provider"
	"reasonix/internal/state/sessionstore"
)

type indexedDocument struct {
	message int
	part    int
	role    string
	kind    string
	tool    string
	terms   string
	count   int
}

func documentDigest(docs []indexedDocument) string {
	h := sha256.New()
	for _, doc := range docs {
		_, _ = h.Write([]byte(doc.terms))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func documents(messages []provider.Message) []indexedDocument {
	out, _ := documentsContext(context.Background(), messages)
	return out
}

func documentsContext(ctx context.Context, messages []provider.Message) ([]indexedDocument, error) {
	out := []indexedDocument{}
	appendDoc := func(message, part int, role, kind, tool, text string) {
		terms := retrieval.Tokens(strings.TrimSpace(text))
		if len(terms) == 0 {
			return
		}
		out = append(out, indexedDocument{message: message, part: part, role: role, kind: kind, tool: tool, terms: strings.Join(terms, " "), count: len(terms)})
	}
	for i, msg := range messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch msg.Role {
		case provider.RoleUser:
			appendDoc(i, 0, string(msg.Role), "user_text", "", sessionstore.UserSaidText(msg.Content))
		case provider.RoleAssistant:
			appendDoc(i, 0, string(msg.Role), "assistant_text", "", msg.Content)
			for part, call := range msg.ToolCalls {
				appendDoc(i, part, string(msg.Role), "tool_input", call.Name, call.Name+" "+call.Arguments)
			}
		case provider.RoleTool:
			// Index both tool_error and tool_output so explicit kind filters stay
			// honest. Default search kinds still exclude tool_output.
			text := msg.Name + " " + msg.Content
			lower := strings.ToLower(strings.TrimSpace(msg.Content))
			if strings.HasPrefix(lower, "error:") || strings.HasPrefix(lower, "blocked:") || strings.Contains(lower, "permission denied") {
				appendDoc(i, 0, string(msg.Role), "tool_error", msg.Name, text)
			}
			appendDoc(i, 0, string(msg.Role), "tool_output", msg.Name, text)
		}
	}
	return out, nil
}
