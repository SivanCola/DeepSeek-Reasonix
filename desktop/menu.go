package main

import (
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// createAppMenu builds the native application menu bar. It emits app:open-settings
// on CmdOrCtrl+, so the frontend can open the settings drawer without a global
// keyboard listener. Edit and Window menus use Wails standard roles for platform-
// correct Undo/Redo/Cut/Copy/Paste/SelectAll and Minimize/Zoom/Fullscreen.
func (a *App) createAppMenu() *menu.Menu {
	m := menu.NewMenu()

	m.Append(menu.AppMenu())

	fileMenu := m.AddSubmenu("File")
	fileMenu.AddText("Settings", keys.CmdOrCtrl(","), func(_ *menu.CallbackData) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "app:open-settings")
		}
	})
	fileMenu.AddText("Show Reasonix", nil, func(_ *menu.CallbackData) {
		a.showMainWindow()
	})
	fileMenu.AddText("Quit Reasonix", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		a.quitApp()
	})
	m.Append(menu.EditMenu())
	m.Append(menu.WindowMenu())

	return m
}
