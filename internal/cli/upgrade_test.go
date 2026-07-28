package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"dev", "", false},
		{"", "", false},
		{"  ", "", false},
		{"abc", "", false},
		{"v1.2.3", "v1.2.3", true},
		{"1.2.3", "v1.2.3", true},
		{"v1.2.3-rc1", "v1.2.3-rc1", true},
		{"  v0.10.0  ", "v0.10.0", true},
	}
	for _, tt := range tests {
		got, ok := normalizeVersion(tt.in)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("normalizeVersion(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestVerifyChecksum(t *testing.T) {
	content := []byte("hello world")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	t.Run("match", func(t *testing.T) {
		checksumFile := []byte(fmt.Sprintf("%s  reasonix-linux-amd64.tar.gz\n", hash))
		if err := verifyChecksum(content, "reasonix-linux-amd64.tar.gz", checksumFile); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		checksumFile := []byte(fmt.Sprintf("%s  reasonix-linux-amd64.tar.gz\n", "0000000000000000000000000000000000000000000000000000000000000000"))
		if err := verifyChecksum(content, "reasonix-linux-amd64.tar.gz", checksumFile); err == nil {
			t.Error("expected checksum mismatch error")
		}
	})

	t.Run("not found", func(t *testing.T) {
		checksumFile := []byte(fmt.Sprintf("%s  reasonix-darwin-arm64.tar.gz\n", hash))
		if err := verifyChecksum(content, "reasonix-linux-amd64.tar.gz", checksumFile); err == nil {
			t.Error("expected not-found error")
		}
	})
}

func TestUpgradeSuccessMessageIncludesCurrentAndLatestVersions(t *testing.T) {
	cur := "v1.10.0"
	latest := "v1.11.0"

	got := upgradeSuccessMessage(cur, latest)
	if !strings.Contains(got, cur) {
		t.Fatalf("success message %q does not include current version %q", got, cur)
	}
	if !strings.Contains(got, latest) {
		t.Fatalf("success message %q does not include latest version %q", got, latest)
	}
	if strings.Index(got, cur) > strings.Index(got, latest) {
		t.Fatalf("success message %q should report current version before latest version", got)
	}
	if strings.Contains(got, "%!") {
		t.Fatalf("success message %q contains a missing fmt argument marker", got)
	}
}

func TestExtractFromTarGz(t *testing.T) {
	// Build a .tar.gz in memory containing a "reasonix" entry.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	body := []byte("fake binary content")
	if err := tw.WriteHeader(&tar.Header{
		Name: "reasonix",
		Mode: 0o755,
		Size: int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractFromTarGz(buf.Bytes(), "reasonix")
	if err != nil {
		t.Fatalf("extractFromTarGz: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("extracted body = %q, want %q", got, body)
	}
}

func TestExtractFromTarGz_Nested(t *testing.T) {
	// Archives from goreleaser have the binary at the root with its name.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	body := []byte("nested binary")
	if err := tw.WriteHeader(&tar.Header{
		Name: "reasonix-linux-amd64/reasonix",
		Mode: 0o755,
		Size: int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractFromTarGz(buf.Bytes(), "reasonix")
	if err != nil {
		t.Fatalf("extractFromTarGz: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("extracted body = %q, want %q", got, body)
	}
}

func TestExtractFromTarGz_NotFound(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name: "other-file.txt",
		Mode: 0o644,
		Size: 3,
	}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("foo"))
	tw.Close()
	gw.Close()

	_, err := extractFromTarGz(buf.Bytes(), "reasonix")
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestIsCLITag(t *testing.T) {
	tests := []struct {
		tag  string
		want bool
	}{
		{"v1.6.0", true},
		{"v0.1.0", true},
		{"v2.0.0-rc.1", true},
		{"desktop-v1.5.0", false},
		{"npm-v1.4.0", false},
		{"", false},
		{"v", false},
	}
	for _, tt := range tests {
		if got := isCLITag(tt.tag); got != tt.want {
			t.Errorf("isCLITag(%q) = %v, want %v", tt.tag, got, tt.want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{500, "500 B"},
		{2048, "2.0 KiB"},
		{19_000_000, "18.1 MiB"},
	}
	for _, tt := range tests {
		if got := humanSize(tt.bytes); got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestPickCLIRelease(t *testing.T) {
	pick := func(rels []ghRelease, channel cliReleaseChannel) string {
		if r := pickCLIRelease(rels, channel); r != nil {
			return r.TagName
		}
		return ""
	}

	// Stable skips foreign namespaces and every prerelease, even when a Preview
	// was published more recently than the latest Stable release.
	mixed := []ghRelease{
		{TagName: "v1.18.0-preview.1", Prerelease: true},
		{TagName: "desktop-v1.18.0"},
		{TagName: "npm-v1.18.0"},
		{TagName: "v1.6.0"},
	}
	if got := pick(mixed, cliReleaseStable); got != "v1.6.0" {
		t.Errorf("stable channel: got %q, want v1.6.0", got)
	}

	preview := []ghRelease{
		{TagName: "v1.18.0-preview.2", Prerelease: true},
		{TagName: "v1.19.0-rc.1", Prerelease: true},
		{TagName: "v1.18.0-preview.12", Prerelease: true},
		{TagName: "v1.18.0-preview.13"}, // GitHub prerelease metadata must agree.
		{TagName: "v1.17.21"},
	}
	if got := pick(preview, cliReleasePreview); got != "v1.18.0-preview.12" {
		t.Errorf("preview channel: got %q, want v1.18.0-preview.12", got)
	}

	if got := pick([]ghRelease{{TagName: "desktop-v1.0.0"}}, cliReleaseStable); got != "" {
		t.Errorf("no CLI release should return nil, got %q", got)
	}
}

func TestCLIReleaseChannelContract(t *testing.T) {
	if !strings.HasSuffix(ghAPIReleases, "?per_page=100") {
		t.Fatalf("CLI release query must retain enough history for Stable after frequent Preview releases: %q", ghAPIReleases)
	}

	for _, tc := range []struct {
		value string
		want  cliReleaseChannel
		ok    bool
	}{
		{"", cliReleaseStable, true},
		{"stable", cliReleaseStable, true},
		{"PREVIEW", cliReleasePreview, true},
		{"canary", "", false},
		{"rc", "", false},
	} {
		got, err := parseCLIReleaseChannel(tc.value)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("parseCLIReleaseChannel(%q) = (%q, %v), want (%q, ok=%v)", tc.value, got, err, tc.want, tc.ok)
		}
	}

	for _, tc := range []struct {
		version string
		channel cliReleaseChannel
		want    bool
	}{
		{"v1.17.21", cliReleaseStable, true},
		{"v1.18.0-preview.1", cliReleaseStable, false},
		{"v1.18.0-preview.1", cliReleasePreview, true},
		{"v1.18.0-preview.01", cliReleasePreview, false},
		{"v1.18.0-rc.1", cliReleasePreview, false},
		{"v1.18.0", cliReleasePreview, false},
	} {
		if got := versionBelongsToCLIChannel(tc.version, tc.channel); got != tc.want {
			t.Errorf("versionBelongsToCLIChannel(%q, %q) = %v, want %v", tc.version, tc.channel, got, tc.want)
		}
	}
}

func TestFetchCLIReleasePointer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "reasonix-cli" {
			t.Errorf("unexpected pointer request headers: Accept=%q User-Agent=%q", r.Header.Get("Accept"), r.Header.Get("User-Agent"))
		}
		if r.URL.Path == "/valid" {
			fmt.Fprint(w, `{"tag_name":"v1.18.0-preview.1","prerelease":true,"assets":[]}`)
			return
		}
		fmt.Fprint(w, `{"tag_name":"v1.18.0-preview.1","prerelease":false,"assets":[]}`)
	}))
	defer server.Close()

	release, err := fetchCLIReleasePointer(server.Client(), server.URL+"/valid", cliReleasePreview)
	if err != nil || release.TagName != "v1.18.0-preview.1" {
		t.Fatalf("valid Preview pointer = (%+v, %v)", release, err)
	}
	if _, err := fetchCLIReleasePointer(server.Client(), server.URL+"/invalid", cliReleasePreview); err == nil {
		t.Fatal("pointer with mismatched GitHub prerelease metadata should fail closed")
	}
}
