package sessionstore

import "os"

// copySessionFile copies one message-format session into the import, reporting
// whether it landed. A copy that fails has to reach the caller: the one-time
// import marker is only truthful when every session arrived.
func copySessionFile(jsonlPath, dest string) bool {
	if isNativeSessionEventLog(SessionEventLogPath(jsonlPath)) {
		return saveNativeSessionCopy(jsonlPath, dest) == nil
	}
	return transformAndCopyJsonl(jsonlPath, dest) == nil
}

// publishFileNoReplace atomically publishes a completed sibling temp file
// without replacing a destination another startup/import writer created.
// The temp and destination share a directory, so a hard link is atomic and
// portable across the filesystems Reasonix supports.
func publishFileNoReplace(tmp, dst string) error {
	if err := linkFileNoReplace(tmp, dst); err != nil {
		return err
	}
	return os.Remove(tmp)
}

func linkFileNoReplace(src, dst string) error {
	if err := os.Link(src, dst); err != nil {
		if os.IsExist(err) {
			return os.ErrExist
		}
		return err
	}
	return nil
}
