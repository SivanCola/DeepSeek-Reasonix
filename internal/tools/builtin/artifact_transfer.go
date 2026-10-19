package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

func (w artifactDownload) fetch(ctx context.Context, raw string) (*http.Response, []string, error) {
	client := ssrfGuardedClient((webFetch{proxySpec: w.proxySpec}).proxyURLFor)
	client.Timeout = artifactDownloadTimeout
	redirects := []string{}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("artifact redirect limit reached")
		}
		if webImageURL(req.URL.String(), nil) == nil {
			return fmt.Errorf("invalid artifact redirect")
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("artifact redirect downgraded HTTPS")
		}
		redirects = append(redirects, req.URL.String())
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "reasonix-artifact/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, artifactTransferError{cause: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || resp.StatusCode == http.StatusPartialContent {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("artifact source returned HTTP %d", resp.StatusCode)
	}
	return resp, redirects, nil
}

// Signed addresses must not enter model-visible transport errors.
type artifactTransferError struct{ cause error }

func (artifactTransferError) Error() string {
	return "artifact request failed (refusal: artifact.download_failed)"
}
func (e artifactTransferError) Unwrap() error     { return e.cause }
func (artifactTransferError) RefusalCode() string { return "artifact.download_failed" }

func publishArtifact(root *os.Root, stage string, source artifactSourceReceipt) error {
	data, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return err
	}
	f, err := root.OpenFile(source.ProvenancePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.Remove(source.ProvenancePath)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := root.Link(stage, source.Path); err != nil {
		return err
	}
	committed = true
	return nil
}
