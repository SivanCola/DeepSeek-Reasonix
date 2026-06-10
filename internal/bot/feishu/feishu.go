// Package feishu 实现飞书自建应用 Bot 适配器。
// 参考 Hermes Agent 的 feishu adapter：
// - 长连接 WebSocket（默认）或 Webhook 模式
// - @mention gating
// - open_id / user_id / union_id 映射
// - 消息去重
// - interactive card 审批/问答
package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"reasonix/internal/bot"
	"reasonix/internal/config"

	"golang.org/x/net/websocket"
)

const (
	feishuTokenURL    = "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
	feishuReplyMsgURL = "https://open.feishu.cn/open-apis/im/v1/messages/%s/reply"
	feishuSendMsgURL  = "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id"
	feishuUserInfoURL = "https://open.feishu.cn/open-apis/contact/v3/users/%s"
	feishuWSSURL      = "wss://open.feishu.cn/open-apis/ws"
)

// textContent 飞书消息文本内容结构。
type textContent struct {
	Text string `json:"text"`
}

// feishuEvent 飞书事件结构。
type feishuEvent struct {
	Schema string          `json:"schema"`
	Header feishuHeader    `json:"header"`
	Event  json.RawMessage `json:"event"`
}

type feishuHeader struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	Token      string `json:"token"`
	CreateTime string `json:"create_time"`
}

type feishuMsgEvent struct {
	MessageID string          `json:"message_id"`
	RootID    string          `json:"root_id"`
	ParentID  string          `json:"parent_id"`
	ChatID    string          `json:"chat_id"`
	ChatType  string          `json:"chat_type"`
	MsgType   string          `json:"msg_type"`
	Content   string          `json:"content"`
	Sender    feishuSender    `json:"sender"`
	Mentions  []feishuMention `json:"mentions"`
}

type feishuSender struct {
	SenderID struct {
		UserID  string `json:"user_id"`
		OpenID  string `json:"open_id"`
		UnionID string `json:"union_id"`
	} `json:"sender_id"`
}

type feishuMention struct {
	Key string `json:"key"`
	ID  struct {
		OpenID string `json:"open_id"`
	} `json:"id"`
}

type feishuTenantToken struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"`
}

// adapter 飞书适配器实现。
type adapter struct {
	cfg         config.FeishuBotConfig
	logger      *slog.Logger
	msgCh       chan bot.InboundMessage
	cancel      context.CancelFunc
	token       string
	tokenExpiry time.Time

	seen map[string]bool // 消息去重
}

// New 创建飞书 Bot 适配器。
func New(cfg config.FeishuBotConfig, logger *slog.Logger) bot.Adapter {
	return &adapter{
		cfg:    cfg,
		logger: logger.With("platform", "feishu"),
		seen:   make(map[string]bool),
	}
}

func (a *adapter) Platform() bot.Platform { return bot.PlatformFeishu }
func (a *adapter) Name() string           { return "feishu" }

func (a *adapter) Start(ctx context.Context) error {
	a.msgCh = make(chan bot.InboundMessage, 64)
	ctx, a.cancel = context.WithCancel(ctx)

	mode := a.cfg.Mode
	if mode == "" {
		mode = "webhook"
	}

	switch mode {
	case "webhook":
		go a.runWebhook(ctx)
	default:
		go a.runWebSocket(ctx)
	}
	return nil
}

func (a *adapter) Stop() error {
	if a.cancel != nil {
		a.cancel()
	}
	return nil
}

func (a *adapter) Send(ctx context.Context, msg bot.OutboundMessage) (bot.SendResult, error) {
	return a.sendMessage(ctx, msg)
}

