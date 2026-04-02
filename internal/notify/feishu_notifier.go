package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// FeishuOption 飞书通知器配置选项
type FeishuOption func(*FeishuNotifier)

// WithFeishuSecret 设置签名密钥
func WithFeishuSecret(secret string) FeishuOption {
	return func(n *FeishuNotifier) { n.secret = secret }
}

// WithFeishuLogger 设置日志记录器
func WithFeishuLogger(logger *slog.Logger) FeishuOption {
	return func(n *FeishuNotifier) {
		if logger != nil {
			n.logger = logger
		}
	}
}

// FeishuNotifier 飞书自定义机器人（Webhook）通知实现
type FeishuNotifier struct {
	webhookURL string
	secret     string
	httpClient *http.Client
	logger     *slog.Logger
}

type feishuWebhookResponse struct {
	Code          *int   `json:"code"`
	Msg           string `json:"msg"`
	StatusCode    *int   `json:"StatusCode"`
	StatusMessage string `json:"StatusMessage"`
}

// NewFeishuNotifier 创建飞书通知器
func NewFeishuNotifier(webhookURL string, opts ...FeishuOption) (*FeishuNotifier, error) {
	if webhookURL == "" {
		return nil, fmt.Errorf("飞书 webhookURL 不能为空")
	}
	n := &FeishuNotifier{
		webhookURL: webhookURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		opt(n)
	}
	return n, nil
}

// Name 返回渠道名称
func (n *FeishuNotifier) Name() string { return "feishu" }

// Send 发送飞书交互卡片消息
func (n *FeishuNotifier) Send(ctx context.Context, msg Message) error {
	cardMap, err := FormatFeishuCard(msg)
	if err != nil {
		return fmt.Errorf("格式化卡片失败 (%w): %w", ErrSendFailed, err)
	}

	body, err := n.marshalRequestBody(cardMap)
	if err != nil {
		return fmt.Errorf("构建请求体失败 (%w): %w", ErrSendFailed, err)
	}

	// 提前快速失败：如果 context 已取消则避免网络调用
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("发送通知前 context 已取消: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建请求失败 (%w): %w", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")

	n.logger.InfoContext(ctx, "发送飞书 Webhook 通知",
		"event", msg.EventType,
		"owner", msg.Target.Owner,
		"repo", msg.Target.Repo,
	)

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP 请求失败 (%w): %w", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode == http.StatusTooManyRequests {
		n.logger.Warn("飞书 Webhook 触发 rate limit (429)，请关注发送频率",
			"status", resp.StatusCode,
			"response", string(respBody),
		)
		return fmt.Errorf("rate limit (429): %w", ErrSendFailed)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d (%w), response: %s", resp.StatusCode, ErrSendFailed, string(respBody))
	}

	if len(respBody) == 0 {
		return nil
	}

	var apiResp feishuWebhookResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("解析飞书响应失败 (%w): %w", ErrSendFailed, err)
	}
	if code, ok := apiResp.code(); ok && code != 0 {
		msg := apiResp.message()
		if msg == "" {
			msg = "unknown error"
		}
		return fmt.Errorf("飞书 API 返回错误 code=%d, message=%s (%w)", code, msg, ErrSendFailed)
	}

	return nil
}

func (r feishuWebhookResponse) code() (int, bool) {
	switch {
	case r.Code != nil:
		return *r.Code, true
	case r.StatusCode != nil:
		return *r.StatusCode, true
	default:
		return 0, false
	}
}

func (r feishuWebhookResponse) message() string {
	if r.Msg != "" {
		return r.Msg
	}
	return r.StatusMessage
}

// marshalRequestBody 将卡片 map 序列化为 JSON。若配置了签名密钥，则追加 timestamp 和 sign 字段。
// 仅执行一次 json.Marshal，避免 Marshal→Unmarshal→Marshal 的三重序列化。
func (n *FeishuNotifier) marshalRequestBody(cardMap map[string]any) ([]byte, error) {
	if n.secret != "" {
		timestamp := time.Now().Unix()
		sign, err := genSign(n.secret, timestamp)
		if err != nil {
			return nil, err
		}
		cardMap["timestamp"] = fmt.Sprintf("%d", timestamp)
		cardMap["sign"] = sign
	}
	return json.Marshal(cardMap)
}

// genSign 按飞书自定义机器人签名算法生成签名。
// 算法：将 "timestamp\nsecret" 作为 HMAC-SHA256 的密钥，对空消息体签名，再 base64 编码。
// 参考：https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot
func genSign(secret string, timestamp int64) (string, error) {
	stringToSign := fmt.Sprintf("%d\n%s", timestamp, secret)
	h := hmac.New(sha256.New, []byte(stringToSign))
	_, err := h.Write([]byte{})
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}
