package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"reasonix/internal/base/secrets"
	"reasonix/internal/runtime/schedrun"
	"reasonix/internal/runtime/scheduler"
	"reasonix/internal/state/schedule"
)

// Exit statuses of the person-facing schedule commands.
const (
	scheduleExitOK       = 0
	scheduleExitFailed   = 1
	scheduleExitUsage    = 2
	scheduleExitNotHuman = 77
)

const scheduleUsage = `usage: reasonix schedule <command>
  add --every 2h|--at <time> --prompt <text> [--workspace dir] [--label name] [--model provider/model]
  list
  pause <id> | resume <id> | renew <id> | rm <id>
  pause-all | resume-all
  tick   claim and run what is due now, then exit (for an OS scheduler to call)
Creating, resuming and deleting need a terminal: a person confirms them by typing back the plan id.`

// scheduleSupervisorForCLI builds the runner a tick hands claimed runs to.
var scheduleSupervisorForCLI = func(store *schedule.Store, p schedule.Policy) scheduler.Runner {
	return &schedrun.Supervisor{Store: store, Policy: p}
}

type scheduleIO struct {
	in    io.Reader
	out   io.Writer
	err   io.Writer
	human bool
	// agentChild is set when the host started this process for an agent tool.
	agentChild bool
	now        func() time.Time
}

func scheduleOS() scheduleIO {
	human := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	return scheduleIO{in: os.Stdin, out: os.Stdout, err: os.Stderr, human: human, agentChild: inAgentChild(), now: time.Now}
}

func scheduleManage(args []string, sio scheduleIO) int {
	if len(args) == 0 {
		fmt.Fprintln(sio.err, scheduleUsage)
		return scheduleExitUsage
	}
	store, policy, err := openScheduleStore(sio.now)
	if err != nil {
		fmt.Fprintln(sio.err, "schedule:", err)
		return scheduleExitFailed
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list":
		return scheduleList(store, sio)
	case "tick":
		return scheduleTick(store, policy, sio)
	case "pause-all":
		return scheduleSwitch(store, sio, func(ctx context.Context) error { return store.SetPausedAll(ctx, true) }, "all schedules paused")
	case "resume-all":
		if !requireHuman(sio, "resume-all") {
			return scheduleExitNotHuman
		}
		return scheduleSwitch(store, sio, func(ctx context.Context) error { return store.SetPausedAll(ctx, false) }, "schedules resumed")
	case "pause", "resume", "renew", "rm":
		return scheduleOne(cmd, rest, store, policy, sio)
	case "add":
		return scheduleAdd(rest, store, policy, sio)
	}
	fmt.Fprintln(sio.err, scheduleUsage)
	return scheduleExitUsage
}

func openScheduleStore(now func() time.Time) (*schedule.Store, schedule.Policy, error) {
	return scheduler.OpenUserStore(now)
}

// requireHuman refuses what only a person at a terminal may do. A command run by
// a tool, a pipe or a scheduler has no terminal, so it cannot create, resume or
// delete a schedule. A terminal an agent tool opened itself is refused by the
// marker the host puts on its subprocesses; that is a guard against a pty trick,
// not a boundary, because a child can clear its own environment.
func requireHuman(sio scheduleIO, what string) bool {
	if sio.agentChild {
		fmt.Fprintf(sio.err, "schedule %s is refused: this command was started by an agent tool (%s is set)\n", what, secrets.AgentChildEnv)
		return false
	}
	if sio.human {
		return true
	}
	fmt.Fprintf(sio.err, "schedule %s needs a person at a terminal to confirm it\n", what)
	return false
}

func scheduleSwitch(_ *schedule.Store, sio scheduleIO, do func(context.Context) error, done string) int {
	if err := do(context.Background()); err != nil {
		fmt.Fprintln(sio.err, "schedule:", err)
		return scheduleExitFailed
	}
	fmt.Fprintln(sio.out, done)
	return scheduleExitOK
}

