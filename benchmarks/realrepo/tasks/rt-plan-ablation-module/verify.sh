#!/usr/bin/env bash
set -e
export GOFLAGS=-mod=mod GOTOOLCHAIN=local
cat > internal/contract/ablation/zz_grader_test.go <<'GO'
package ablation

import (
	"strings"
	"testing"
)

var _ Module = Memory

func TestGraderMemoryIsTheLastModule(t *testing.T) {
	mods := Modules()
	if len(mods) != 8 || mods[7] != Module("memory") || Memory != Module("memory") {
		t.Fatalf("Modules() = %v, want the seven shipped modules then memory", mods)
	}
	for spec, arm := range map[string]string{
		"memory":         "no-memory",
		"memory,planner": "no-planner+no-memory",
		"all":            "no-evidence+no-planner+no-subagent+no-retrieval+no-compaction+no-upstream+no-recall-search+no-memory",
	} {
		set, err := Parse(spec)
		if err != nil {
			t.Fatalf("Parse(%q): %v", spec, err)
		}
		if got := set.Arm(); got != arm {
			t.Errorf("Parse(%q).Arm() = %q, want %q", spec, got, arm)
		}
	}
	if !strings.Contains(ModuleList(), "memory") {
		t.Errorf("ModuleList() = %q", ModuleList())
	}
}

func TestGraderSummariesAndHelp(t *testing.T) {
	seen := map[string]Module{}
	var want []string
	for _, m := range Modules() {
		s := m.Summary()
		if strings.TrimSpace(s) == "" || strings.Contains(s, "\n") {
			t.Errorf("%s: summary %q is not one line", m, s)
		}
		if prev, dup := seen[s]; dup {
			t.Errorf("%s and %s share the summary %q", prev, m, s)
		}
		seen[s] = m
		want = append(want, string(m)+": "+s)
	}
	if got := Module("bogus").Summary(); got != "" {
		t.Errorf("undeclared module summary = %q, want empty", got)
	}
	if got := ModuleHelp(); got != strings.Join(want, "\n") {
		t.Errorf("ModuleHelp() = %q, want %q", got, strings.Join(want, "\n"))
	}
}
GO
gofmt_out="$(gofmt -l .)"
[ -z "$gofmt_out" ] || { echo "gofmt needed: $gofmt_out"; exit 1; }
go vet ./...
go test ./... >/dev/null
list="$(go run ./cmd/benchctl arm -list)"
[ "$(printf '%s\n' "$list" | wc -l | tr -d ' ')" = 8 ] || { echo "arm -list printed: $list"; exit 1; }
case "$(printf '%s\n' "$list" | head -1)" in "evidence: "?*) ;; *) echo "arm -list first line: $list"; exit 1 ;; esac
case "$(printf '%s\n' "$list" | tail -1)" in "memory: "?*) ;; *) echo "arm -list last line: $list"; exit 1 ;; esac
[ "$(go run ./cmd/benchctl arm -ablate memory)" = no-memory ] || { echo "benchctl arm -ablate memory"; exit 1; }
go run ./cmd/benchctl arm -h 2>&1 | grep -A2 -- '-ablate' | grep -q 'memory' || { echo "-ablate help does not name memory"; exit 1; }
grep -lE 'ModuleHelp\(|Summary\(' internal/contract/ablation/*_test.go | grep -qv zz_grader_test.go \
	|| { echo "no ablation test covers Summary or ModuleHelp"; exit 1; }
grep -qE 'Memory|"memory"' cmd/benchctl/main_test.go internal/contract/ablation/ablation_test.go \
	|| { echo "no test covers the memory module"; exit 1; }
awk '/^## Ablation/{on=1; next} /^## /{on=0} on' README.md | grep -qE '^\| `memory` \|' \
	|| { echo "README's ablation table has no memory row"; exit 1; }
