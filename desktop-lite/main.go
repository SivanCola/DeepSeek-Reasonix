package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// assets embeds the built frontend. `all:` so dotfiles (the dist .gitkeep that
// keeps an unbuilt checkout compiling) are included.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Reasonix Lite",
		Width:     980,
		Height:    720,
		MinWidth:  640,
		MinHeight: 420,
		// Match the shell's own background so the webview does not flash white
		// before CSS loads, which is particularly visible on WebKitGTK.
		BackgroundColour: &options.RGBA{R: 15, G: 16, B: 20, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []any{app},
	})
	if err != nil {
		log.Fatalf("reasonix-lite: %v", err)
	}
}
