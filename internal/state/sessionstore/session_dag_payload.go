package sessionstore

import (
	"encoding/json"
	"fmt"
	"io"

	"reasonix/internal/contract/provider"
)

type sessionDAGPayload struct {
	start int64
	end   int64
	role  provider.Role
}

func (st *sessionDAGState) payload(raw json.RawMessage, ref sessionDAGPayload, limits sessionReplayLimits) (sessionDAGPayload, bool, error) {
	m, ok, err := st.decodeOne(raw, limits)
	if err != nil || !ok {
		return ref, ok, err
	}
	if err := st.chargeMetadata(int64(len(m.Role))); err != nil {
		return ref, false, err
	}
	ref.role = m.Role
	return ref, true, nil
}

func (ref sessionDAGPayload) read(f io.ReaderAt, path string, limits sessionReplayLimits) (provider.Message, error) {
	if err := limits.err(); err != nil {
		return provider.Message{}, err
	}
	raw := make([]byte, ref.end-ref.start)
	if _, err := f.ReadAt(raw, ref.start); err != nil {
		return provider.Message{}, fmt.Errorf("read selected session event: %w", err)
	}
	msgs, _, err := decodeSessionEventMessages(path, raw, 0, 0, limits)
	if err != nil {
		return provider.Message{}, err
	}
	if len(msgs) != 1 {
		return provider.Message{}, fmt.Errorf("selected session event must hold one message")
	}
	var origin []struct {
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(raw, &origin); err != nil {
		return provider.Message{}, err
	}
	if len(origin) == 1 && origin[0].Origin == "host" {
		msgs[0].HostAuthored = true
	}
	return msgs[0], nil
}

func (st *sessionDAGState) chargeMetadata(n int64) error {
	st.metadataBytes += n
	if st.metadataBytes > sessionReplayMaxPayloadBytes {
		return sessionReplayLimitError(st.path, "dag_metadata_bytes", st.metadataBytes, sessionReplayMaxPayloadBytes)
	}
	return nil
}