func scheduleOne(cmd string, args []string, store *schedule.Store, policy schedule.Policy, sio scheduleIO) int {
	if len(args) != 1 {
		fmt.Fprintln(sio.err, scheduleUsage)
		return scheduleExitUsage
	}
	if cmd != "pause" && !requireHuman(sio, cmd) {
		return scheduleExitNotHuman
	}
	ctx := context.Background()
	var err error
	switch cmd {
	case "pause":
		err = store.Pause(ctx, args[0])
	case "resume":
		err = store.Resume(ctx, args[0])
	case "rm":
		err = store.Delete(ctx, args[0])
	case "renew":
		err = store.Renew(ctx, policy, args[0])
	}
	if err != nil {
		fmt.Fprintln(sio.err, "schedule:", err)
		return scheduleExitFailed
	}
	fmt.Fprintf(sio.out, "%s %s\n", cmd, args[0])
	return scheduleExitOK
}

func scheduleList(store *schedule.Store, sio scheduleIO) int {
	m, _, err := store.Snapshot(context.Background())
	if err != nil {
		fmt.Fprintln(sio.err, "schedule:", err)
		return scheduleExitFailed
	}
	if m.PausedAll {
		fmt.Fprintln(sio.out, "ALL SCHEDULES ARE PAUSED")
	}
	if len(m.Schedules) == 0 {
		fmt.Fprintln(sio.out, "no schedules")
	}
	for _, s := range m.Schedules {
		fmt.Fprintf(sio.out, "%s  %-9s  %s  %s  spent %d of %d tokens  expires %s  created by %s\n", s.ID, s.Status, describeTrigger(s.Trigger), displayLabel(s),
			s.SpentLifetimeTokens, s.Budget.LifetimeTokens, s.ExpiresAt.Local().Format("2006-01-02"), createdBy(s))
	}
	runs := m.Runs
	if len(runs) > 10 {
		runs = runs[len(runs)-10:]
	}
	for _, r := range runs {
		fmt.Fprintf(sio.out, "  run %s  %s  %d tokens  %s\n", r.TriggerID, r.State, r.Charged, r.SkipReason)
	}
	return scheduleExitOK
}

func createdBy(s schedule.Schedule) string {
	if s.Confirmed.Via == "" {
		return "unknown"
	}
	return s.Confirmed.Via
}

func displayLabel(s schedule.Schedule) string {
	if s.Label != "" {
		return fmt.Sprintf("%q", s.Label)
	}
	return filepath.Base(s.Target.Workspace)
}

func describeTrigger(t schedule.Trigger) string {
	if t.Kind == schedule.TriggerAt {
		return "at " + t.At.Local().Format("2006-01-02 15:04")
	}
	return "every " + (time.Duration(t.EverySec) * time.Second).String()
}

func scheduleTick(store *schedule.Store, p schedule.Policy, sio scheduleIO) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := (&scheduler.Scheduler{Store: store, Policy: p, Runner: scheduleSupervisorForCLI(store, p), Now: sio.now}).Tick(ctx)
	if res.Paused {
		fmt.Fprintln(sio.out, "all schedules are paused; nothing was started")
	}
	for _, id := range res.Reaped {
		fmt.Fprintln(sio.out, "reaped", id)
	}
	for _, s := range res.Skipped {
		fmt.Fprintf(sio.out, "skipped %s at %s: %s\n", s.ScheduleID, s.Slot.Local().Format(time.RFC3339), s.Reason)
	}
	for _, r := range res.Refused {
		fmt.Fprintf(sio.out, "refused %s: %v\n", r.ScheduleID, r.Err)
	}
	for _, r := range res.Reports {
		fmt.Fprintf(sio.out, "ran %s: %s %s\n", r.TriggerID, r.State, r.Code)
	}
	if err != nil {
		fmt.Fprintln(sio.err, "schedule:", err)
		return scheduleExitFailed
	}
	return scheduleExitOK
}

type addFlags struct {
	every, at, prompt, workspace, label, model string
}

