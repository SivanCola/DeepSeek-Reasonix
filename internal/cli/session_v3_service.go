package cli

import (
	"sync"

	"reasonix/internal/sessionv3"
)

// cliSessionServices is the process host registry shared by chat, run, serve,
// and ACP. Boot receives an existing service and never opens a second writer
// registry for the same root.
var cliSessionServices = struct {
	sync.Mutex
	byRoot map[string]*sessionv3.Service
}{byRoot: map[string]*sessionv3.Service{}}

func cliSessionService(sessionDir string) *sessionv3.Service {
	root := sessionv3.RootForLegacyDir(sessionDir)
	if root == "" {
		return nil
	}
	cliSessionServices.Lock()
	defer cliSessionServices.Unlock()
	if service := cliSessionServices.byRoot[root]; service != nil {
		return service
	}
	service, err := sessionv3.NewService("local", sessionv3.NewFilesystemPersistence(root))
	if err != nil {
		return nil
	}
	cliSessionServices.byRoot[root] = service
	return service
}
