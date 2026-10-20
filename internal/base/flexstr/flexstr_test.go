package flexstr

import (
	"encoding/json"
	"testing"
)

// The producer decides the type of a small text field, so the decoder has to
// take both readings of it. A string is itself, a number is its text, and an
// absent or null field is empty — the shapes seen in the wild are exactly these
// four.
func TestStringAcceptsAStringOrNumericValue(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "string", body: `{"code":"invalid_request"}`, want: "invalid_request"},
		{name: "number", body: `{"code":400}`, want: "400"},
		{name: "zero", body: `{"code":0}`, want: "0"},
		{name: "negative number", body: `{"code":-1}`, want: "-1"},
		{name: "absent", body: `{}`, want: ""},
		{name: "null", body: `{"code":null}`, want: ""},
		{name: "empty string", body: `{"code":""}`, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got struct {
				Code String `json:"code"`
			}
			if err := json.Unmarshal([]byte(tc.body), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Code.String() != tc.want {
				t.Fatalf("code = %q, want %q", got.Code.String(), tc.want)
			}
		})
	}
}

// A malformed string literal is a broken payload, not a type mismatch: it has to
// still fail, or the tolerant reading would hide corrupt input.
func TestStringStillRejectsAMalformedLiteral(t *testing.T) {
	var got struct {
		Code String `json:"code"`
	}
	if err := json.Unmarshal([]byte(`{"code":"unterminated}`), &got); err == nil {
		t.Fatal("a malformed string literal decoded without an error")
	}
}

// A String going back out to JSON is written as a string, so a value read from a
// number and echoed back does not change shape between a decode and a re-encode.
func TestStringMarshalsAsAString(t *testing.T) {
	out, err := json.Marshal(struct {
		Code String `json:"code"`
	}{Code: "400"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(out) != `{"code":"400"}` {
		t.Fatalf("encoded %s, want the text form", out)
	}
}
