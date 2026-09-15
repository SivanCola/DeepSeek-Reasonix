package main

// App.mu, runtimeRebuildMu and turnStartMu guard this handoff. A running source
// keeps its runtime, sink and writer; only the target reservation is replaced.
func (a *App) commitCanonicalRuntimeTransitionLocked(tab *WorkspaceTab, transition sessionRuntimePathTransition, preserve bool) bool {
	if !preserve {
		return a.commitSessionRuntimePathLocked(transition)
	}
	if !a.sessionRuntimePathTransitionValidLocked(transition) || !a.detachRuntimeForReplacementLocked(tab) {
		return false
	}
	if a.runtimeBySessionKey[transition.targetKey] == transition.runtime {
		delete(a.runtimeBySessionKey, transition.targetKey)
	}
	return true
}

func fenceCanonicalNavigationSink(sink *tabEventSink) {
	if sink != nil {
		sink.setBinding("", nil)
		sink.clearContext()
	}
}
