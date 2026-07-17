package agent

import "testing"

func TestLegacyDefaultRuntimeContract(t *testing.T) {
	c := LegacyDefaultRuntimeContract()
	if c.ToolSurface != ToolSurfaceLegacy || c.EditProtocol != EditProtocolClassic || c.PromptLayout != PromptLayoutSystem {
		t.Fatalf("legacy default = %+v", c)
	}
	if err := ValidateRuntimeContract(c); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultRuntimeContract(t *testing.T) {
	c := DefaultRuntimeContract()
	if c.ToolSurface != ToolSurfaceCapability || c.EditProtocol != EditProtocolClassic || c.PromptLayout != PromptLayoutSessionCtx {
		t.Fatalf("default = %+v", c)
	}
	if err := ValidateRuntimeContract(c); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeRuntimeContractNilIsLegacy(t *testing.T) {
	got := NormalizeRuntimeContract(nil)
	want := LegacyDefaultRuntimeContract()
	if !got.Equal(want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestValidateRuntimeContractRejectsUnknown(t *testing.T) {
	if err := ValidateRuntimeContract(RuntimeContract{ToolSurface: "x", EditProtocol: EditProtocolClassic, PromptLayout: PromptLayoutSystem}); err == nil {
		t.Fatal("expected error for unknown tool surface")
	}
	if err := ValidateRuntimeContract(RuntimeContract{
		ToolSurface:  ToolSurfaceLegacy,
		EditProtocol: EditProtocolHashline,
		PromptLayout: PromptLayoutSystem,
	}); err == nil {
		t.Fatal("hashline requires capability surface")
	}
}

func TestRuntimeContractFromConfigValues(t *testing.T) {
	c, err := RuntimeContractFromConfigValues("stable", "classic", "session_context")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Equal(DefaultRuntimeContract()) {
		t.Fatalf("got %+v", c)
	}
	legacy, err := RuntimeContractFromConfigValues("legacy", "classic", "legacy_system")
	if err != nil {
		t.Fatal(err)
	}
	if !legacy.Equal(LegacyDefaultRuntimeContract()) {
		t.Fatalf("got %+v", legacy)
	}
	_, err = RuntimeContractFromConfigValues("nope", "classic", "session_context")
	if err == nil {
		t.Fatal("expected config error")
	}
	_, err = RuntimeContractFromConfigValues("legacy", "hashline", "legacy_system")
	if err == nil {
		t.Fatal("hashline+legacy surface must fail")
	}
}

func TestResolveRuntimeContractFromMeta(t *testing.T) {
	old := ResolveRuntimeContractFromMeta(BranchMeta{})
	if !old.Equal(LegacyDefaultRuntimeContract()) {
		t.Fatalf("missing meta field should be legacy, got %+v", old)
	}
	want := DefaultRuntimeContract()
	got := ResolveRuntimeContractFromMeta(BranchMeta{RuntimeContract: want.Clone()})
	if !got.Equal(want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestParseEditProtocolFlag(t *testing.T) {
	got, err := ParseEditProtocolFlag("hashline")
	if err != nil || got != EditProtocolHashline {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := ParseEditProtocolFlag("x"); err == nil {
		t.Fatal("expected error")
	}
}
