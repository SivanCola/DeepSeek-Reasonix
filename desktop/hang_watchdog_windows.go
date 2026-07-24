//go:build windows

package main

import (
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmNull          = 0x0000
	smtoBlock       = 0x0001
	smtoAbortIfHung = 0x0002
)

var (
	user32DLL                  = windows.NewLazySystemDLL("user32.dll")
	enumWindowsProc            = user32DLL.NewProc("EnumWindows")
	getClassNameProc           = user32DLL.NewProc("GetClassNameW")
	getWindowThreadProcessProc = user32DLL.NewProc("GetWindowThreadProcessId")
	sendMessageTimeoutProc     = user32DLL.NewProc("SendMessageTimeoutW")
	windowsHeartbeatMu         sync.Mutex
	windowsHeartbeatStop       chan struct{}
)

func mainThreadWatchdogSupported() bool { return true }

func startNativeMainThreadHeartbeat(intervalMS uint64) {
	windowsHeartbeatMu.Lock()
	if windowsHeartbeatStop != nil {
		windowsHeartbeatMu.Unlock()
		return
	}
	stop := make(chan struct{})
	windowsHeartbeatStop = stop
	windowsHeartbeatMu.Unlock()

	go func() {
		interval := time.Duration(intervalMS) * time.Millisecond
		if interval <= 0 {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				hwnd := currentProcessTopLevelWindow()
				// Window creation can lag OnStartup. Treat absence as
				// inconclusive instead of manufacturing a startup hang.
				if hwnd == 0 || windowMessageLoopResponsive(hwnd, interval) {
					recordMainThreadHeartbeat(now)
				}
			}
		}
	}()
}

func stopNativeMainThreadHeartbeat() {
	windowsHeartbeatMu.Lock()
	stop := windowsHeartbeatStop
	windowsHeartbeatStop = nil
	windowsHeartbeatMu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func currentProcessTopLevelWindow() uintptr {
	var found uintptr
	pid := uint32(os.Getpid())
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var windowPID uint32
		getWindowThreadProcessProc.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
		if windowPID == pid && windowClassName(hwnd) == "wailsWindow" {
			found = hwnd
			return 0
		}
		return 1
	})
	enumWindowsProc.Call(callback, 0)
	return found
}

func windowClassName(hwnd uintptr) string {
	var name [256]uint16
	n, _, _ := getClassNameProc.Call(
		hwnd,
		uintptr(unsafe.Pointer(&name[0])),
		uintptr(len(name)),
	)
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(name[:n])
}

func windowMessageLoopResponsive(hwnd uintptr, timeout time.Duration) bool {
	timeoutMS := timeout.Milliseconds()
	if timeoutMS < 250 {
		timeoutMS = 250
	}
	var result uintptr
	ok, _, _ := sendMessageTimeoutProc.Call(
		hwnd,
		wmNull,
		0,
		0,
		smtoBlock|smtoAbortIfHung,
		uintptr(timeoutMS),
		uintptr(unsafe.Pointer(&result)),
	)
	return ok != 0
}
