package forward

import (
	"errors"
	"syscall"
)

// isAddrInUse reports whether err is an "address already in use" bind error,
// across platforms (EADDRINUSE on Unix, WSAEADDRINUSE on Windows).
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
