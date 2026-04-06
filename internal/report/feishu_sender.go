package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
)

// CardSender 发送预格式化的飞书卡片
type CardSender interface {
	SendCard(ctx context.Context, cardMap map[string]any) error
}

// ReportFeishuSender 发送预格式化的飞书卡片（不经过 FormatFeishuCard / notify.Router）
type ReportFeishuSender struct {
	webhookURL string
	secret     string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewReportFeishuSender 创建日报飞书发送器
func NewReportFeishuSender(webhookURL, secret string) (*ReportFeishuSender, error) {
	if webhookURL == "" {
		return nil, fmt.Errorf("webhookURL 不能为空")
	}
	return &ReportFeishuSender{
		webhookURL: webhookURL,
		secret:     secret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     slog.Default(),
	}, nil
}

// SendCard 发送预格式化的卡片 JSON
func (s *ReportFeishuSender) SendCard(ctx context.Context, cardMap map[string]any) error {
	body, err := s.marshalWithSign(cardMap)
	if err != nil {
		return fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	s.logger.InfoContext(ctx, "发送每日报告飞书通知")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d, response: %s", resp.StatusCode, string(respBody))
	}

	// 检查飞书业务层错误码
	if len(respBody) > 0 {
		var apiResp struct {
			Code *int   `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(respBody, &apiResp); err == nil && apiResp.Code != nil && *apiResp.Code != 0 {
			return fmt.Errorf("飞书 API 返回错误 code=%d, msg=%s", *apiResp.Code, apiResp.Msg)
		}
	}

	return nil
}

// marshalWithSign 序列化卡片并添加签名（若配置了 secret）
func (s *ReportFeishuSender) marshalWithSign(cardMap map[string]any) ([]byte, error) {
	if s.secret != "" {
		m := make(map[string]any, len(cardMap)+2)
		for k, v := range cardMap {
			m[k] = v
		}
		timestamp := time.Now().Unix()
		sign, err := notify.GenSign(s.secret, timestamp)
		if err != nil {
			return nil, err
		}
		m["timestamp"] = fmt.Sprintf("%d", timestamp)
		m["sign"] = sign
		return json.Marshal(m)
	}
	return json.Marshal(cardMap)
}
