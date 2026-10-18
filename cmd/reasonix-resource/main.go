// reasonix-resource runs repository checks through the same user-wide resource
// admission as Reasonix shell tools. It intentionally boots no application state.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"reasonix/internal/proc"
	"reasonix/internal/resourcebudget"
)

func main() { os.Exit(run()) }

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	f := flag.NewFlagSet("reasonix-resource", flag.ContinueOnError)
	dir := f.String("cwd", "", "child working directory")
	status := f.Bool("status", false, "show shared reservations and policy as JSON")
	configure := f.Bool("configure", false, "update the shared user policy; omitted flags retain current values")
	cpu := f.Int("cpu", 0, "task CPU reservation")
	memory := f.Int("memory-mib", 0, "task memory reservation in MiB")
	budgetCPU := f.Int("cpu-budget", 0, "shared CPU budget (configure only)")
	budgetMemory := f.Int("memory-budget-mib", 0, "shared memory budget (configure only)")
	script := f.String("script", "", "run a package script using the platform shell")
	if err := f.Parse(os.Args[1:]); err != nil {
		return 2
	}
	args := f.Args()
	if *configure || *status {
		if len(args) > 0 || *script != "" {
			fmt.Fprintln(os.Stderr, "status/configure cannot run a command")
			return 2
		}
		s, err := resourcebudget.Status(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if *configure {
			c := s.Config
			if *budgetCPU != 0 {
				c.Budget.CPU = *budgetCPU
			}
			if *budgetMemory != 0 {
				c.Budget.MemoryMiB = *budgetMemory
			}
			if *cpu != 0 {
				c.Task.CPU = *cpu
			} else {
				c.Task.CPU = min(c.Task.CPU, c.Budget.CPU)
			}
			if *memory != 0 {
				c.Task.MemoryMiB = *memory
			} else {
				c.Task.MemoryMiB = min(c.Task.MemoryMiB, c.Budget.MemoryMiB)
			}
			if err := resourcebudget.Configure(ctx, c); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			s, err = resourcebudget.Status(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		if err := json.NewEncoder(os.Stdout).Encode(s); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	if *budgetCPU != 0 || *budgetMemory != 0 {
		fmt.Fprintln(os.Stderr, "budget flags require --configure")
		return 2
	}
	if len(args) == 0 && *script == "" {
		fmt.Fprintln(os.Stderr, "usage: reasonix-resource [--cpu N --memory-mib N] -- command [args...]")
		return 2
	}
	ctx = resourcebudget.WithRequest(ctx, resourcebudget.Resources{CPU: *cpu, MemoryMiB: *memory})
	ctx = resourcebudget.WithWaitDetails(ctx, func(w resourcebudget.WaitInfo) { fmt.Fprintln(os.Stderr, w.String()) })
	lease, err := resourcebudget.Acquire(ctx, "", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer lease.Release()
	var cmd *exec.Cmd
	if *script != "" {
		cmd = scriptCommand(ctx, *script, args)
	} else {
		cmd = proc.CommandContext(ctx, args[0], args[1:]...)
	}
	cmd.Dir = *dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = lease.Env(os.Environ())
	tracked, err := proc.RunCommand(ctx, cmd, proc.RunOptions{Track: true, Source: "resource_runner"})
	// Check runners own their whole process tree, including workers that
	// outlive a successful leader. Reap them before releasing admission.
	if tracked != nil {
		tracked.Kill()
	}
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}
