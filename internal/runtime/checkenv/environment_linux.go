//go:build linux

package checkenv

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"sync"

	"reasonix/internal/base/fileutil"
	"reasonix/internal/safety/evidence"
)

type Invocation struct {
	mu          sync.Mutex
	spec        Spec
	identity    string
	command     string
	executionID string
	callID      string
	state       invocationState
	scope       *namespaceScope
}

type invocationState struct {
	directory     string
	runtimeRoot   string
	sourceRoot    string
	argv          []string
	input         evidence.CheckInputs
	runtimeDigest string
	sourceDigest  string
	status        *os.File
	cmd           *exec.Cmd
	ctx           context.Context
	sourcePolicy  treePolicy
	launcher      fileutil.ContentVersion
	active        bool
	closed        bool
}

// New allocates no filesystem or process resources. Freezing happens only in
// the exact call's execution phase, after permissions and pre-tool hooks.
func New(spec Spec, command, executionID, callID string) *Invocation {
	return &Invocation{spec: spec, command: command, executionID: executionID, callID: callID, identity: "venv_" + rand.Text(), scope: newNamespaceScope()}
}

func (i *Invocation) Inputs() []evidence.CheckInputs {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state.input.CommandDigest == "" {
		return nil
	}
	input := i.state.input
	input.Gaps = append([]string(nil), input.Gaps...)
	return []evidence.CheckInputs{input}
}

func (i *Invocation) Close() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.state.closed = true
	if !i.state.active {
		i.remove()
	}
}

func (i *Invocation) remove() {
	i.scope.closeFiles()
	if i.state.status != nil {
		_ = i.state.status.Close()
		i.state.status = nil
	}
	if i.state.directory != "" {
		removeFrozenTree(i.state.directory)
		i.state.directory = ""
	}
}
