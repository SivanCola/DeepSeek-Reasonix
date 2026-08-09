package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareOutboundMediaReadsDataAndHashesIt(t *testing.T) {
	prepared, err := PrepareOutboundMedia(context.Background(), OutboundMedia{Kind: "image", Name: "x.png", Data: []byte("png")}, MediaPolicy{MaxBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Size != 3 || prepared.SHA256 == "" || string(prepared.Data) != "png" {
		t.Fatalf("prepared=%+v", prepared)
	}
}

func TestPrepareOutboundMediaRejectsPrivateRedirectTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1/private", http.StatusFound)
	}))
	defer server.Close()
	if _, err := PrepareOutboundMedia(context.Background(), OutboundMedia{Kind: "file", URL: server.URL}, MediaPolicy{MaxBytes: 100, ResolveDNS: true}); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("redirect SSRF was not rejected: %v", err)
	}
}

func TestValidateOutboundMediaRejectsPrivateURLAndPathEscape(t *testing.T) {
	if err := ValidateOutboundMedia(OutboundMedia{Kind: "image", URL: "http://127.0.0.1/a"}, MediaPolicy{}); err == nil {
		t.Fatal("private media URL accepted")
	}
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.bin")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutboundMedia(OutboundMedia{Kind: "file", Path: outside}, MediaPolicy{LocalRoots: []string{root}}); err == nil {
		t.Fatal("media path outside allowlist accepted")
	}
}
