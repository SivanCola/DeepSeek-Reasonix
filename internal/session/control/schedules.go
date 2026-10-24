package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"time"

	"reasonix/internal/runtime/scheduler"
	"reasonix/internal/state/schedule"
)

// ErrScheduleConfirmMismatch is a creation whose confirmation is not the one the
// preview issued: what is stored is exactly what the person was shown.
var ErrScheduleConfirmMismatch = errors.New("schedule: the confirmation does not match the preview")

// Schedules is the machine-wide scheduled-runs surface. Every frontend that
// shows or edits schedules goes through it, so floors, defaults and the
// confirmation are enforced once and never by a frontend.
type Schedules interface {
	Schedules(ctx context.Context) (ScheduleBoard, error)
	PreviewSchedule(ctx context.Context, d ScheduleDraft) (SchedulePreview, error)
	CreateSchedule(ctx context.Context, d ScheduleDraft, confirm string) (ScheduleView, error)
	PauseSchedule(ctx context.Context, id string) error
	ResumeSchedule(ctx context.Context, id string) error
	RenewSchedule(ctx context.Context, id string) error
	DeleteSchedule(ctx context.Context, id string) error
	SetSchedulesPaused(ctx context.Context, paused bool) error
	ScheduleResult(ctx context.Context, triggerID string) (ScheduleResultView, error)
}

// ScheduleDraft is what a person picked in a frontend. Exactly one of EverySec
// and AtUnix is set.
type ScheduleDraft struct {
	Label     string `json:"label"`
	EverySec  int64  `json:"everySec,omitempty"`
	AtUnix    int64  `json:"atUnix,omitempty"`
	Workspace string `json:"workspace"`
	Prompt    string `json:"prompt"`
	// Model is a provider/model reference; empty is the default model.
	Model string `json:"model,omitempty"`
}

// ScheduleView is one stored schedule.
type ScheduleView struct {
	ID                  string `json:"id"`
	Label               string `json:"label"`
	Status              string `json:"status"`
	PausedReason        string `json:"pausedReason,omitempty"`
	EverySec            int64  `json:"everySec,omitempty"`
	AtUnix              int64  `json:"atUnix,omitempty"`
	Workspace           string `json:"workspace"`
	Model               string `json:"model"`
	Prompt              string `json:"prompt"`
	PerRunTokens        int64  `json:"perRunTokens"`
	LifetimeTokens      int64  `json:"lifetimeTokens"`
	SpentLifetimeTokens int64  `json:"spentLifetimeTokens"`
	ConsecutiveFailures int    `json:"consecutiveFailures,omitempty"`
	ExpiresAtUnix       int64  `json:"expiresAtUnix"`
}

// ScheduleRunView is one run record: a claimed slot, or one skipped on purpose.
type ScheduleRunView struct {
	TriggerID  string `json:"triggerId"`
	ScheduleID string `json:"scheduleId"`
	SlotUnix   int64  `json:"slotUnix"`
	State      string `json:"state"`
	SkipReason string `json:"skipReason,omitempty"`
	Charged    int64  `json:"chargedTokens"`
	EndedUnix  int64  `json:"endedUnix,omitempty"`
}

// SchedulePolicyView is the part of the policy a frontend states to the person.
type SchedulePolicyView struct {
	MinIntervalMinutes int64 `json:"minIntervalMinutes"`
	MaxSchedules       int64 `json:"maxSchedules"`
	GlobalTokensDay    int64 `json:"globalTokensDay"`
	ExpiryDays         int64 `json:"expiryDays"`
}

// ScheduleBoard is everything the list shows.
type ScheduleBoard struct {
	PausedAll    bool               `json:"pausedAll"`
	PausedReason string             `json:"pausedReason,omitempty"`
	Schedules    []ScheduleView     `json:"schedules"`
	Runs         []ScheduleRunView  `json:"runs"`
	Policy       SchedulePolicyView `json:"policy"`
	// Folders are the trusted projects a schedule may be given. A creation names
	// one of these and nothing else.
	Folders []ScheduleFolder `json:"folders"`
	// Current is the open project when it is not yet one of Folders, with the
	// person's recorded answer about it, so the list can offer the decision.
	Current *ScheduleCurrent `json:"current,omitempty"`
}

// ScheduleCurrent is the open project a schedule cannot read yet. Trust is the
// recorded answer: empty when undecided, "declined" after a refusal.
type ScheduleCurrent struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Trust     string `json:"trust"`
	Trustable bool   `json:"trustable"`
}

