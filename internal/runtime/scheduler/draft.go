package scheduler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/state/schedule"
)

// Typed reasons a draft cannot become a request; a frontend maps them by
// identity and never by wording.
var (
	ErrDraftTrigger   = errors.New("schedule draft: give exactly one of an interval and a time")
	ErrDraftWorkspace = errors.New("schedule draft: the folder does not exist")
	// ErrDraftFolderUnknown: the folder is not one of the trusted projects, which
	// is the only kind a schedule may read.
	ErrDraftFolderUnknown = errors.New("schedule draft: the folder is not a trusted project")
	ErrDraftModel         = errors.New("schedule draft: the model is not configured")
)

// Draft is what a person typed or picked, before the host normalises it: the
// one shape every frontend builds a schedule from.
type Draft struct {
	Label     string
	EverySec  int64
	At        time.Time
	Workspace string
	Prompt    string
	// Model is a provider/model reference; empty is the user's default.
	Model string
}

// Request normalises a draft into the store's request. The folder must be one
// of folders (see Folders); it is resolved from that entry, not from the draft.
// The model is pinned from the user's configuration, and the request is marked
// as a human's: a frontend only calls this for one.
func (d Draft) Request(folders []string) (schedule.CreateRequest, error) {
	req := schedule.CreateRequest{Label: strings.TrimSpace(d.Label), Prompt: strings.TrimSpace(d.Prompt), ConfirmedBy: schedule.ConfirmedByHuman}
	if (d.EverySec > 0) == !d.At.IsZero() {
		return req, ErrDraftTrigger
	}
	if d.EverySec > 0 {
		req.Trigger = schedule.Trigger{Kind: schedule.TriggerEvery, EverySec: d.EverySec}
	} else {
		req.Trigger = schedule.Trigger{Kind: schedule.TriggerAt, At: d.At}
	}
	ws, ok := Match(folders, d.Workspace)
	if !ok {
		return req, ErrDraftFolderUnknown
	}
	abs, err := filepath.Abs(ws)
	if err != nil {
		return req, err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return req, fmt.Errorf("%w: %q", ErrDraftWorkspace, ws)
	}
	req.Target = schedule.Target{Workspace: abs}
	model, err := PinModel(d.Model, abs)
	if err != nil {
		return req, fmt.Errorf("%w: %w", ErrDraftModel, err)
	}
	req.Model = model
	return req, nil
}
