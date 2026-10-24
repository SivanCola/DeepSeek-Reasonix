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

// completeBranchMetaSidecar finishes a transcript whose sidecar an interrupted
// import never published. It reports whether it wrote one. A sidecar that is
// already there — readable or not — is never rewritten, so the ordinary retry of
// a finished session copies nothing, and a corrupt sidecar is not clobbered by a
// source that has one too.
func completeBranchMetaSidecar(srcPath, dstPath string) (bool, error) {
	// LoadBranchMeta is the pre-existing reader: ok means the sidecar is there,
	// an error means it exists but cannot be trusted, and only ok == false with
	// no error means there is nothing to preserve.
	_, ok, err := LoadBranchMeta(dstPath)
	if err != nil {
		return false, err
	}
	if ok {
		return false, nil
	}
	if err := copyBranchMetaSidecar(srcPath, dstPath); err != nil {
		return false, err
	}
	return true, nil
}
