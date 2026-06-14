package botruntime

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"reasonix/internal/bot"
	"reasonix/internal/config"
)

func TestRemoteRemembererKeepsDistinctGroupUsers(t *testing.T) {
	isolateUserConfig(t)
	cfg := config.Default()
	cfg.Bot.Connections = []config.BotConnectionConfig{
		{ID: "feishu-lark", Provider: "feishu", Domain: "lark", Label: "Lark", Enabled: true, Status: "connected"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	remember := NewRemoteRememberer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	remember(bot.InboundMessage{
		Platform:     bot.PlatformFeishu,
		ConnectionID: "feishu-lark",
		Domain:       "lark",
		ChatType:     bot.ChatGroup,
		ChatID:       "oc-group-1",
		UserID:       "ou-user-1",
	})
	remember(bot.InboundMessage{
		Platform:     bot.PlatformFeishu,
		ConnectionID: "feishu-lark",
		Domain:       "lark",
		ChatType:     bot.ChatGroup,
		ChatID:       "oc-group-1",
		UserID:       "ou-user-2",
	})

	got := config.LoadForEdit(config.UserConfigPath())
	if users := got.Bot.Allowlist.FeishuUsers; len(users) != 2 || users[0] != "ou-user-1" || users[1] != "ou-user-2" {
		t.Fatalf("feishu users = %+v, want both group users", users)
	}
	if groups := got.Bot.Allowlist.FeishuGroups; len(groups) != 1 || groups[0] != "oc-group-1" {
		t.Fatalf("feishu groups = %+v, want group once", groups)
	}
	if mappings := got.Bot.Connections[0].SessionMappings; len(mappings) != 1 || mappings[0].RemoteID != "oc-group-1" {
		t.Fatalf("session mappings = %+v, want group remote once", mappings)
	}
}

func TestRememberInboundSessionFillsExistingMappingSessionID(t *testing.T) {
	isolateUserConfig(t)
	cfg := config.Default()
	cfg.Bot.Connections = []config.BotConnectionConfig{
		{ID: "weixin-weixin", Provider: "weixin", Domain: "weixin", Label: "微信", Enabled: true, Status: "connected"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	msg := bot.InboundMessage{
		Platform:     bot.PlatformWeixin,
		ConnectionID: "weixin-weixin",
		Domain:       "weixin",
		ChatType:     bot.ChatDM,
		ChatID:       "wx-chat-1",
		UserID:       "wx-user-1",
	}
	if err := RememberInbound(msg); err != nil {
		t.Fatalf("remember inbound: %v", err)
	}
	if err := RememberInboundSession(msg, "20260614-120000.000000000-deepseek"); err != nil {
		t.Fatalf("remember inbound session: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	mappings := got.Bot.Connections[0].SessionMappings
	if len(mappings) != 1 {
		t.Fatalf("mappings = %+v, want one mapping", mappings)
	}
	if mappings[0].RemoteID != "wx-chat-1" || mappings[0].SessionID != "20260614-120000.000000000-deepseek" {
		t.Fatalf("mapping = %+v, want remote chat with session id", mappings[0])
	}
}

func TestRememberInboundSessionCreatesMappingWithSessionID(t *testing.T) {
	isolateUserConfig(t)
	cfg := config.Default()
	cfg.Bot.Connections = []config.BotConnectionConfig{
		{ID: "feishu-lark", Provider: "feishu", Domain: "lark", Label: "Lark", Enabled: true, Status: "connected"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := RememberInboundSession(bot.InboundMessage{
		Platform:     bot.PlatformFeishu,
		ConnectionID: "feishu-lark",
		Domain:       "lark",
		ChatType:     bot.ChatDM,
		ChatID:       "oc-chat-1",
		UserID:       "ou-user-1",
	}, "topic-bot"); err != nil {
		t.Fatalf("remember inbound session: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	mappings := got.Bot.Connections[0].SessionMappings
	if len(mappings) != 1 || mappings[0].RemoteID != "oc-chat-1" || mappings[0].SessionID != "topic-bot" {
		t.Fatalf("mappings = %+v, want mapping with session id", mappings)
	}
}

func TestConnectionChannelConfigsPreserveToolApprovalMode(t *testing.T) {
	connections := []config.BotConnectionConfig{
		{ID: "feishu-feishu", Provider: "feishu", Domain: "feishu", Enabled: true, ToolApprovalMode: "auto"},
		{ID: "feishu-lark", Provider: "feishu", Domain: "lark", Enabled: true, ToolApprovalMode: "yolo"},
		{ID: "weixin-weixin", Provider: "weixin", Domain: "weixin", Enabled: true, ToolApprovalMode: "ask"},
	}

	byConnection := ConnectionChannelConfigs(connections, true, true)
	if got := byConnection["feishu-feishu"].ToolApprovalMode; got != "auto" {
		t.Fatalf("feishu tool approval mode = %q, want auto", got)
	}
	if got := byConnection["feishu-lark"].ToolApprovalMode; got != "yolo" {
		t.Fatalf("lark tool approval mode = %q, want yolo", got)
	}
	if got := byConnection["weixin-weixin"].ToolApprovalMode; got != "ask" {
		t.Fatalf("weixin tool approval mode = %q, want explicit ask override", got)
	}

	byPlatform := ChannelConfigs(connections, true, true)
	if got := byPlatform[bot.PlatformFeishu].ToolApprovalMode; got != "yolo" {
		t.Fatalf("platform feishu tool approval mode = %q, want last enabled Feishu/Lark override", got)
	}
}

func isolateUserConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	t.Chdir(t.TempDir())
}
