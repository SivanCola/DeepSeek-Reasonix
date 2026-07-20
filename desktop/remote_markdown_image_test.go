package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/netclient"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestRemoteMarkdownImageUsesReasonixProxySpec(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nproxy-image")
	wantSpec := netclient.ProxySpec{Mode: netclient.ModeCustom, URL: "socks5://127.0.0.1:10808"}
	var gotSpec netclient.ProxySpec
	var gotRequest *http.Request
	factory := func(spec netclient.ProxySpec) (*http.Client, error) {
		gotSpec = spec
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotRequest = req
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(png)),
				Request:    req,
			}, nil
		})}, nil
	}

	req := httptest.NewRequest(http.MethodGet, remoteMarkdownImagePath+"?url="+url.QueryEscape("https://images.example.com/pixel.png"), nil)
	rec := httptest.NewRecorder()
	serveRemoteMarkdownImage(rec, req, wantSpec, factory)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if !reflect.DeepEqual(gotSpec, wantSpec) {
		t.Fatalf("proxy spec = %#v, want %#v", gotSpec, wantSpec)
	}
	if gotRequest == nil || gotRequest.URL.String() != "https://images.example.com/pixel.png" {
		t.Fatalf("remote request = %v", gotRequest)
	}
	if got := gotRequest.Header.Get("Accept"); !strings.Contains(got, "image/png") {
		t.Fatalf("Accept = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q", got)
	}
	if rec.Body.String() != string(png) {
		t.Fatalf("body mismatch: %q", rec.Body.String())
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

func TestRemoteMarkdownImageTraversesConfiguredHTTPProxy(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nproxied")
	proxyCalled := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalled = true
		if r.URL.String() != "http://images.example.invalid/pixel.png" {
			t.Errorf("proxy target = %q", r.URL.String())
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer proxy.Close()

	spec := netclient.ProxySpec{Mode: netclient.ModeCustom, URL: proxy.URL}
	req := httptest.NewRequest(http.MethodGet, remoteMarkdownImagePath+"?url="+url.QueryEscape("http://images.example.invalid/pixel.png"), nil)
	rec := httptest.NewRecorder()
	serveRemoteMarkdownImage(rec, req, spec, newRemoteMarkdownImageClient)

	if rec.Code != http.StatusOK || !proxyCalled || rec.Body.String() != string(png) {
		t.Fatalf("configured proxy was not used: status=%d called=%v body=%q", rec.Code, proxyCalled, rec.Body.String())
	}
}

func TestRemoteMarkdownImageDirectDialRejectsPrivateResolution(t *testing.T) {
	for _, address := range []string{"127.0.0.1:80", "[::1]:80", "169.254.169.254:80"} {
		t.Run(address, func(t *testing.T) {
			conn, err := remoteMarkdownImageDirectDialContext(context.Background(), "tcp", address)
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "non-public") {
				t.Fatalf("private dial error = %v, want non-public rejection", err)
			}
		})
	}
}

func TestRemoteMarkdownImageRoundTripperUsesDirectGuardForNoProxy(t *testing.T) {
	directCalled := false
	proxiedCalled := false
	rt := remoteMarkdownImageRoundTripper{
		proxyFor: func(*http.Request) (*url.URL, error) { return nil, nil },
		direct: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			directCalled = true
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
		}),
		proxied: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			proxiedCalled = true
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
		}),
	}
	req := httptest.NewRequest(http.MethodGet, "https://images.example.com/pixel.png", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !directCalled || proxiedCalled {
		t.Fatalf("direct=%v proxied=%v, want guarded direct transport only", directCalled, proxiedCalled)
	}
}

