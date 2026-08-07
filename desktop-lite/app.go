// Package main is the Wails shell around the lite session host.
//
// It is deliberately thin. Every decision worth testing — which tools are
// deferred, how the roster is announced, which kernel events reach the UI —
// lives in internal/session and is covered without a display. What is left
// here is adapting that to the webview: hold the Wails context, forward frames,
// and own the window lifecycle.
package main

import (
	"context"
	"sync"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/desktop-lite/internal/session"
	"reasonix/internal/event"
)

// FrameEvent is the Wails event name the frontend subscribes to.
const FrameEvent = "reasonix:frame"

// App is the binding surface Wails exposes to the webview.
type App struct {
	host *session.Host

	mu  sync.RWMutex
	ctx context.Context
}

// NewApp returns a shell bound to a real kernel session.
func NewApp() *App {
	return &App{host: session.NewHost(nil)}
}

// startup stores the Wails context and begins assembling the session.
func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()

	// Assembly reads config and spawns MCP subprocesses. Running it inline
	// would hold the window back until every server had answered, so the
	// window paints first and the composer unlocks on the ready frame.
	go func() {
		if err := a.host.Open(ctx, session.Options{Sink: event.FuncSink(a.emit)}); err != nil {
			a.emitFrame(session.Frame{
				Kind:  "notice",
				Level: "warn",
				Text:  "Could not start a session: " + err.Error(),
			})
			return
		}
		a.emitFrame(session.Frame{Kind: "ready"})
	}()
}

// shutdown closes the session when the window goes away.
func (a *App) shutdown(context.Context) { a.host.Close() }

// Send runs one turn and resolves when it finishes. Output streams to the
// frontend as frames while it runs, so the returned error is only the terminal
// outcome.
func (a *App) Send(input string) error {
	ctx := a.context()
	if ctx == nil {
		return session.ErrNoConversation
	}
	err := a.host.Send(ctx, input)
	// The kernel does not publish TurnDone for synchronous runs, so the shell
	// closes the turn itself; see session.TurnDoneFrame.
	a.emitFrame(session.TurnDoneFrame(err))
	return err
}

// Running reports whether a turn is in flight, so a reloaded webview can
// restore its composer state instead of assuming idle.
func (a *App) Running() bool { return a.host.Running() }

// emit translates a kernel event and forwards anything the UI renders.
func (a *App) emit(e event.Event) {
	frame, ok := session.TranslateEvent(e)
	if !ok {
		return
	}
	a.emitFrame(frame)
}

func (a *App) emitFrame(frame session.Frame) {
	// Events can arrive from kernel goroutines before startup has stored the
	// context, or after shutdown; dropping them is correct, because there is no
	// webview to receive them.
	if ctx := a.context(); ctx != nil {
		wailsruntime.EventsEmit(ctx, FrameEvent, frame)
	}
}

func (a *App) context() context.Context {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ctx
}