func (a *adapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

func (a *adapter) Messages() <-chan bot.InboundMessage {
	return a.msgCh
}

// getTenantToken 获取飞书 tenant access token。
func (a *adapter) getTenantToken(ctx context.Context) (string, error) {
	if a.token != "" && time.Now().Before(a.tokenExpiry) {
		return a.token, nil
	}
	secret := os.Getenv(a.cfg.AppSecretEnv)
	if a.cfg.AppID == "" || secret == "" {
		return "", fmt.Errorf("feishu app_id or %s is not configured", a.cfg.AppSecretEnv)
	}

	body := fmt.Sprintf(`{"app_id":"%s","app_secret":"%s"}`,
		a.cfg.AppID, secret)

	req, err := http.NewRequestWithContext(ctx, "POST", feishuTokenURL, bytes.NewBufferString(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var token feishuTenantToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", err
	}
	if token.Code != 0 {
		return "", fmt.Errorf("feishu token error: %s", token.Msg)
	}

	a.token = token.TenantAccessToken
	if token.Expire > 60 {
		a.tokenExpiry = time.Now().Add(time.Duration(token.Expire-60) * time.Second)
	} else {
		a.tokenExpiry = time.Now().Add(5 * time.Minute)
	}
	return a.token, nil
}

// runWebSocket 启动飞书 WebSocket 长连接。
func (a *adapter) runWebSocket(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := a.connectWSS(ctx); err != nil {
			a.logger.Error("feishu websocket error", "err", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func (a *adapter) connectWSS(ctx context.Context) error {
	token, err := a.getTenantToken(ctx)
	if err != nil {
		return err
	}

	cfg, err := websocket.NewConfig(feishuWSSURL, feishuWSSURL)
	if err != nil {
		return err
	}
	cfg.Header = http.Header{}
	cfg.Header.Set("Authorization", "Bearer "+token)

	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		return fmt.Errorf("dial feishu wss: %w", err)
	}
	defer conn.Close()

	decoder := json.NewDecoder(conn)

	type wsPacket struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}

	// 发送建立连接事件
	establish := map[string]interface{}{
		"type": "url_verification",
	}
	data, _ := json.Marshal(establish)
	var pkt wsPacket
	pkt.Type = "event"
	pkt.Data = data

	encoder := json.NewEncoder(conn)

	for {
		if err := decoder.Decode(&pkt); err != nil {
			return fmt.Errorf("read wss: %w", err)
		}

		switch pkt.Type {
		case "url_verification":
			var challenge struct {
				Challenge string `json:"challenge"`
				Token     string `json:"token"`
			}
			json.Unmarshal(pkt.Data, &challenge)

			resp := map[string]string{"challenge": challenge.Challenge}
			respData, _ := json.Marshal(resp)
			pkt.Type = "event"
			pkt.Data = respData
			encoder.Encode(resp)

		case "event":
			a.handleWSEvent(ctx, pkt.Data)

		case "ping":
			pong := wsPacket{Type: "pong"}
			encoder.Encode(pong)
		}
	}
}

func (a *adapter) handleWSEvent(ctx context.Context, raw json.RawMessage) {
	var evt feishuEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		return
	}

	// 消息去重
	if a.seen[evt.Header.EventID] {
		return
	}
	a.seen[evt.Header.EventID] = true
	// 清理旧的去重记录
	if len(a.seen) > 10000 {
		a.seen = make(map[string]bool)
	}

	switch evt.Header.EventType {
	case "im.message.receive_v1":
		var msg feishuMsgEvent
		if err := json.Unmarshal(evt.Event, &msg); err != nil {
			return
		}
		a.handleMessage(msg)
	}
}

func (a *adapter) handleCardAction(raw []byte) bool {
	var payload struct {
		Header feishuHeader `json:"header"`
		Event  struct {
			Operator struct {
				OperatorID struct {
					UserID  string `json:"user_id"`
					OpenID  string `json:"open_id"`
					UnionID string `json:"union_id"`
				} `json:"operator_id"`
			} `json:"operator"`
			Context struct {
				OpenMessageID string `json:"open_message_id"`
				OpenChatID    string `json:"open_chat_id"`
			} `json:"context"`
			Action struct {
				Value map[string]string `json:"value"`
			} `json:"action"`
		} `json:"event"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	command := payload.Event.Action.Value["command"]
	if command == "" || payload.Event.Context.OpenChatID == "" {
		return false
	}
	userID := firstNonEmpty(payload.Event.Operator.OperatorID.UnionID, payload.Event.Operator.OperatorID.OpenID, payload.Event.Operator.OperatorID.UserID)
	ib := bot.InboundMessage{
		Platform:  bot.PlatformFeishu,
		ChatType:  bot.ChatGroup,
		ChatID:    payload.Event.Context.OpenChatID,
		UserID:    userID,
		UserName:  userID,
		Text:      command,
		MessageID: payload.Event.Context.OpenMessageID,
	}
	select {
	case a.msgCh <- ib:
	default:
		a.logger.Warn("feishu card action channel full")
	}
	return true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (a *adapter) handleMessage(msg feishuMsgEvent) {
	if msg.MsgType != "text" {
		return
	}

	// 解析文本内容
	var content textContent
	if err := json.Unmarshal([]byte(msg.Content), &content); err != nil {
		return
	}

	// @mention gating：仅在群聊中检查是否 @了 bot
	chatType := bot.ChatDM
	if msg.ChatType == "group" {
		chatType = bot.ChatGroup
		if a.cfg.RequireMention && len(msg.Mentions) == 0 {
			return
		}
	}

	ib := bot.InboundMessage{
		Platform:  bot.PlatformFeishu,
		ChatType:  chatType,
		ChatID:    msg.ChatID,
		UserID:    msg.Sender.SenderID.OpenID,
		UserName:  "",
		Text:      content.Text,
		MessageID: msg.MessageID,
	}

	// 获取用户信息填充用户名
	if msg.Sender.SenderID.OpenID != "" {
		ib.UserName = msg.Sender.SenderID.OpenID
	}

	select {
	case a.msgCh <- ib:
	default:
		a.logger.Warn("feishu message channel full")
	}
}

// sendMessage 使用飞书 REST API 回复消息。
func (a *adapter) sendMessage(ctx context.Context, msg bot.OutboundMessage) (bot.SendResult, error) {
	token, err := a.getTenantToken(ctx)
	if err != nil {
		return bot.SendResult{}, err
	}

	if msg.Card != nil {
		return a.sendCard(ctx, token, msg)
	}

	content, _ := json.Marshal(textContent{Text: msg.Text})
	payload := map[string]interface{}{
		"msg_type": "text",
		"content":  string(content),
	}

	url := feishuSendMsgURL
	if msg.ReplyToMsgID != "" {
		url = fmt.Sprintf(feishuReplyMsgURL, msg.ReplyToMsgID)
	} else {
		payload["receive_id"] = msg.ChatID
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return bot.SendResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return bot.SendResult{}, err
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	respBody, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(respBody, &result); err != nil {
		return bot.SendResult{}, err
	}
	if result.Code != 0 {
		return bot.SendResult{}, fmt.Errorf("feishu send error: %s", result.Msg)
	}

	return bot.SendResult{MessageID: result.Data.MessageID}, nil
}

// sendCard 发送 interactive card 消息（用于审批/问答）。
func (a *adapter) sendCard(ctx context.Context, token string, msg bot.OutboundMessage) (bot.SendResult, error) {
	card := msg.Card

	elements := make([]map[string]interface{}, 0)
	for _, el := range card.Elements {
		item := map[string]interface{}{"tag": el.Tag}
		if el.Content != "" {
			item["content"] = el.Content
		}
		if actions, ok := el.Extra["actions"]; ok && el.Tag == "action" {
			item["actions"] = actions
		} else {
			for k, v := range el.Extra {
				item[k] = v
			}
		}
		elements = append(elements, item)
	}

	cardPayload := map[string]interface{}{
		"header": map[string]interface{}{
			"title": map[string]string{
				"tag":     "plain_text",
				"content": card.Header,
			},
		},
		"elements": elements,
	}

	cardJSON, _ := json.Marshal(cardPayload)
	payload := map[string]interface{}{
		"msg_type": "interactive",
		"content":  string(cardJSON),
	}

	url := feishuSendMsgURL
	if msg.ReplyToMsgID != "" {
		url = fmt.Sprintf(feishuReplyMsgURL, msg.ReplyToMsgID)
	} else {
		payload["receive_id"] = msg.ChatID
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return bot.SendResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return bot.SendResult{}, err
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return bot.SendResult{}, err
	}
	if result.Code != 0 {
		return bot.SendResult{}, fmt.Errorf("feishu send card error: %s", result.Msg)
	}

	return bot.SendResult{MessageID: result.Data.MessageID}, nil
}

// runWebhook 启动飞书 Webhook 模式。
func (a *adapter) runWebhook(ctx context.Context) {
	port := a.cfg.WebhookPort
	if port == 0 {
		port = 8080
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/feishu/event", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
		if err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		var challenge struct {
			Challenge string `json:"challenge"`
			Token     string `json:"token"`
			Type      string `json:"type"`
		}
		_ = json.Unmarshal(body, &challenge)
		if challenge.Type == "url_verification" {
			if a.cfg.VerificationToken != "" && challenge.Token != a.cfg.VerificationToken {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": challenge.Challenge})
			return
		}

		var evt feishuEvent
		if err := json.Unmarshal(body, &evt); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if a.cfg.VerificationToken != "" && evt.Header.Token != "" && evt.Header.Token != a.cfg.VerificationToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if !a.handleCardAction(body) {
			raw, _ := json.Marshal(evt)
			a.handleWSEvent(ctx, raw)
		}
		w.WriteHeader(200)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	a.logger.Info("feishu webhook listening", "port", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		a.logger.Error("feishu webhook server error", "err", err)
	}
}
