package cli

import (
	"strings"
	"testing"

	"reasonix/internal/platform/remote/sftpfs"
)

func TestRemoteTestNamesTheFileRoute(t *testing.T) {
	if got := remoteFilesLine(sftpfs.ModeSFTP); !strings.HasSuffix(got, "sftp") {
		t.Errorf("sftp line = %q", got)
	}
	if got := remoteFilesLine(sftpfs.ModeExec); !strings.Contains(got, "exec") || !strings.Contains(got, "refuses the sftp") {
		t.Errorf("exec line = %q", got)
	}
	if got := remoteFilesLine(""); got != "" {
		t.Errorf("an unknown route printed %q", got)
	}
}
