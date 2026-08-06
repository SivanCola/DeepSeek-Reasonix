module reasonix/desktop-lite

go 1.25.0

toolchain go1.26.5

// The lite shell is a nested module for the same reason the full desktop is: its
// eventual Wails/WebKit build must never touch the CLI's CGO_ENABLED=0
// single-static-binary guarantee. The replace lets it import the same
// reasonix/internal/* kernel (the import path stays under reasonix/, so the
// internal rule still permits it), and the parent module's build/test ./...
// skips this directory.
//
// Everything under internal/ here is deliberately cgo-free and headless: the
// kernel-facing runtime is unit-testable without a display, a provider, or a
// WebView, and only the outer shell links the native toolkit.
require reasonix v0.0.0

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/aymanbagabas/go-udiff v0.4.1 // indirect
	github.com/bmatcuk/doublestar/v4 v4.10.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/joho/godotenv v1.5.1 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sabhiram/go-gitignore v0.0.0-20210923224102-525f6e181f06 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/tree-sitter/go-tree-sitter v0.25.0 // indirect
	github.com/tree-sitter/tree-sitter-javascript v0.25.0 // indirect
	github.com/tree-sitter/tree-sitter-python v0.25.0 // indirect
	github.com/tree-sitter/tree-sitter-rust v0.24.2 // indirect
	github.com/tree-sitter/tree-sitter-typescript v0.23.2 // indirect
	github.com/yuin/goldmark v1.8.2 // indirect
	github.com/zalando/go-keyring v0.2.8 // indirect
	golang.org/x/image v0.43.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	mvdan.cc/sh/v3 v3.13.1 // indirect
)

replace reasonix => ../
