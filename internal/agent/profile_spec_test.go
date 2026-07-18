package agent

import "testing"

func TestIntersectToolLists(t *testing.T) {
	got, err := IntersectToolLists([]string{"read_file", "bash"}, []string{"bash", "write_file"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "bash" {
		t.Fatalf("got %v, want [bash]", got)
	}
	if _, err := IntersectToolLists([]string{"read_file"}, []string{"write_file"}); err == nil {
		t.Fatal("empty intersection should error")
	}
	got, err = IntersectToolLists(nil, []string{"bash"})
	if err != nil || len(got) != 1 || got[0] != "bash" {
		t.Fatalf("profile empty → call list: %v %v", got, err)
	}
	got, err = IntersectToolLists([]string{"bash"}, nil)
	if err != nil || len(got) != 1 || got[0] != "bash" {
		t.Fatalf("call empty → profile list: %v %v", got, err)
	}
}

func TestResolveModelEffortPrecedence(t *testing.T) {
	// config override wins over call params.
	m, e := ResolveModelEffort("cfg-m", "cfg-e", "call-m", "call-e", "prof-m", "prof-e", "glob-m", "glob-e")
	if m != "cfg-m" || e != "cfg-e" {
		t.Fatalf("got %s/%s", m, e)
	}
	// call wins over frontmatter when no config.
	m, e = ResolveModelEffort("", "", "call-m", "call-e", "prof-m", "prof-e", "glob-m", "glob-e")
	if m != "call-m" || e != "call-e" {
		t.Fatalf("got %s/%s", m, e)
	}
	// frontmatter over global.
	m, e = ResolveModelEffort("", "", "", "", "prof-m", "prof-e", "glob-m", "glob-e")
	if m != "prof-m" || e != "prof-e" {
		t.Fatalf("got %s/%s", m, e)
	}
	// global fallback.
	m, e = ResolveModelEffort("", "", "", "", "", "", "glob-m", "glob-e")
	if m != "glob-m" || e != "glob-e" {
		t.Fatalf("got %s/%s", m, e)
	}
}

func TestResolveProfileDefinition(t *testing.T) {
	lookup := func(name string) (ProfileDefinition, bool) {
		if name != "doc-rewriter" {
			return ProfileDefinition{}, false
		}
		return ProfileDefinition{Name: "doc-rewriter", Body: "rewrite docs", ReadOnly: false}, true
	}
	if _, err := ResolveProfileDefinition(lookup, "missing"); err == nil {
		t.Fatal("expected unknown profile error")
	}
	if _, err := ResolveProfileDefinition(nil, "doc-rewriter"); err == nil {
		t.Fatal("expected unconfigured error")
	}
	def, err := ResolveProfileDefinition(lookup, "doc-rewriter")
	if err != nil || def.Body != "rewrite docs" {
		t.Fatalf("got %+v %v", def, err)
	}
}

func TestNamedBuiltinProfile(t *testing.T) {
	for _, name := range []string{"explore", "research", "review", "security-review", "security_review"} {
		if !NamedBuiltinProfile(name) {
			t.Fatalf("%s should be named builtin", name)
		}
	}
	if NamedBuiltinProfile("doc-rewriter") {
		t.Fatal("custom profile is not named builtin")
	}
}
