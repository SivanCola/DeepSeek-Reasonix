package main

const configLoadWarningsEvent = "config:load-warnings"

// emitConfigLoadWarnings transfers ownership of resilient-loader diagnostics
// to the persistent desktop banner. Returning false keeps boot's diagnostic
// notice when no Wails event context is available.
func (a *App) emitConfigLoadWarnings(warnings []string) bool {
	if a == nil || a.ctx == nil || len(warnings) == 0 {
		return false
	}
	owned := append([]string(nil), warnings...)
	a.runtimeEvents.Emit(a.ctx, configLoadWarningsEvent, owned)
	return true
}
