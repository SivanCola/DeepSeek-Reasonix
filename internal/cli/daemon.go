package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"reasonix/internal/config"
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
	case "doctor":
		return daemonDoctor(rest)
	case "sessions":
		return daemonSessions(rest)
	case "timeline":
		return daemonTimeline(rest)
	case "stop":
		return daemonStopCmd(rest)
	case "continue":
		return daemonContinueCmd(rest)
	case "schedule":
		return daemonScheduleCmd(rest)
	case "approve":
		return daemonApprovalCmd(rest, true)
	case "deny":
		return daemonApprovalCmd(rest, false)
	case "answer":
		return daemonAnswerCmd(rest)
	case "help", "--help", "-h":
		daemonUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown daemon subcommand %q\n\n", sub)
		daemonUsage()
		return 2
	}
}

func daemonTimeline(args []string) int {
	fs := flag.NewFlagSet("daemon timeline", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "daemon 地址")
	dir := fs.String("dir", "", "会话目录（用于读取本地 token）")
	sessionID := fs.String("session", "", "session ID")
	limit := fs.Int("limit", 50, "最多显示多少条事件，0 表示全部")
	jsonOut := fs.Bool("json", false, "JSON 格式输出")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "error: --session is required")
		return 2
	}
	q := url.Values{}
	q.Set("session_id", *sessionID)
	q.Set("limit", fmt.Sprintf("%d", *limit))
	resp, err := daemonGet(*addr, *dir, "/timeline?"+q.Encode())
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon not reachable: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: %s\n", string(b))
		return 1
	}
	var timeline daemon.TimelineResponse
	if err := json.NewDecoder(resp.Body).Decode(&timeline); err != nil {
		fmt.Fprintf(os.Stderr, "invalid response: %v\n", err)
		return 1
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(timeline)
		return 0
	}
	if len(timeline.Events) == 0 {
		fmt.Println("no timeline events")
		return 0
	}
	for _, e := range timeline.Events {
		when := e.Time.Local().Format("2006-01-02 15:04:05")
		parts := []string{when, e.Type}
		if e.Source != "" {
			parts = append(parts, "source="+e.Source)
		}
		if e.RunStatus != "" {
			parts = append(parts, "run="+e.RunStatus)
		}
		if e.WaitKind != "" {
			wait := e.WaitKind
			if e.WaitID != "" {
				wait += ":" + e.WaitID
			}
			parts = append(parts, "wait="+wait)
		}
		if e.Error != "" {
			parts = append(parts, "error="+e.Error)
		}
		fmt.Println(strings.Join(parts, "  "))
	}
	return 0
}

func daemonStart(args []string) int {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "监听地址")
	dir := fs.String("dir", "", "会话目录（默认用户配置）")
	webhook := fs.Bool("webhook", false, "启用 /webhook 外部事件入口")
	webhookSecret := fs.String("webhook-secret", "", "webhook HMAC secret（也可用 REASONIX_DAEMON_WEBHOOK_SECRET）")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	webhookCfg, err := resolveDaemonWebhookConfig(*webhook, *webhookSecret, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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
		Webhook:    webhookCfg,
	})

	if err := d.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func resolveDaemonWebhookConfig(enabled bool, secret string, getenv func(string) string) (*daemon.WebhookConfig, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" && getenv != nil {
		secret = strings.TrimSpace(getenv("REASONIX_DAEMON_WEBHOOK_SECRET"))
	}
	if !enabled && secret == "" {
		return nil, nil
	}
	if secret == "" {
		return nil, fmt.Errorf("--webhook requires --webhook-secret or REASONIX_DAEMON_WEBHOOK_SECRET")
	}
	return &daemon.WebhookConfig{Enabled: true, Secret: secret}, nil
}

func daemonStatus(args []string) int {
	fs := flag.NewFlagSet("daemon status", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "daemon 地址")
	dir := fs.String("dir", "", "会话目录（用于读取本地 token）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	resp, err := daemonGet(*addr, *dir, "/status")
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
	dir := fs.String("dir", "", "会话目录（用于读取本地 token）")
	jsonOut := fs.Bool("json", false, "JSON 格式输出")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	resp, err := daemonGet(*addr, *dir, "/sessions")
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
			wait := ""
			if s.WaitKind != "" {
				wait = "  wait=" + s.WaitKind
				if s.WaitID != "" {
					wait += ":" + s.WaitID
				}
				if s.WaitTool != "" {
					wait += "(" + s.WaitTool + ")"
				}
			}
			active := ""
			if s.Active {
				active = "  active=true"
			}
			fmt.Printf("  %s  goal=%s  status=%s  run=%s%s%s\n", s.ID[:8], truncate(goal, 40), s.GoalStatus, s.RunStatus, wait, active)
		}
	}
	return 0
}

func daemonStopCmd(args []string) int {
	fs := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "daemon 地址")
	dir := fs.String("dir", "", "会话目录（用于读取本地 token）")
	sessionID := fs.String("session", "", "要停止的 session ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "error: --session is required")
		return 2
	}

	body := fmt.Sprintf(`{"session_id":%q}`, *sessionID)
	resp, err := daemonPost(*addr, *dir, "/stop", body)
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

func daemonContinueCmd(args []string) int {
	fs := flag.NewFlagSet("daemon continue", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "daemon 地址")
	dir := fs.String("dir", "", "会话目录（用于读取本地 token）")
	sessionID := fs.String("session", "", "要继续的 session ID")
	reason := fs.String("reason", "cli", "唤醒原因")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "error: --session is required")
		return 2
	}
	body := fmt.Sprintf(`{"session_id":%q,"reason":%q}`, *sessionID, *reason)
	resp, err := daemonPost(*addr, *dir, "/continue-goal", body)
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

