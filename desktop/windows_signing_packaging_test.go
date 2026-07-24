package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type signPathArtifactConfiguration struct {
	Zip signPathZip `xml:"zip-file"`
}

type signPathZip struct {
	Files []signPathPEFile `xml:"pe-file"`
}

type signPathPEFile struct {
	Path   string    `xml:"path,attr"`
	Sign   *struct{} `xml:"authenticode-sign"`
	Verify *struct{} `xml:"authenticode-verify"`
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func parseSignPathConfiguration(t *testing.T, name string) signPathArtifactConfiguration {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", ".signpath", "artifact-configurations", name))
	if err != nil {
		t.Fatal(err)
	}
	var config signPathArtifactConfiguration
	if err := xml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return config
}

func TestWindowsReleaseSignsPayloadBeforeRepackaging(t *testing.T) {
	workflow := readTestFile(t, "../.github/workflows/release-desktop.yml")
	orderedSteps := []string{
		"name: Upload unsigned Windows payload for SignPath",
		"name: Submit Windows payload for Authenticode signing",
		"name: Rebuild Windows packages from signed payload",
		"name: Upload unsigned installer for SignPath",
		"name: Submit installer for Authenticode signing",
		"name: Replace installer with signed build",
		"name: Verify Windows Authenticode release contract",
		"name: Sign artifacts (minisign)",
	}
	last := -1
	for _, step := range orderedSteps {
		index := strings.Index(workflow, step)
		if index < 0 {
			t.Fatalf("desktop release workflow is missing %q", step)
		}
		if index <= last {
			t.Fatalf("desktop release workflow step %q is out of order", step)
		}
		last = index
	}
	for _, want := range []string{
		`artifact-configuration-slug: windows-payload`,
		`artifact-configuration-slug: windows-installer-v2`,
		`path: desktop/build/windows/signing-payload/*.exe`,
		`path: desktop/build/windows/installer-signing-bundle/*.exe`,
		`github.repository == 'esengine/DeepSeek-Reasonix'`,
		`SIGNPATH_API_TOKEN is required for official Windows releases`,
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("desktop release workflow is missing signing contract %q", want)
		}
	}

	packager := readTestFile(t, "../scripts/package-windows-desktop.sh")
	copyMain := strings.Index(packager, `cp "$PAYLOAD/$BINNAME.exe" "$BIN_DIR/$BINNAME.exe"`)
	makeNSIS := strings.Index(packager, "makensis \\\n")
	portable := strings.Index(packager, `cp "$PAYLOAD/$BINNAME.exe" "$portable_staging/$BINNAME.exe"`)
	bundle := strings.Index(packager, `installer_bundle="$DESKTOP/build/windows/installer-signing-bundle"`)
	if copyMain < 0 || makeNSIS < 0 || portable < 0 || bundle < 0 {
		t.Fatal("Windows packager is missing the signed-payload packaging stages")
	}
	if !(copyMain < makeNSIS && makeNSIS < portable && portable < bundle) {
		t.Fatalf("Windows package order must be payload copy -> NSIS -> portable -> signing bundle (copy=%d nsis=%d portable=%d bundle=%d)", copyMain, makeNSIS, portable, bundle)
	}
	for _, want := range []string{
		`cp "$PAYLOAD/$GUARDNAME.exe" "$INSTALLER_DIR/$GUARDNAME.exe"`,
		`cp "$PAYLOAD/$LAUNCHERNAME.exe" "$INSTALLER_DIR/$LAUNCHERNAME.exe"`,
		`cp "$PAYLOAD/$UPDATE_HELPER" "$INSTALLER_DIR/$UPDATE_HELPER"`,
		`cp "$PAYLOAD/$WINDOWS_CLINAME.exe" "$INSTALLER_DIR/$WINDOWS_CLINAME.exe"`,
		`"-DARG_REASONIX_SIGNED_UNINSTALLER=${uninstaller_path}"`,
		`cp "$PAYLOAD/$LAUNCHERNAME.exe" "$portable_staging/$APPNAME.exe"`,
		`"$ROOT/scripts/verify-windows-portable.sh" "$portable_staging"`,
	} {
		if !strings.Contains(packager, want) {
			t.Errorf("Windows packager is missing payload contract %q", want)
		}
	}

	verifier := readTestFile(t, "../scripts/verify-windows-authenticode.ps1")
	for _, want := range []string{
		"Get-AuthenticodeSignature",
		"$signature.SignerCertificate",
		"$signature.Status -ne \"Valid\"",
		"Expand-Archive",
		"Portable archive must contain exactly 6 executables",
		"Get-FileHash -Algorithm SHA256",
	} {
		if !strings.Contains(verifier, want) {
			t.Errorf("Windows Authenticode verifier is missing %q", want)
		}
	}
}

func TestSignPathConfigurationsCoverExactWindowsPayload(t *testing.T) {
	expected := map[string]bool{
		"reasonix-desktop.exe":       true,
		"reasonix-guard.exe":         true,
		"reasonix-launcher.exe":      true,
		"reasonix-update-helper.exe": true,
		"reasonix-cli.exe":           true,
		"reasonix-uninstall.exe":     true,
	}

	payload := parseSignPathConfiguration(t, "windows-payload.xml")
	if len(payload.Zip.Files) != len(expected) {
		t.Fatalf("windows-payload.xml files = %d, want %d", len(payload.Zip.Files), len(expected))
	}
	for _, file := range payload.Zip.Files {
		if !expected[file.Path] {
			t.Errorf("windows-payload.xml contains unexpected path %q", file.Path)
		}
		if file.Sign == nil || file.Verify != nil {
			t.Errorf("windows-payload.xml %q must sign, not verify", file.Path)
		}
	}

	installer := parseSignPathConfiguration(t, "windows-installer-v2.xml")
	if len(installer.Zip.Files) != len(expected)+1 {
		t.Fatalf("windows-installer.xml files = %d, want %d", len(installer.Zip.Files), len(expected)+1)
	}
	verified := 0
	signedInstaller := 0
	for _, file := range installer.Zip.Files {
		switch {
		case file.Path == "*installer*.exe":
			if file.Sign == nil || file.Verify != nil {
				t.Error("windows-installer.xml must sign the outer installer")
			}
			signedInstaller++
		case expected[file.Path]:
			if file.Verify == nil || file.Sign != nil {
				t.Errorf("windows-installer.xml %q must verify, not re-sign", file.Path)
			}
			verified++
		default:
			t.Errorf("windows-installer.xml contains unexpected path %q", file.Path)
		}
	}
	if signedInstaller != 1 || verified != len(expected) {
		t.Fatalf("windows-installer.xml signed installers=%d verified payload=%d", signedInstaller, verified)
	}
}