// ScheduleFolder is one trusted project a schedule can read.
type ScheduleFolder struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// SchedulePreview is what a person confirms, with the token that proves a
// creation is the one they were shown.
type SchedulePreview struct {
	FirstRunUnix  int64  `json:"firstRunUnix"`
	Workspace     string `json:"workspace"`
	Model         string `json:"model"`
	Prompt        string `json:"prompt"`
	PerRunTokens  int64  `json:"perRunTokens"`
	PerRunSteps   int64  `json:"perRunSteps"`
	PerRunWallSec int64  `json:"perRunWallSec"`
	PerDayMax     int64  `json:"perDayMax"`
	Lifetime      int64  `json:"lifetimeTokens"`
	ExpiresAtUnix int64  `json:"expiresAtUnix"`
	Confirm       string `json:"confirm"`
}

// SchedulePendingView is something a run parked for a person. Detail is the
// model's own wording and is untrusted.
type SchedulePendingView struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
}

// ScheduleResultView is what a finished run left. The report is model text: a
// frontend labels it and renders it without loading remote content.
type ScheduleResultView struct {
	TriggerID       string                `json:"triggerId"`
	Report          string                `json:"report"`
	ReportTruncated bool                  `json:"reportTruncated,omitempty"`
	Pending         []SchedulePendingView `json:"pending"`
	PendingDropped  int                   `json:"pendingDropped,omitempty"`
	Enforcement     string                `json:"enforcement"`
	RecordedUnix    int64                 `json:"recordedUnix"`
}

func (c *Controller) scheduleStore() (*schedule.Store, schedule.Policy, error) {
	return scheduler.OpenUserStore(nil)
}

func (c *Controller) Schedules(ctx context.Context) (ScheduleBoard, error) {
	store, policy, err := c.scheduleStore()
	if err != nil {
		return ScheduleBoard{}, err
	}
	m, _, err := store.Snapshot(ctx)
	if err != nil {
		return ScheduleBoard{}, err
	}
	out := ScheduleBoard{PausedAll: m.PausedAll, PausedReason: string(m.PausedReason), Schedules: []ScheduleView{}, Runs: []ScheduleRunView{},
		Policy: SchedulePolicyView{MinIntervalMinutes: policy.MinIntervalMinutes, MaxSchedules: policy.MaxSchedules, GlobalTokensDay: policy.GlobalTokensDay, ExpiryDays: policy.ExpiryDays}}
	out.Folders = c.scheduleFolderViews()
	out.Current = c.scheduleCurrentView(out.Folders)
	for _, s := range m.Schedules {
		out.Schedules = append(out.Schedules, scheduleView(s))
	}
	runs := m.Runs
	if len(runs) > 20 {
		runs = runs[len(runs)-20:]
	}
	for _, r := range slices.Backward(runs) {
		out.Runs = append(out.Runs, scheduleRunView(r))
	}
	return out, nil
}

// scheduleFolders is the trusted-project list a draft is matched against.
func (c *Controller) scheduleFolders() []string { return scheduler.Folders(c.WorkspaceRoot()) }

func (c *Controller) scheduleFolderViews() []ScheduleFolder {
	out := []ScheduleFolder{}
	for _, p := range c.scheduleFolders() {
		out = append(out, ScheduleFolder{Path: p, Name: filepath.Base(p)})
	}
	return out
}

func (c *Controller) scheduleCurrentView(trusted []ScheduleFolder) *ScheduleCurrent {
	root := c.WorkspaceRoot()
	if root == "" {
		return nil
	}
	for _, f := range trusted {
		if f.Path == root {
			return nil
		}
	}
	return &ScheduleCurrent{Path: root, Name: filepath.Base(root), Trust: string(c.WorkspaceTrust()), Trustable: TrustableFolder(root)}
}

func scheduleView(s schedule.Schedule) ScheduleView {
	v := ScheduleView{ID: s.ID, Label: s.Label, Status: string(s.Status), PausedReason: string(s.PausedReason), Workspace: s.Target.Workspace,
		Model: s.Model.Provider + "/" + s.Model.Model, Prompt: s.Prompt, PerRunTokens: s.Budget.PerRunTokens, LifetimeTokens: s.Budget.LifetimeTokens,
		SpentLifetimeTokens: s.SpentLifetimeTokens, ConsecutiveFailures: s.ConsecutiveFailures, ExpiresAtUnix: s.ExpiresAt.Unix()}
	if s.Trigger.Kind == schedule.TriggerEvery {
		v.EverySec = s.Trigger.EverySec
	} else {
		v.AtUnix = s.Trigger.At.Unix()
	}
	return v
}

func scheduleRunView(r schedule.Run) ScheduleRunView {
	v := ScheduleRunView{TriggerID: r.TriggerID, ScheduleID: r.ScheduleID, SlotUnix: r.SlotAt.Unix(), State: string(r.State), SkipReason: r.SkipReason, Charged: r.Charged}
	if !r.EndedAt.IsZero() {
		v.EndedUnix = r.EndedAt.Unix()
	}
	return v
}

func (d ScheduleDraft) draft() scheduler.Draft {
	out := scheduler.Draft{Label: d.Label, EverySec: d.EverySec, Workspace: d.Workspace, Prompt: d.Prompt, Model: d.Model}
	if d.AtUnix > 0 {
		out.At = time.Unix(d.AtUnix, 0)
	}
	return out
}