func daemonScheduleCmd(args []string) int {
	fs := flag.NewFlagSet("daemon schedule", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "daemon 地址")
	dir := fs.String("dir", "", "会话目录（用于读取本地 token）")
	sessionID := fs.String("session", "", "要调度的 session ID")
	dailyAt := fs.String("daily-at", "", "每日唤醒时间 HH:MM")
	interval := fs.String("interval", "", "固定间隔，例如 1h")
	enabled := fs.Bool("enable", true, "是否启用调度")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "error: --session is required")
		return 2
	}
	body := fmt.Sprintf(`{"session_id":%q,"enabled":%t`, *sessionID, *enabled)
	if *dailyAt != "" {
		body += fmt.Sprintf(`,"daily_at":%q`, *dailyAt)
	}
	if *interval != "" {
		body += fmt.Sprintf(`,"interval":%q`, *interval)
	}
	body += "}"
	resp, err := daemonPost(*addr, *dir, "/schedule", body)
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

func daemonApprovalCmd(args []string, allow bool) int {
	name := "daemon approve"
	path := "/approvals/approve"
	if !allow {
		name = "daemon deny"
		path = "/approvals/deny"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "daemon 地址")
	dir := fs.String("dir", "", "会话目录（用于读取本地 token）")
	sessionID := fs.String("session", "", "session ID")
	approvalID := fs.String("approval", "", "approval ID")
	sessionGrant := fs.Bool("session-grant", false, "本 session 内记住该批准范围")
	persist := fs.Bool("persist", false, "持久化批准规则")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *sessionID == "" || *approvalID == "" {
		fmt.Fprintln(os.Stderr, "error: --session and --approval are required")
		return 2
	}
	body := fmt.Sprintf(`{"session_id":%q,"approval_id":%q,"session":%t,"persist":%t}`, *sessionID, *approvalID, *sessionGrant, *persist)
	resp, err := daemonPost(*addr, *dir, path, body)
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

func daemonAnswerCmd(args []string) int {
	fs := flag.NewFlagSet("daemon answer", flag.ContinueOnError)
	addr := fs.String("addr", daemon.DefaultAddr, "daemon 地址")
	dir := fs.String("dir", "", "会话目录（用于读取本地 token）")
	sessionID := fs.String("session", "", "session ID")
	askID := fs.String("ask", "", "ask ID")
	selected := fs.String("selected", "", "选择/回答文本")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *sessionID == "" || *askID == "" || *selected == "" {
		fmt.Fprintln(os.Stderr, "error: --session, --ask and --selected are required")
		return 2
	}
	body := fmt.Sprintf(`{"session_id":%q,"ask_id":%q,"selected":%q}`, *sessionID, *askID, *selected)
	resp, err := daemonPost(*addr, *dir, "/asks/answer", body)
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

func daemonGet(addr, dir, path string) (*http.Response, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	url := "http://" + addr + path
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	addDaemonAuth(req, dir)
	return client.Do(req)
}

func daemonPost(addr, dir, path, body string) (*http.Response, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := "http://" + addr + path
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	addDaemonAuth(req, dir)
	return client.Do(req)
}

func addDaemonAuth(req *http.Request, dir string) {
	token := readDaemonToken(dir)
	if token != "" {
		req.Header.Set("X-Reasonix-Daemon-Token", token)
	}
}

func readDaemonToken(dir string) string {
	if strings.TrimSpace(dir) == "" {
		dir = config.SessionDir()
	}
	b, err := os.ReadFile(daemon.TokenFile(dir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
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
  reasonix daemon start    [--addr HOST:PORT] [--dir PATH] [--webhook --webhook-secret SECRET]
  reasonix daemon status   [--addr HOST:PORT] [--dir PATH]
  reasonix daemon doctor   [--addr HOST:PORT] [--dir PATH] [--json]
  reasonix daemon sessions [--addr HOST:PORT] [--dir PATH] [--json]
  reasonix daemon timeline --session ID [--limit N] [--json]
  reasonix daemon continue --session ID [--addr HOST:PORT] [--dir PATH]
  reasonix daemon schedule --session ID [--daily-at HH:MM | --interval 1h]
  reasonix daemon approve  --session ID --approval ID
  reasonix daemon deny     --session ID --approval ID
  reasonix daemon answer   --session ID --ask ID --selected TEXT
  reasonix daemon stop     --session ID [--addr HOST:PORT] [--dir PATH]

Subcommands:
  start      启动 daemon（前台运行，Ctrl-C 停止）
  status     查询 daemon 状态
  doctor     检查 daemon token、lock、runtime sidecar 和在线状态
  sessions   列出所有跟踪的 session 及其 goal/run 状态
  timeline   查看指定 session 的运行事件时间线
  continue   显式唤醒并继续指定 goal
  schedule   设置 daily/interval 定时唤醒
  approve    批准 daemon 中等待的审批
  deny       拒绝 daemon 中等待的审批
  answer     回答 daemon 中等待的 ask 问题
  stop       停止指定 session 的目标

The daemon scans session directories for *.runtime.json files, recovers
interrupted sessions, and exposes a localhost HTTP API for status queries
and goal continuation. The local HTTP API uses a token stored beside the
session directory. Webhooks require an HMAC secret supplied by
--webhook-secret or REASONIX_DAEMON_WEBHOOK_SECRET.
`)
}
