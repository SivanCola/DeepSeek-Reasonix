package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ResultRecorder retains a lossless tool result outside the model transcript.
type ResultRecorder func(json.RawMessage) (string, error)

type resultRecorderKey struct{}
type resultReaderKey struct{}
type ResultReader func(string) (io.ReadCloser, error)

const ResultReferencePrefix = "tool-result:"

func IsResultReference(ref string) bool { return strings.HasPrefix(ref, ResultReferencePrefix) }

func WithResultReader(ctx context.Context, read ResultReader) context.Context {
	return context.WithValue(ctx, resultReaderKey{}, read)
}

func OpenResult(ctx context.Context, ref string) (io.ReadCloser, error) {
	read, _ := ctx.Value(resultReaderKey{}).(ResultReader)
	if read == nil {
		return nil, fmt.Errorf("tool result is unavailable in this session")
	}
	return read(ref)
}

func WithResultRecorder(ctx context.Context, record ResultRecorder) context.Context {
	return context.WithValue(ctx, resultRecorderKey{}, record)
}

func RecordResult(ctx context.Context, raw json.RawMessage) (string, error) {
	record, _ := ctx.Value(resultRecorderKey{}).(ResultRecorder)
	if record == nil {
		return "", nil
	}
	return record(append(json.RawMessage(nil), raw...))
}
