package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/config"
	"reasonix/internal/runtime/scheduler"
	"reasonix/internal/state/schedule"
	"reasonix/internal/state/workspacelist"
)

func schedulesController(t *testing.T) (*Controller, string) {
	t.Helper()
	home := testenv.TempDir(t)
	t.Setenv("REASONIX_HOME", home)
	body := "default_model = \"local/fake-model\"\n\n[[providers]]\nname = \"local\"\nkind = \"openai\"\nbase_url = \"http://example.invalid\"\nmodel = \"fake-model\"\napi_key_env = \"REASONIX_TEST_KEY\"\n"
	if err := os.WriteFile(config.UserConfigPath(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(Options{WorkspaceRoot: testenv.TempDir(t)})
	t.Cleanup(c.Close)
	ws := testenv.TempDir(t)
	trustProject(t, ws)
	return c, ws
}

// trustProject remembers dir as a project and marks it trusted, which is what
// makes it one a schedule may read.
func trustProject(t *testing.T, dir string) {
	t.Helper()
	list := filepath.Join(config.MemoryUserDir(), workspacelist.FileName)
	if err := workspacelist.Update(context.Background(), list, false, func(l *workspacelist.List) error {
		l.Paths = append(l.Paths, dir)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.NewProjectGrantStore(config.RootsForHome("").Home()).SetTrust(dir, config.WorkspaceTrusted); err != nil {
		t.Fatal(err)
	}
}

func draftIn(ws string) ScheduleDraft {
	return ScheduleDraft{Label: "morning", EverySec: 7200, Workspace: ws, Prompt: "summarize the TODOs"}
}

func TestAScheduleIsCreatedOnlyWithTheConfirmationOfItsOwnPreview(t *testing.T) {
	c, ws := schedulesController(t)
	ctx := context.Background()
	prev, err := c.PreviewSchedule(ctx, draftIn(ws))
	if err != nil || prev.Confirm == "" || prev.Model != "local/fake-model" || prev.PerRunTokens != 250_000 || prev.PerDayMax != 1_000_000 {
		t.Fatalf("preview = %+v, %v", prev, err)
	}
	for name, mutate := range map[string]func(*ScheduleDraft){
		"another prompt":   func(d *ScheduleDraft) { d.Prompt = "something else" },
		"another interval": func(d *ScheduleDraft) { d.EverySec = 3600 },
		"another label":    func(d *ScheduleDraft) { d.Label = "other" },
	} {
		d := draftIn(ws)
		mutate(&d)
		if _, err := c.CreateSchedule(ctx, d, prev.Confirm); !errors.Is(err, ErrScheduleConfirmMismatch) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := c.CreateSchedule(ctx, draftIn(ws), ""); !errors.Is(err, ErrScheduleConfirmMismatch) {
		t.Fatalf("no confirmation: %v", err)
	}
	board, _ := c.Schedules(ctx)
	if len(board.Schedules) != 0 {
		t.Fatal("a refused creation stored a schedule")
	}
	sc, err := c.CreateSchedule(ctx, draftIn(ws), prev.Confirm)
	if err != nil || sc.Status != "active" || sc.EverySec != 7200 || sc.Model != "local/fake-model" {
		t.Fatalf("created = %+v, %v", sc, err)
	}
	board, _ = c.Schedules(ctx)
	if len(board.Schedules) != 1 || board.Policy.MinIntervalMinutes != 60 || board.Policy.MaxSchedules != 8 {
		t.Fatalf("board = %+v", board)
	}
}

func TestSchedulesAreRefusedWithTypedReasons(t *testing.T) {
	c, ws := schedulesController(t)
	ctx := context.Background()
	d := draftIn(ws)
	d.EverySec = 60
	prev, err := c.PreviewSchedule(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateSchedule(ctx, d, prev.Confirm); !errors.Is(err, schedule.ErrIntervalBelowFloor) {
		t.Fatalf("an interval under the floor: %v", err)
	}
	for name, d := range map[string]ScheduleDraft{
		"no trigger":    {Workspace: ws, Prompt: "x"},
		"both":          {EverySec: 7200, AtUnix: 4102444800, Workspace: ws, Prompt: "x"},
		"no folder":     {EverySec: 7200, Workspace: ws + "/nope", Prompt: "x"},
		"unknown model": {EverySec: 7200, Workspace: ws, Prompt: "x", Model: "nobody/none"},
	} {
		if _, err := c.PreviewSchedule(ctx, d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSchedulesCanBePausedResumedRenewedAndDeleted(t *testing.T) {
	c, ws := schedulesController(t)
	ctx := context.Background()
	prev, _ := c.PreviewSchedule(ctx, draftIn(ws))
	sc, err := c.CreateSchedule(ctx, draftIn(ws), prev.Confirm)
	if err != nil {
		t.Fatal(err)
	}
	status := func() string {
		b, _ := c.Schedules(ctx)
		if len(b.Schedules) == 0 {
			return "gone"
		}
		return b.Schedules[0].Status
	}
	if err := c.PauseSchedule(ctx, sc.ID); err != nil || status() != "paused" {
		t.Fatalf("pause: %v %s", err, status())
	}
	if err := c.ResumeSchedule(ctx, sc.ID); err != nil || status() != "active" {
		t.Fatalf("resume: %v %s", err, status())
	}
	if err := c.RenewSchedule(ctx, sc.ID); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err := c.SetSchedulesPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := c.Schedules(ctx); !b.PausedAll {
		t.Fatal("the global switch did not show")
	}
	if err := c.DeleteSchedule(ctx, sc.ID); err != nil || status() != "gone" {
		t.Fatalf("delete: %v %s", err, status())
	}
	if err := c.PauseSchedule(ctx, "sch_missing"); !errors.Is(err, schedule.ErrNotFound) {
		t.Fatalf("an unknown id: %v", err)
	}
}

func TestAResultThatWasNeverRecordedIsATypedNotFound(t *testing.T) {
	c, _ := schedulesController(t)
	if _, err := c.ScheduleResult(context.Background(), "sch_x/1"); !errors.Is(err, schedule.ErrResultNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestAScheduleOnlyEverReadsATrustedProjectAndTheBoardListsThem(t *testing.T) {
	c, ws := schedulesController(t)
	ctx := context.Background()
	board, _ := c.Schedules(ctx)
	if len(board.Folders) != 1 || board.Folders[0].Path != ws || board.Folders[0].Name != filepath.Base(ws) {
		t.Fatalf("folders = %+v, want the one trusted project", board.Folders)
	}
	untrusted := testenv.TempDir(t)
	for name, folder := range map[string]string{
		"a project nobody trusted": untrusted,
		"the parent directory":     filepath.Dir(ws),
		"a path through ..":        filepath.Join(ws, "..", filepath.Base(untrusted)),
		"a relative path":          ".",
		"a missing folder":         filepath.Join(ws, "nope"),
	} {
		if _, err := c.PreviewSchedule(ctx, draftIn(folder)); !errors.Is(err, scheduler.ErrDraftFolderUnknown) {
			t.Errorf("%s: preview = %v, want ErrDraftFolderUnknown", name, err)
		}
		if _, err := c.CreateSchedule(ctx, draftIn(folder), "anything"); !errors.Is(err, scheduler.ErrDraftFolderUnknown) {
			t.Errorf("%s: create = %v, want ErrDraftFolderUnknown", name, err)
		}
	}
	if board, _ = c.Schedules(ctx); len(board.Schedules) != 0 {
		t.Fatal("a refused folder stored a schedule")
	}
	declined := testenv.TempDir(t)
	trustProject(t, declined)
	if err := config.NewProjectGrantStore(config.RootsForHome("").Home()).SetTrust(declined, config.WorkspaceTrustDeclined); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PreviewSchedule(ctx, draftIn(declined)); !errors.Is(err, scheduler.ErrDraftFolderUnknown) {
		t.Fatalf("a declined project = %v, want ErrDraftFolderUnknown", err)
	}
}