func (c *Controller) PreviewSchedule(_ context.Context, d ScheduleDraft) (SchedulePreview, error) {
	_, policy, err := c.scheduleStore()
	if err != nil {
		return SchedulePreview{}, err
	}
	req, err := d.draft().Request(c.scheduleFolders())
	if err != nil {
		return SchedulePreview{}, err
	}
	f := scheduler.FactsFor(req, policy, time.Now())
	return SchedulePreview{FirstRunUnix: f.FirstRun.Unix(), Workspace: f.Folder, Model: f.Model.Provider + "/" + f.Model.Model, Prompt: f.Prompt,
		PerRunTokens: f.Budget.PerRunTokens, PerRunSteps: f.Budget.PerRunSteps, PerRunWallSec: f.Budget.PerRunWallSec,
		PerDayMax: f.PerDayMax, Lifetime: f.Lifetime, ExpiresAtUnix: f.ExpiresAt.Unix(), Confirm: confirmToken(req, f, policy.ExpiryDays)}, nil
}

// confirmToken fingerprints everything the person was shown except the clock, so
// a creation that differs in any shown fact is refused.
func confirmToken(req schedule.CreateRequest, f scheduler.Facts, expiryDays int64) string {
	body, _ := json.Marshal(struct {
		Label     string
		Trigger   schedule.Trigger
		Folder    string
		Model     schedule.Model
		Prompt    string
		Budget    schedule.Budget
		PerDayMax int64
		Expiry    int64
	}{req.Label, schedule.Trigger{Kind: req.Trigger.Kind, EverySec: req.Trigger.EverySec, At: req.Trigger.At.UTC().Truncate(time.Second)}, f.Folder, f.Model, f.Prompt, f.Budget, f.PerDayMax, expiryDays})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (c *Controller) CreateSchedule(ctx context.Context, d ScheduleDraft, confirm string) (ScheduleView, error) {
	store, policy, err := c.scheduleStore()
	if err != nil {
		return ScheduleView{}, err
	}
	req, err := d.draft().Request(c.scheduleFolders())
	if err != nil {
		return ScheduleView{}, err
	}
	if confirm == "" || confirm != confirmToken(req, scheduler.FactsFor(req, policy, time.Now()), policy.ExpiryDays) {
		return ScheduleView{}, ErrScheduleConfirmMismatch
	}
	sc, err := store.Create(ctx, policy, req)
	if err != nil {
		return ScheduleView{}, err
	}
	return scheduleView(sc), nil
}

func (c *Controller) editSchedule(ctx context.Context, do func(context.Context, *schedule.Store, schedule.Policy) error) error {
	store, policy, err := c.scheduleStore()
	if err != nil {
		return err
	}
	return do(ctx, store, policy)
}

func (c *Controller) PauseSchedule(ctx context.Context, id string) error {
	return c.editSchedule(ctx, func(ctx context.Context, s *schedule.Store, _ schedule.Policy) error { return s.Pause(ctx, id) })
}

func (c *Controller) ResumeSchedule(ctx context.Context, id string) error {
	return c.editSchedule(ctx, func(ctx context.Context, s *schedule.Store, _ schedule.Policy) error { return s.Resume(ctx, id) })
}

func (c *Controller) RenewSchedule(ctx context.Context, id string) error {
	return c.editSchedule(ctx, func(ctx context.Context, s *schedule.Store, p schedule.Policy) error { return s.Renew(ctx, p, id) })
}

func (c *Controller) DeleteSchedule(ctx context.Context, id string) error {
	return c.editSchedule(ctx, func(ctx context.Context, s *schedule.Store, _ schedule.Policy) error { return s.Delete(ctx, id) })
}

func (c *Controller) SetSchedulesPaused(ctx context.Context, paused bool) error {
	return c.editSchedule(ctx, func(ctx context.Context, s *schedule.Store, _ schedule.Policy) error {
		return s.SetPausedAll(ctx, paused)
	})
}

func (c *Controller) ScheduleResult(ctx context.Context, triggerID string) (ScheduleResultView, error) {
	store, _, err := c.scheduleStore()
	if err != nil {
		return ScheduleResultView{}, err
	}
	r, err := store.GetResult(ctx, triggerID)
	if err != nil {
		return ScheduleResultView{}, err
	}
	out := ScheduleResultView{TriggerID: r.TriggerID, Report: r.Report, ReportTruncated: r.ReportTruncated, Pending: []SchedulePendingView{},
		PendingDropped: r.PendingDropped, Enforcement: r.Posture.Enforcement, RecordedUnix: r.RecordedAt.Unix()}
	for _, p := range r.Pending {
		out.Pending = append(out.Pending, SchedulePendingView{Kind: string(p.Kind), Summary: p.Summary, Detail: p.Detail})
	}
	return out, nil
}
