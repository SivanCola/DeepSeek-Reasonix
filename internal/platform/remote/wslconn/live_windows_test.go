//go:build windows

package wslconn

import (
	"context"
	"errors"
	"os"
	"testing"

	"reasonix/internal/platform/remote/execfs/fstest"
	"reasonix/internal/platform/wsl"
)

// REASONIX_LIVE_WSL names an installed WSL2 distribution to run the file layer in.
func TestFileLayerInARealDistribution(t *testing.T) {
	name := os.Getenv("REASONIX_LIVE_WSL")
	if name == "" {
		t.Skip("set REASONIX_LIVE_WSL to an installed WSL2 distribution")
	}
	ctx := context.Background()
	c, err := New(ctx, wsl.System(), wsl.SystemCommand(), name)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Exec(ctx, "mktemp -d")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("mktemp = %+v, %v", res, err)
	}
	dir := string(res.Stdout[:len(res.Stdout)-1])
	defer c.Exec(ctx, "rm -rf '"+dir+"'")
	fstest.RunScenario(t, c.Volume(), dir)
	runPathScenario(t, c)
}

// runPathScenario holds the translation to what a real distribution answers.
func runPathScenario(t *testing.T, c *Conn) {
	t.Helper()
	ctx := context.Background()
	for in, want := range map[string]string{
		`C:\Users\someone\a b`:                        "/mnt/c/Users/someone/a b",
		`c:/Users/x`:                                  "/mnt/c/Users/x",
		`C:\a b\it's "q"\-dash\back`:                  "/mnt/c/a b/it's \"q\"/-dash/back",
		`C:\a b\`:                                     "/mnt/c/a b/",
		`C:\dollar $HOME\` + "`z`" + `\ünï`:           "/mnt/c/dollar $HOME/`z`/ünï",
		`\\wsl.localhost\` + c.Distro() + `\home\dev`: "/home/dev",
	} {
		if got, err := c.ToLinux(ctx, in); err != nil || got != want {
			t.Errorf("ToLinux(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{`\\wsl.localhost\Another\x`, `\\server\share\x`, `Z:\nodrive`, `relative\dir`, `/home/dev`, `C:`, ``} {
		if _, err := c.ToLinux(ctx, in); !errors.Is(err, ErrUntranslatable) {
			t.Errorf("ToLinux(%q) = %v, want ErrUntranslatable", in, err)
		}
	}
	if got, err := c.ToWindows(ctx, "/mnt/c/Users"); err != nil || got != `C:\Users` {
		t.Errorf("ToWindows(/mnt/c/Users) = %q, %v", got, err)
	}
	if got, err := c.ToWindows(ctx, "/home/dev"); err != nil || got != `\\wsl.localhost\`+c.Distro()+`\home\dev` {
		t.Errorf("ToWindows(/home/dev) = %q, %v", got, err)
	}
	for _, in := range []string{"relative", "", `C:\x`} {
		if _, err := c.ToWindows(ctx, in); !errors.Is(err, ErrUntranslatable) {
			t.Errorf("ToWindows(%q) = %v, want ErrUntranslatable", in, err)
		}
	}
}
