package teamroster

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// OutboxFile holds what the user sent agents from a window that does not hold
// the team, until the window that does delivers it.
const OutboxFile = "user-outbox.jsonl"

// MaxUserMessageRunes bounds one message the user sends this way.
const MaxUserMessageRunes = 8000

// UserMessage is one message the user sent an agent from outside its
// conversation.
type UserMessage struct {
	ID   string    `json:"id"`
	To   string    `json:"to"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// QueueUserMessage appends a message for agent to under the outbox's lock.
func (s Store) QueueUserMessage(ctx context.Context, to, text string) (UserMessage, error) {
	switch {
	case !agentID.MatchString(to):
		return UserMessage{}, fieldErr("to", ErrInvalid)
	case strings.TrimSpace(text) == "":
		return UserMessage{}, fieldErr("text", ErrEmpty)
	case utf8.RuneCountInString(text) > MaxUserMessageRunes:
		return UserMessage{}, fieldErr("text", ErrTooLong)
	}
	m := UserMessage{ID: "u-" + randomHex(), To: to, Text: text, At: time.Now().UTC()}
	err := s.withLock(ctx, s.Dir, OutboxFile, func() error {
		return appendJSONL(filepath.Join(s.Dir, OutboxFile), m)
	})
	return m, err
}

// TakeUserMessages returns every queued message and empties the outbox, both
// under its lock, so a message is taken once.
func (s Store) TakeUserMessages(ctx context.Context) ([]UserMessage, error) {
	path := filepath.Join(s.Dir, OutboxFile)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var out []UserMessage
	err := s.withLock(ctx, s.Dir, OutboxFile, func() error {
		if err := readJSONL(path, func(line []byte) error {
			var m UserMessage
			if err := json.Unmarshal(line, &m); err != nil {
				return err
			}
			out = append(out, m)
			return nil
		}); err != nil {
			return err
		}
		return os.Remove(path)
	})
	return out, err
}
