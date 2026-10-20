package flexstr

import (
	"bytes"
	"encoding/json"
	"strings"
)

// String holds a value the producer may have sent as a JSON string or a number.
type String string

// UnmarshalJSON takes a string as itself and any other scalar as its text. An
// invalid string literal still fails: a malformed payload is a real error, and
// swallowing it would hide the payload rather than the type.
func (s *String) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var text string
		if err := json.Unmarshal(b, &text); err != nil {
			return err
		}
		*s = String(text)
		return nil
	}
	*s = String(strings.Trim(string(b), `"`))
	return nil
}

func (s String) String() string { return string(s) }
