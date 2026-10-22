package sessionstore

import (
	"strings"
	"testing"
)

func TestDAGPayloadLocationsFollowJSONFieldSemantics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record string
		want   string
	}{
		{
			"duplicate targets merge and overwrite",
			`{"schema_version":2,"type":"redact","targets":{"m3":[{"role":"user","content":"first"}]},"TARGETS":{"m4":[{"role":"assistant","content":"four"}],"m3":[{"role":"user","content":"last"}]}}`,
			"system:sys|user:<session-context>|user:last|assistant:four",
		},
		{
			"null resets earlier targets",
			`{"schema_version":2,"type":"redact","targets":{"m3":[{"role":"user","content":"first"}]},"targets":null,"targets":{"m4":[{"role":"assistant","content":"four"}]}}`,
			"system:sys|user:<session-context>|user:hello|assistant:four",
		},
		{
			"escaped field name and nested ignored values",
			`{"schema_version":2,"type":"patch","target":"m3","ignored":{"nested":[{"text":"中文 \\\" { [ ] }"}]},"m\u0073gs":[{"role":"user","content":"中文 \\ \" { [ ] }"}]}`,
			"system:sys|user:<session-context>|user:中文 \\ \" { [ ] }|assistant:hi",
		},
		{
			"case folded message field",
			`{"schema_version":2,"type":"patch","target":"m3","MSGS":[{"role":"user","content":"upper"}]}`,
			"system:sys|user:<session-context>|user:upper|assistant:hi",
		},
		{
			"escaped target id",
			`{"schema_version":2,"type":"redact","targets":{"m\u0033":[{"role":"user","content":"escaped target"}]}}`,
			"system:sys|user:<session-context>|user:escaped target|assistant:hi",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeDAGSession(t, append(baseDAG(t), tc.record)...)
			s, err := LoadSession(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(contents(s.Snapshot()), "|"); got != tc.want {
				t.Fatalf("transcript = %q, want %q", got, tc.want)
			}
		})
	}
}