func scheduleAdd(args []string, store *schedule.Store, p schedule.Policy, sio scheduleIO) int {
	fs := flag.NewFlagSet("schedule add", flag.ContinueOnError)
	fs.SetOutput(sio.err)
	var f addFlags
	fs.StringVar(&f.every, "every", "", "repeat at this interval, for example 2h")
	fs.StringVar(&f.at, "at", "", "run once at this time (RFC 3339, or 2006-01-02 15:04 local)")
	fs.StringVar(&f.prompt, "prompt", "", "what the run should do")
	fs.StringVar(&f.workspace, "workspace", "", "the folder it works in (default: the current directory)")
	fs.StringVar(&f.label, "label", "", "an optional name")
	fs.StringVar(&f.model, "model", "", "provider/model (default: your default model)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return scheduleExitUsage
	}
	if !requireHuman(sio, "add") {
		return scheduleExitNotHuman
	}
	req, err := f.request()
	if err != nil {
		fmt.Fprintln(sio.err, "schedule:", err)
		return scheduleExitUsage
	}
	plan := preview(req, p, sio.now())
	code := planCode(plan)
	fmt.Fprint(sio.out, plan)
	fmt.Fprintf(sio.out, "Type %s to create this schedule, anything else cancels: ", code)
	line, _ := bufio.NewReader(sio.in).ReadString('\n')
	if strings.TrimSpace(line) != code {
		fmt.Fprintln(sio.out, "not created")
		return scheduleExitOK
	}
	sc, err := store.Create(context.Background(), p, req)
	if err != nil {
		fmt.Fprintln(sio.err, "schedule:", err)
		return scheduleExitFailed
	}
	fmt.Fprintf(sio.out, "created %s\n", sc.ID)
	return scheduleExitOK
}

func (f addFlags) request() (schedule.CreateRequest, error) {
	d := scheduler.Draft{Label: f.label, Prompt: f.prompt, Workspace: f.workspace, Model: f.model}
	wd, err := os.Getwd()
	if err != nil {
		return schedule.CreateRequest{}, err
	}
	if d.Workspace == "" {
		d.Workspace = wd
	}
	switch {
	case (f.every == "") == (f.at == ""):
		return schedule.CreateRequest{}, errors.New("give exactly one of --every and --at")
	case f.every != "":
		dur, err := time.ParseDuration(f.every)
		if err != nil || dur <= 0 {
			return schedule.CreateRequest{}, fmt.Errorf("--every %q is not a duration such as 90m or 2h", f.every)
		}
		d.EverySec = int64(dur / time.Second)
	default:
		at, err := parseAt(f.at)
		if err != nil {
			return schedule.CreateRequest{}, err
		}
		d.At = at
	}
	req, err := d.Request(scheduler.Folders(wd))
	switch {
	case errors.Is(err, scheduler.ErrDraftFolderUnknown):
		return req, fmt.Errorf("%q is not a trusted project: open it in Studio or the terminal UI and trust it first, then run this again", d.Workspace)
	case errors.Is(err, scheduler.ErrDraftWorkspace):
		return req, fmt.Errorf("--workspace %q is not a folder", f.workspace)
	case errors.Is(err, scheduler.ErrDraftModel):
		return req, fmt.Errorf("%w; give one with --model", err)
	}
	req.ConfirmedVia = schedule.ConfirmedViaTerminal
	return req, err
}

func parseAt(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("--at %q is not a time (RFC 3339, or 2006-01-02 15:04 in local time)", s)
}

// planCode is the short id a person types back: it names the exact plan shown,
// so a confirmation typed for one plan cannot be replayed for another.
func planCode(plan string) string {
	sum := sha256.Sum256([]byte(plan))
	return hex.EncodeToString(sum[:3])
}

// preview is what a person confirms: when it first runs, what it may do, and the
// most it can spend, in the policy's own numbers.
func preview(req schedule.CreateRequest, p schedule.Policy, now time.Time) string {
	f := scheduler.FactsFor(req, p, now)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Schedule %s\n", describeTrigger(req.Trigger))
	if req.Trigger.Kind == schedule.TriggerEvery {
		fmt.Fprintf(&sb, "  first run:   %s\n", f.FirstRun.Local().Format("2006-01-02 15:04"))
	}
	fmt.Fprintf(&sb, "  folder:      %s (read-only: it can read files here and write nothing)\n", f.Folder)
	fmt.Fprintf(&sb, "  model:       %s/%s\n", f.Model.Provider, f.Model.Model)
	fmt.Fprintf(&sb, "  prompt:      %s\n", f.Prompt)
	fmt.Fprintf(&sb, "  each run:    up to %d tokens, %d steps, %d s\n", f.Budget.PerRunTokens, f.Budget.PerRunSteps, f.Budget.PerRunWallSec)
	fmt.Fprintf(&sb, "  at most:     %d tokens per day, %d over its lifetime; it ends on its own after %d days\n", f.PerDayMax, f.Lifetime, p.ExpiryDays)
	sb.WriteString("  Anything that needs your approval is parked for you, never answered for you.\n")
	return sb.String()
}