func TestRemoteMarkdownImageRejectsUnsafeTargets(t *testing.T) {
	for _, raw := range []string{
		"",
		"file:///tmp/secret.png",
		"http://localhost/image.png",
		"http://127.0.0.1/image.png",
		"http://10.0.0.1/image.png",
		"http://169.254.169.254/latest/meta-data",
		"http://100.100.100.200/latest/meta-data",
		"http://router.local/image.png",
		"https://user:pass@images.example.com/image.png",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := validateRemoteMarkdownImageURL(raw); err == nil {
				t.Fatalf("unsafe URL accepted: %q", raw)
			}
		})
	}
	if got, err := validateRemoteMarkdownImageURL("https://images.example.com/a.png#section"); err != nil || got != "https://images.example.com/a.png" {
		t.Fatalf("public URL = %q, %v", got, err)
	}
	if _, err := validateRemoteMarkdownImageURL("https://[2001:4860:4860::8888]/a.png"); err != nil {
		t.Fatalf("public IPv6 URL rejected: %v", err)
	}
}

func TestRemoteMarkdownImageRejectsNonImagesAndOversizedBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		want int
	}{
		{name: "html", body: []byte("<!doctype html><script>alert(1)</script>"), want: http.StatusUnsupportedMediaType},
		{name: "oversized", body: bytes.Repeat([]byte{'x'}, remoteMarkdownImageMaxBytes+1), want: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factory := func(netclient.ProxySpec) (*http.Client, error) {
				return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(bytes.NewReader(tc.body)),
						Request:    req,
					}, nil
				})}, nil
			}
			req := httptest.NewRequest(http.MethodGet, remoteMarkdownImagePath+"?url="+url.QueryEscape("https://images.example.com/image"), nil)
			rec := httptest.NewRecorder()
			serveRemoteMarkdownImage(rec, req, netclient.ProxySpec{Mode: netclient.ModeCustom, URL: "http://127.0.0.1:10808"}, factory)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body = %q", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestRemoteMarkdownImageSanitizesSVG(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="steal()">
<style>@import url(https://evil.example/style.css);</style>
<script>alert(1)</script>
<foreignObject><iframe src="https://evil.example/"></iframe></foreignObject>
<image href="https://evil.example/pixel.png" />
<use href="#safe-shape" />
<rect id="safe-shape" width="10" height="10" fill="url(#paint)" style="color:red" />
</svg>`)
	factory := func(netclient.ProxySpec) (*http.Client, error) {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"image/svg+xml"}},
				Body:       io.NopCloser(bytes.NewReader(svg)),
				Request:    req,
			}, nil
		})}, nil
	}
	req := httptest.NewRequest(http.MethodGet, remoteMarkdownImagePath+"?url="+url.QueryEscape("https://images.example.com/badge.svg"), nil)
	rec := httptest.NewRecorder()
	serveRemoteMarkdownImage(rec, req, netclient.ProxySpec{Mode: netclient.ModeCustom, URL: "http://127.0.0.1:10808"}, factory)

	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("SVG status=%d type=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	got := rec.Body.String()
	for _, forbidden := range []string{"<script", "<style", "foreignObject", "iframe", "onload", "evil.example"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized SVG still contains %q: %s", forbidden, got)
		}
	}
	for _, preserved := range []string{`href="#safe-shape"`, `fill="url(#paint)"`, `style="color:red"`} {
		if !strings.Contains(got, preserved) {
			t.Fatalf("sanitized SVG dropped %q: %s", preserved, got)
		}
	}
}

func TestRemoteMarkdownImageMiddlewarePassesOtherPaths(t *testing.T) {
	app := NewApp()
	called := false
	handler := app.remoteMarkdownImageMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if !called || rec.Code != http.StatusNoContent {
		t.Fatalf("unrelated request was not passed through: called=%v status=%d", called, rec.Code)
	}
}

func TestRemoteMarkdownImageOnlyAllowsGet(t *testing.T) {
	called := false
	factory := func(netclient.ProxySpec) (*http.Client, error) {
		called = true
		return &http.Client{}, nil
	}
	req := httptest.NewRequest(http.MethodPost, remoteMarkdownImagePath+"?url="+url.QueryEscape("https://images.example.com/image.png"), nil)
	rec := httptest.NewRecorder()
	serveRemoteMarkdownImage(rec, req, netclient.ProxySpec{}, factory)
	if rec.Code != http.StatusMethodNotAllowed || called {
		t.Fatalf("POST status=%d factoryCalled=%v", rec.Code, called)
	}
}
