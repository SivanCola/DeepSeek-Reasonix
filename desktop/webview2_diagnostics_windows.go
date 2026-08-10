//go:build windows

package main

import "github.com/wailsapp/go-webview2/pkg/edge"

func installWebView2ProcessObserver(app *App) {
	if app == nil {
		return
	}
	nativeWebView2ObserverInstalled.Store(true)
	edge.SetProcessFailedObserver(func(diagnostic edge.ProcessFailedDiagnostic) {
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
		app.recordDiagnosticMetric("desktop_webview2_outcome", outcome)
	})
}
