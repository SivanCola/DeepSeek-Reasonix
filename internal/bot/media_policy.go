package bot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultOutboundMediaLimit int64 = 20 << 20

type MediaPolicy struct {
	LocalRoots []string
	MaxBytes   int64
	AllowHosts map[string]bool
	ResolveDNS bool
}

type PreparedMedia struct {
	Kind   string
	Name   string
	MIME   string
	Size   int64
	SHA256 string
	Data   []byte
}

func (m PreparedMedia) Open() io.ReadCloser { return io.NopCloser(bytes.NewReader(m.Data)) }

// PrepareOutboundMedia resolves and validates a media reference in the host.
// Adapters receive only immutable bytes and never fetch arbitrary URLs or
// local paths themselves.
func PrepareOutboundMedia(ctx context.Context, media OutboundMedia, policy MediaPolicy) (PreparedMedia, error) {
	if err := ValidateOutboundMedia(media, policy); err != nil {
		return PreparedMedia{}, err
	}
	limit := policy.MaxBytes
	if limit <= 0 {
		limit = DefaultOutboundMediaLimit
	}
	data := append([]byte(nil), media.Data...)
	if len(data) == 0 && strings.TrimSpace(media.Path) != "" {
		var err error
		data, err = ReadOutboundMedia(media.Path, limit)
		if err != nil {
			return PreparedMedia{}, err
		}
	}
	if len(data) == 0 && strings.TrimSpace(media.URL) != "" {
		var err error
		data, err = fetchOutboundMedia(ctx, media.URL, policy, limit)
		if err != nil {
			return PreparedMedia{}, err
		}
	}
	if len(data) == 0 {
		return PreparedMedia{}, fmt.Errorf("outbound media has no readable data")
	}
	if int64(len(data)) > limit {
		return PreparedMedia{}, fmt.Errorf("outbound media exceeds %d bytes", limit)
	}
	name := strings.TrimSpace(media.Name)
	if name == "" && media.Path != "" {
		name = filepath.Base(media.Path)
	}
	mimeType := strings.TrimSpace(media.MIME)
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	if parsed, _, err := mime.ParseMediaType(mimeType); err == nil {
		mimeType = parsed
	}
	sum := sha256.Sum256(data)
	return PreparedMedia{Kind: strings.ToLower(strings.TrimSpace(media.Kind)), Name: name, MIME: mimeType, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sum[:]), Data: data}, nil
}

func fetchOutboundMedia(ctx context.Context, rawURL string, policy MediaPolicy, limit int64) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	current := rawURL
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many outbound media redirects")
		}
		if err := validateRemoteMediaURL(req.URL, policy); err != nil {
			return err
		}
		return nil
	}}
	for i := 0; i < 4; i++ {
		u, err := url.Parse(current)
		if err != nil {
			return nil, err
		}
		if err := validateRemoteMediaURL(u, policy); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if location == "" {
				return nil, fmt.Errorf("outbound media redirect has no location")
			}
			next, err := u.Parse(location)
			if err != nil {
				return nil, err
			}
			current = next.String()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("outbound media download error %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("outbound media exceeds %d bytes", limit)
		}
		return data, nil
	}
	return nil, fmt.Errorf("outbound media redirect limit exceeded")
}

func validateRemoteMediaURL(u *url.URL, policy MediaPolicy) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid outbound media URL")
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if len(policy.AllowHosts) > 0 && !policy.AllowHosts[host] {
		return fmt.Errorf("outbound media host is not allowlisted")
	}
	if ip := net.ParseIP(host); ip != nil && isPrivateMediaIP(ip) {
		return fmt.Errorf("outbound media URL resolves to a private address")
	}
	if policy.ResolveDNS {
		ips, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("resolve outbound media host: %w", err)
		}
		for _, ip := range ips {
			if isPrivateMediaIP(ip) {
				return fmt.Errorf("outbound media host resolves to a private address")
			}
		}
	}
	return nil
}

func ValidateOutboundMedia(media OutboundMedia, policy MediaPolicy) error {
	switch strings.ToLower(strings.TrimSpace(media.Kind)) {
	case "image", "file", "audio", "video":
	default:
		return fmt.Errorf("unsupported outbound media kind %q", media.Kind)
	}
	limit := policy.MaxBytes
	if limit <= 0 {
		limit = DefaultOutboundMediaLimit
	}
	if len(media.Data) > int(limit) {
		return fmt.Errorf("outbound media exceeds %d bytes", limit)
	}
	if raw := strings.TrimSpace(media.URL); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Hostname() == "" {
			return fmt.Errorf("invalid outbound media URL")
		}
		host := strings.ToLower(u.Hostname())
		if len(policy.AllowHosts) > 0 && !policy.AllowHosts[host] {
			return fmt.Errorf("outbound media host is not allowlisted")
		}
		if ip := net.ParseIP(host); ip != nil && isPrivateMediaIP(ip) {
			return fmt.Errorf("outbound media URL resolves to a private address")
		}
		if policy.ResolveDNS {
			ips, err := net.LookupIP(host)
			if err != nil {
				return fmt.Errorf("resolve outbound media host: %w", err)
			}
			for _, ip := range ips {
				if isPrivateMediaIP(ip) {
					return fmt.Errorf("outbound media host resolves to a private address")
				}
			}
		}
		return nil
	}
	if raw := strings.TrimSpace(media.Path); raw != "" {
		path, err := filepath.Abs(raw)
		if err != nil {
			return err
		}
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		allowed := false
		for _, root := range policy.LocalRoots {
			rootAbs, rootErr := filepath.Abs(root)
			if rootErr != nil {
				continue
			}
			rootResolved, rootErr := filepath.EvalSymlinks(rootAbs)
			if rootErr != nil {
				continue
			}
			if resolvedPath == rootResolved || strings.HasPrefix(resolvedPath, rootResolved+string(os.PathSeparator)) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("outbound media path is outside allowed roots")
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > limit {
			return fmt.Errorf("outbound media file is invalid or too large")
		}
		return nil
	}
	if len(media.Data) == 0 {
		return fmt.Errorf("outbound media has no data, URL, or path")
	}
	return nil
}

func isPrivateMediaIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func ReadOutboundMedia(path string, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultOutboundMediaLimit
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("outbound media exceeds %d bytes", limit)
	}
	return data, nil
}
