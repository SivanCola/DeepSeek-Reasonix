package builtin

import (
	"context"
	"encoding/json"

	"reasonix/internal/base/secrets"
	"reasonix/internal/contract/tool"
)

// maskedProgress masks what a shell tool streams to the window with the same
// snapshot that masks what it returns. A value split across two chunks is not
// recognised in either; what the tool returns is masked whole.
func maskedProgress(ctx context.Context, m *secrets.Mask) context.Context {
	if m == nil {
		return ctx
	}
	if emit, ok := tool.ProgressFrom(ctx); ok {
		return tool.WithProgress(ctx, func(chunk string) { emit(m.String(chunk)) })
	}
	return ctx
}

// maskedShell runs a tool that reads a shell's output under one snapshot of the
// values the host holds for the person.
func maskedShell(ctx context.Context, args json.RawMessage, run func(context.Context, json.RawMessage) (string, error)) (string, error) {
	m := secrets.Masker()
	out, err := run(maskedProgress(ctx, m), args)
	return m.String(out), err
}
