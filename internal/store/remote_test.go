package store

import (
	"strings"
	"testing"
)

func TestRemoteWorkspaceSlug(t *testing.T) {
	cases := map[string]string{
		"/home/dev/projects/app":  "home-dev-projects-app",
		"/home/dev/projects/app/": "home-dev-projects-app",
		"/":                       "root",
		"~/work":                  "~-work",
	}
	for in, want := range cases {
		if got := RemoteWorkspaceSlug(in); got != want {
			t.Errorf("RemoteWorkspaceSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoteWorkspaceSlugBoundsLongPaths(t *testing.T) {
	long := "/data/" + strings.Repeat("verydeepdir/", 40) + "app"
	slug := RemoteWorkspaceSlug(long)
	if len(slug) > 200 {
		t.Fatalf("slug length %d exceeds bound", len(slug))
	}
	other := RemoteWorkspaceSlug(long + "2")
	if slug == other {
		t.Fatal("distinct long paths collapsed to one slug")
	}
}

func TestRemoteServeFileNames(t *testing.T) {
	slug := "home-dev-app"
	if got := RemoteServeStateName(slug); got != "serve-home-dev-app.json" {
		t.Fatalf("state name = %q", got)
	}
	if got := RemoteServeTokenName(slug); got != "serve-home-dev-app.token" {
		t.Fatalf("token name = %q", got)
	}
	if got := RemoteServeLogName(slug); got != "serve-home-dev-app.log" {
		t.Fatalf("log name = %q", got)
	}
	if got := RemoteServePortName(slug); got != "serve-home-dev-app.port" {
		t.Fatalf("port name = %q", got)
	}
	if got := RemoteServePidName(slug); got != "serve-home-dev-app.pid" {
		t.Fatalf("pid name = %q", got)
	}
}
