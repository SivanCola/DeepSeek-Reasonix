// Keeping the .jsonl checkpoint and the event log describing one transcript:
// which one answers a torn replay, and how a declined in-place append is undone.
package sessionstore

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"

	"reasonix/internal/contract/provider"
)

// resolveReplayedMessages answers a replay that applied at least one record.
// A torn log is met with the checkpoint beside it when that checkpoint carries
// the same history further: the turns the torn record dropped are usually
// already durable there, and taking the shorter log alone is what let an
// unclean exit lose them. damaged still rides out so the next save heals.
func resolveReplayedMessagesWithLimits(sessionPath string, replay sessionEventReplay, limits sessionReplayLimits) ([]provider.Message, bool, bool, error) {
	if replay.damaged {
		healed, ok := checkpointExtendingReplayWithLimits(sessionPath, replay.msgs, limits)
		if err := limits.err(); err != nil {
			return nil, false, false, err
		}
		if ok {
			return healed, false, true, nil
		}
	}
	return replay.msgs, true, replay.damaged, nil
}

// checkpointExtendingReplay reports the checkpoint when it continues the prefix
// the log still replays. One that is shorter is a read model that has not
// caught up; one that contradicts what survived is another writer's file.
func checkpointExtendingReplayWithLimits(sessionPath string, replayed []provider.Message, limits sessionReplayLimits) ([]provider.Message, bool) {
	msgs, err := loadSessionMessagesFromJSONLWithLimits(sessionPath, limits)
	if limits.err() != nil {
		return nil, false
	}
	if err != nil {
		// The checkpoint's tail is torn. Keep what decodes rather than reading
		// the tear as "a stranger's file": that fold returned the shorter replay
		// as a clean success and let the next save rewrite it from that.
		msgs = loadSessionMessagesFromJSONLKeepingDecodedTailWithLimits(sessionPath, limits)
	}
	if len(msgs) <= len(replayed) {
		return nil, false
	}
	if !messagesHavePrefix(msgs, replayed) && !messagesHavePrefixWithCompatibleSystem(msgs, replayed) {
		return nil, false
	}
	return msgs, true
}

// loadSessionMessagesFromJSONLKeepingDecodedTail reads the checkpoint as far as
// its records decode, so a torn FINAL line — the half-written in-place append a
// crash leaves — costs only the record it truncated.
func loadSessionMessagesFromJSONLKeepingDecodedTail(path string) []provider.Message {
	return loadSessionMessagesFromJSONLKeepingDecodedTailWithLimits(path, defaultSessionReplayLimits)
}

func loadSessionMessagesFromJSONLKeepingDecodedTailWithLimits(path string, limits sessionReplayLimits) []provider.Message {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(limits.reader(f))
	if err != nil {
		return nil
	}
	lines := bytes.Split(data, []byte("\n"))
	last := -1
	for i, line := range lines {
		if limits.err() != nil {
			return nil
		}
		if len(bytes.TrimSpace(line)) > 0 {
			last = i
		}
	}
	var msgs []provider.Message
	for i, line := range lines {
		if limits.err() != nil {
			return nil
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var m provider.Message
		if err := json.Unmarshal(line, &m); err != nil {
			// Damage before the end means every later record is about to be
			// dropped: nothing here can stand in for the checkpoint.
			if i != last {
				return nil
			}
			return msgs
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// settleDisplayReadModel republishes the read model whole when the in-place
// append declined it. Leaving it is what let the checkpoint keep the transcript
// from before this save — a second lineage beside the log, which a fallback
// reader would take as the session. It reports whether the read model is
// current and the boundary the display index refresh may extend from.
func settleDisplayReadModel(path string, msgs []provider.Message, current bool, appendFrom int) (bool, int) {
	if current {
		return true, appendFrom
	}
	if err := writeSessionMessages(path, msgs); err != nil {
		// The transcript and its revision are already durable; a read model
		// that could not be republished stays for background repair.
		slog.Warn("session: keeping save after display read-model rewrite failure", "path", path, "err", err)
		return false, appendFrom
	}
	// Rebuilt whole, so there is no previous index left to extend.
	return true, -1
}
