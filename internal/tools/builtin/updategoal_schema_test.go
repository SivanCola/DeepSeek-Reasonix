package builtin

import (
	"encoding/json"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"reasonix/internal/contract/provider"
)

func TestUpdateGoalSchemaMatchesStatusRequirements(t *testing.T) {
	var document any
	if err := json.Unmarshal(provider.CanonicalizeSchema((updateGoal{}).Schema()), &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(nil)
	compiler.DefaultDraft(jsonschema.Draft7)
	const resource = "urn:reasonix:update-goal-test"
	if err := compiler.AddResource(resource, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, args string
		valid      bool
	}{
		{"continue", `{"status":"continue","reason":"work remains"}`, true},
		{"blocked", `{"status":"blocked","reason":"need user input"}`, true},
		{"complete without reason", `{"status":"complete"}`, true},
		{"complete with blank optional reason", `{"status":"complete","reason":" "}`, true},
		{"missing continue reason", `{"status":"continue"}`, false},
		{"missing blocked reason", `{"status":"blocked"}`, false},
		{"empty reason", `{"status":"continue","reason":""}`, false},
		{"blank reason", `{"status":"blocked","reason":" \t\n"}`, false},
		{"unicode blank reason", `{"status":"blocked","reason":"\u0085\u00a0\u3000"}`, false},
		{"verified command", `{"status":"complete","completion":{"verified":["go test ./..."]}}`, true},
		{"no verification claim", `{"status":"complete","completion":{"verified":[]}}`, true},
		{"blank verification", `{"status":"complete","completion":{"verified":[" "]}}`, false},
		{"blank ongoing verification", `{"status":"continue","reason":"working","completion":{"verified":[""]}}`, false},
		{"unicode blank verification", `{"status":"complete","completion":{"verified":["\u00a0\u3000"]}}`, false},
		{"unsupported status", `{"status":"resume"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var args any
			if err := json.Unmarshal([]byte(tc.args), &args); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(args); (err == nil) != tc.valid {
				t.Errorf("schema validation = %v, want valid=%v", err, tc.valid)
			}
			toolFn, recorder, ctx := goalTool(t)
			_, err := toolFn.Execute(ctx, json.RawMessage(tc.args))
			if (err == nil) != tc.valid {
				t.Fatalf("execution = %v, want valid=%v", err, tc.valid)
			}
			if !tc.valid && len(recorder.reports) != 0 {
				t.Fatalf("invalid call recorded a report: %+v", recorder.reports)
			}
		})
	}
}
