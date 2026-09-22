//go:build windows

package pathidentity

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// resolveExistingPath asks the kernel to follow all reparse points, including
// junctions. EvalSymlinks does not follow mount points reported as ModeIrregular:
// it can return the alias unchanged for a leaf and ENOTDIR for its descendants.
// This must be the primary resolver, not merely a fallback after an error.
func resolveExistingPath(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(extendedWindowsPath(path))
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", fmt.Errorf("open physical path: %w", err)
	}
	defer windows.CloseHandle(handle)
	for size := uint32(256); size <= 65536; {
		buf := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], size, 0)
		if err != nil {
			return "", fmt.Errorf("get final physical path: %w", err)
		}
		if n < size {
			return stripExtendedPrefix(windows.UTF16ToString(buf[:n])), nil
		}
		if n >= 65536 {
			break
		}
		size = n + 1
	}
	return "", fmt.Errorf("final physical path exceeds 65536 UTF-16 units")
}

// Native Windows calls do not apply the long-path handling performed by os.
// Inputs here are already absolute and clean; preserve existing device paths.
func extendedWindowsPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	if filepath.IsAbs(path) {
		return `\\?\` + path
	}
	return path
}
