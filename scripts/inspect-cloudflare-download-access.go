package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"time"
)

var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: inspect-cloudflare-download-access.go VERSION...")
		os.Exit(2)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	for _, version := range os.Args[1:] {
		if !stableVersion.MatchString(version) {
			fmt.Fprintln(os.Stderr, "invalid Stable version")
			os.Exit(2)
		}
		request, err := http.NewRequest(http.MethodGet, "https://dl.reasonix.io/latest/latest.json", nil)
		if err != nil {
			panic(err)
		}
		request.Header.Set("User-Agent", fmt.Sprintf("Reasonix-Updater/v%s (%s/%s; build=stable; update=stable)", version, runtime.GOOS, runtime.GOARCH))
		response, err := client.Do(request)
		if err != nil {
			panic(err)
		}
		result := map[string]any{
			"kind": "go-updater-manifest", "version": version,
			"status": response.StatusCode, "mitigation": response.Header.Get("cf-mitigated"),
			"ray": response.Header.Get("cf-ray"), "contentType": response.Header.Get("content-type"),
		}
		response.Body.Close()
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			panic(err)
		}
	}
}
