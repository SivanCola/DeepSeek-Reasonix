package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"reasonix/internal/daemon"
)

func daemonCommand(args []string) int {
	if len(args) < 1 {
		daemonUsage()
		return 2
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "start":
		return daemonStart(rest)
	case "status":
		return daemonStatus(rest)
	case "sessions":
		return daemonSessions(rest)
	case "stop":
		return daemonStopCmd(rest)
	case "help", "--help", "-h":
		daemonUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown daemon subcommand %q\n\n", sub)
		daemonUsage()
		return 2
	}
}

func daemonStart(args []string) int {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "监听地址")
	dir := fs.String("dir", "", "会话目录（默认用户配置）")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\ndaemon shutting down...")
		cancel()
	}()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	d := daemon.New(daemon.Options{
		Addr:       *addr,
		SessionDir: *dir,
		Logger:     logger,
	})

	if err := d.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func daemonStatus(args []string) int {
	fs := flag.NewFlagSet("daemon status", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "daemon 地址")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	resp, err := daemonGet(*addr, "/status")
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon not reachable: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	var status daemon.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		fmt.Fprintf(os.Stderr, "invalid response: %v\n", err)
		return 1
	}
	fmt.Printf("status: %s\n", status.Status)
	fmt.Printf("addr: %s\n", status.Addr)
	fmt.Printf("pid: %d\n", status.PID)
	fmt.Printf("sessions: %d\n", status.Sessions)
	fmt.Printf("uptime: %s\n", status.Uptime)
	return 0
}

func daemonSessions(args []string) int {
	fs := flag.NewFlagSet("daemon sessions", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "daemon 地址")
	jsonOut := fs.Bool("json", false, "JSON 格式输出")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	resp, err := daemonGet(*addr, "/sessions")
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon not reachable: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	var sessions daemon.SessionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		fmt.Fprintf(os.Stderr, "invalid response: %v\n", err)
		return 1
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(sessions)
	} else {
		if len(sessions.Sessions) == 0 {
			fmt.Println("no tracked sessions")
			return 0
		}
		for _, s := range sessions.Sessions {
			goal := s.GoalText
			if goal == "" {
				goal = "(none)"
			}
			fmt.Printf("  %s  goal=%s  status=%s  run=%s\n", s.ID[:8], truncate(goal, 40), s.GoalStatus, s.RunStatus)
		}
	}
	return 0
}

func daemonStopCmd(args []string) int {
	fs := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "daemon 地址")
	sessionID := fs.String("session", "", "要停止的 session ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "error: --session is required")
		return 2
	}

	body := fmt.Sprintf(`{"session_id":%q}`, *sessionID)
	resp, err := daemonPost(*addr, "/stop", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon not reachable: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "error: %s\n", string(b))
		return 1
	}
	fmt.Println(string(b))
	return 0
}

func daemonGet(addr, path string) (*http.Response, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	url := "http://" + addr + path
	return client.Get(url)
}

func daemonPost(addr, path, body string) (*http.Response, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := "http://" + addr + path
	return client.Post(url, "application/json", strings.NewReader(body))
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func daemonUsage() {
	fmt.Print(`reasonix daemon — 常驻后台 agent 服务

Usage:
  reasonix daemon start    [--addr HOST:PORT] [--dir PATH]
  reasonix daemon status   [--addr HOST:PORT]
  reasonix daemon sessions [--addr HOST:PORT] [--json]
  reasonix daemon stop     --session ID [--addr HOST:PORT]

Subcommands:
  start      启动 daemon（前台运行，Ctrl-C 停止）
  status     查询 daemon 状态
  sessions   列出所有跟踪的 session 及其 goal/run 状态
  stop       停止指定 session 的目标

The daemon scans session directories for *.runtime.json files, recovers
interrupted sessions, and exposes a localhost HTTP API for status queries
and goal continuation.
`)
}
