package main

const (
	defaultDesktopWindowWidth  = 1240
	defaultDesktopWindowHeight = 720
)

// desktopWindowFrameless reports whether the main window is created without a
// native OS frame.
//
// The main window is frameless on Windows and draws its own chrome: the
// frontend supplies the drag rail (the shell rewrites the drag-region marker
// to -webkit-app-region) and the minimise/maximise/close buttons via the App
// bindings. Remote Serve windows are shell BrowserWindows with the native
// frame and never consult this function.
func desktopWindowFrameless(goos string) bool {
	return goos == "windows"
}

// initialDesktopWindowSize returns the startup size for the main window,
// restoring the saved geometry when present. A maximised entry's size is never
// trusted: shells used to persist the maximized frame (which overflows the
// work area) as the restore rectangle, and that corrupt restore rect can only
// heal by falling back to the default size.
func initialDesktopWindowSize() (int, int) {
	width, height := defaultDesktopWindowWidth, defaultDesktopWindowHeight
	if saved, ok := loadWindowState(); ok && !saved.Maximised {
		if saved.Width > 0 {
			width = saved.Width
		}
		if saved.Height > 0 {
			height = saved.Height
		}
	}
	return width, height
}
