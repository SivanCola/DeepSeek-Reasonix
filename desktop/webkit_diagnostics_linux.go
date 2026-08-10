//go:build linux && cgo

package main

/*
#cgo !webkit2_41 pkg-config: gtk+-3.0 webkit2gtk-4.0
#cgo webkit2_41 pkg-config: gtk+-3.0 webkit2gtk-4.1
#include "webkit_diagnostics_linux.h"
*/
import "C"

import (
	"fmt"
	"sync"
)

var webKitObserverState = struct {
	sync.Once
	events chan webKitNativeEvent
}{events: make(chan webKitNativeEvent, 32)}

func installWebKitProcessObserver(app *App, enabled bool) {
	if app == nil || !enabled || !app.diagnosticsOwner {
		return
	}
	webKitObserverState.Do(func() {
		go func() {
			for event := range webKitObserverState.events {
				report, outcome, failureBucket := webKitNativeFailureReport(event)
				_ = writePendingReport(report, true)
				app.recordDiagnosticMetric("desktop_web_runtime_failure", failureBucket)
				app.recordDiagnosticMetric("desktop_web_runtime_outcome", outcome)
			}
		}()
		C.reasonix_install_webkit_observer()
	})
}

//export reasonixWebKitRuntimeReady
func reasonixWebKitRuntimeReady(major, minor, micro C.int, gpuMode C.int) {
	mode := "unknown"
	switch int(gpuMode) {
	case 0:
		mode = "always"
	case 1:
		mode = "disabled"
	case 2:
		mode = "on_demand"
	}
	publishWebRuntimeContext(webRuntimeContext{
		Engine:         "webkitgtk",
		RuntimeVersion: fmt.Sprintf("%d.%d.%d", int(major), int(minor), int(micro)),
		GPUMode:        mode,
	})
}

//export reasonixWebKitProcessTerminated
func reasonixWebKitProcessTerminated(reason, recovery C.int, generation C.ulonglong) {
	event := webKitNativeEvent{
		reason: int(reason), recovery: int(recovery), generation: uint64(generation),
		runtimeContext: webRuntimeContextForTelemetry(0),
	}
	select {
	case webKitObserverState.events <- event:
	default:
		// Native failures are exceptionally rare. Keep the GTK callback bounded;
		// lifecycle v2 remains the fallback if a pathological burst fills the queue.
	}
}
