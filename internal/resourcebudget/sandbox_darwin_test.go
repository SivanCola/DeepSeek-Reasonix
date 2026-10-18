package resourcebudget

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInheritedAdmissionNeedsNoSandboxWriteAccess(t *testing.T) {
	if dir := os.Getenv("REASONIX_READONLY_BUDGET_CHILD"); dir != "" {
		path := filepath.Join(dir, value(os.Environ(), ownerEnv)+".lease")
		if f, err := os.OpenFile(path, os.O_RDWR, 0); err == nil {
			_ = f.Close()
			t.Fatal("sandbox fixture did not deny write access")
		}
		ctx, cancel := context.WithTimeout(WithDirectory(context.Background(), dir), 5*time.Second)
		defer cancel()
		lease, err := Acquire(ctx, "go test ./...", os.Environ())
		if err != nil {
			t.Fatal(err)
		}
		lease.Release()
		return
	}
	sandbox, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skip("sandbox-exec unavailable")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := Acquire(WithDirectory(t.Context(), dir), "go test ./...", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	profile := fmt.Sprintf("(version 1) (allow default) (deny file-write* (subpath %q))", dir)
	cmd := exec.Command(sandbox, "-p", profile, os.Args[0], "-test.run=^TestInheritedAdmissionNeedsNoSandboxWriteAccess$")
	cmd.Env = append(lease.Env(os.Environ()), "REASONIX_READONLY_BUDGET_CHILD="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nested admission attempted a sandbox-forbidden write: %v\n%s", err, strings.TrimSpace(string(output)))
	}
}
