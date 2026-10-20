package responses

import (
	"encoding/json"
	"testing"

	"reasonix/internal/base/flexstr"
)

// Compatible servers disagree about the type of `code` in an error payload: the
// responses protocol sends a string, a gateway in front of it sends a number. A
// decoder that insists on one of them fails the whole event, and the person sees
// a parse error instead of the reason the request was refused.
func TestErrorCodeAcceptsAStringOrNumericValue(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "string code, as the protocol sends it",
			body: `{"message":"Invalid request","code":"invalid_request"}`,
			code: "invalid_request",
		},
		{
			name: "numeric code, as a gateway sent it",
			body: `{"message":"Invalid request: check the parameters and request format.","code":400}`,
			code: "400",
		},
		{
			name: "no code at all",
			body: `{"message":"Invalid request"}`,
			code: "",
		},
		{
			name: "null code",
			body: `{"message":"Invalid request","code":null}`,
			code: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err sseError
			if err := json.Unmarshal([]byte(tc.body), &err); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := err.Code.String(); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
			if err.Message != "Invalid request" && tc.code != "400" {
				t.Fatalf("message = %q", err.Message)
			}
		})
	}
}

// The stream event carries the same field at its top level, so it needs the same
// treatment: a numeric code there must not fail the whole event.
func TestStreamEventAcceptsANumericCode(t *testing.T) {
	var event sseEvent
	body := `{"type":"error","message":"Invalid request","code":400}`
	if err := json.Unmarshal([]byte(body), &event); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := event.Code.String(); got != "400" {
		t.Fatalf("code = %q, want its text \"400\"", got)
	}
	if event.Message != "Invalid request" {
		t.Fatalf("message = %q", event.Message)
	}
}

// A stale-continuation check reads `code` too. A number there must not turn the
// body into a decode failure that reads as "not a stale id".
func TestStaleContinuationReadsANumericCode(t *testing.T) {
	var body struct {
		Error struct {
			Param string         `json:"param"`
			Code  flexstr.String `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(`{"error":{"param":"previous_response_id","code":400}}`), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code.String() != "400" {
		t.Fatalf("code = %q", body.Error.Code.String())
	}
}
