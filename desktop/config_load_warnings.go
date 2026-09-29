package main

const configLoadWarningsEvent = "config:load-warnings"

func (a *App) nextConfigLoadWarningsRevision() uint64 {
	if a == nil {
		return 0
	}
	return a.runtimeEvents.configWarningsRevision.Add(1)
}

func (a *App) configLoadWarningsHandler() func([]string) bool {
	if a == nil {
		return nil
	}
	revision := a.nextConfigLoadWarningsRevision()
	return func(warnings []string) bool {
		return a.emitConfigLoadWarnings(revision, warnings)
	}
}

// emitConfigLoadWarnings preserves the legacy event and invalidates bound
// diagnostic reads in new renderers. Returning false keeps boot's diagnostic
// notice when no desktop event context is available.
func (a *App) emitConfigLoadWarnings(revision uint64, warnings []string) bool {
	if a == nil || a.ctx == nil {
		return false
	}
	owned := append([]string{}, warnings...)
	a.runtimeEvents.Emit(a.ctx, configLoadWarningsEvent, owned, revision)
	return true
}
