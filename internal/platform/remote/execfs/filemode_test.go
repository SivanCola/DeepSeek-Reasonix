package execfs

import (
	"io/fs"
	"testing"
)

func TestFileModeReadsTheRawBits(t *testing.T) {
	for raw, want := range map[uint32]fs.FileMode{
		0o100644: 0o644,
		0o040755: fs.ModeDir | 0o755,
		0o120777: fs.ModeSymlink | 0o777,
		0o104755: fs.ModeSetuid | 0o755,
		0o041777: fs.ModeDir | fs.ModeSticky | 0o777,
	} {
		if got := FileMode(raw); got != want {
			t.Errorf("FileMode(%o) = %v, want %v", raw, got, want)
		}
	}
}
