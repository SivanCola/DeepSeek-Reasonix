//go:build windows

package main

import (
	"sync"

	"github.com/wailsapp/go-webview2/pkg/edge"
)

var webView2ObserverState = struct {
	sync.Once
	events chan edge.ProcessFailedDiagnostic
}{events: make(chan edge.ProcessFailedDiagnostic, 32)}

func installWebView2ProcessObserver(app *App) {
	if app == nil {
		return
	}
	gpuMode := "enabled"
	if windowsWebview2GPUDisabled() {
		gpuMode = "disabled"
	}
	publishWebRuntimeContext(webRuntimeContext{
		Engine: "webview2", RuntimeVersion: webView2RuntimeVersion(), GPUMode: gpuMode,
	})
	nativeWebView2ObserverInstalled.Store(true)
	process := func(diagnostic edge.ProcessFailedDiagnostic) {
		event := webView2NativeEvent{
			Kind:                int(diagnostic.Kind),
			Reason:              int(diagnostic.Reason),
			ReasonAvailable:     diagnostic.ReasonAvailable,
			ExitCode:            diagnostic.ExitCode,
			ExitCodeAvailable:   diagnostic.ExitCodeAvailable,
			ProcessDescription:  diagnostic.ProcessDescription,
			FailureSourceModule: diagnostic.FailureSourceModule,
			Recovery:            diagnostic.Recovery,
		}
		report, outcome := webView2NativeFailureReport(event, webView2RuntimeVersion(), windowsWebview2GPUDisabled())
		_ = writePendingReport(report, true)
		if report.WebRuntime != nil {
			app.recordDiagnosticMetric("desktop_web_runtime_failure", "webview2."+report.WebRuntime.Kind+"."+report.WebRuntime.Reason)
		}
		app.recordDiagnosticMetric("desktop_web_runtime_outcome", outcome)
	}
	webView2ObserverState.Do(func() {
		go func() {
			for diagnostic := range webView2ObserverState.events {
				process(diagnostic)
			}
		}()
		edge.SetProcessFailedObserver(func(diagnostic edge.ProcessFailedDiagnostic) {
			if diagnostic.Kind == edge.COREWEBVIEW2_PROCESS_FAILED_KIND_BROWSER_PROCESS_EXITED {
				// Wails exits immediately after its callback. Preserve the fatal
				// report and outcome synchronously before control returns to Wails.
				process(diagnostic)
				return
			}
			select {
			case webView2ObserverState.events <- diagnostic:
			default:
				// Keep the COM callback non-blocking. Lifecycle v2 remains the
				// fallback for a pathological burst larger than the bounded queue.
			}
		})
	})
}
