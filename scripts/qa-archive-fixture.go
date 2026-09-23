// Creates disposable cold historical sessions for packaged archive acceptance.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"reasonix/internal/provider"
	"reasonix/internal/session"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: qa-archive-fixture <empty disposable home>")
	}
	home := os.Args[1]
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		return fmt.Errorf("fixture home must exist and be empty: %v", err)
	}
	ctx := context.Background()
	service, err := session.NewService("migration-source", session.NewFilesystemPersistence(filepath.Join(home, "sessions-v4")))
	if err != nil {
		return err
	}
	defer service.Shutdown(ctx)
	for _, id := range []string{"qa-cold-history", "qa-adopted-history"} {
		runtime, err := service.Create(ctx, session.CreateOptions{SessionID: id})
		if err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{"message": provider.Message{ID: "user", Role: provider.RoleUser, Content: "Archive acceptance history: " + id}})
		if err != nil {
			return err
		}
		if _, err := runtime.Session().AppendBatch(ctx, "fixture", []session.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
			return err
		}
		if err := service.Close(ctx, runtime.Ref()); err != nil {
			return err
		}
	}
	return nil
}
