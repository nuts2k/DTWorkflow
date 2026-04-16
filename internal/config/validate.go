package config

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/robfig/cron/v3"
)

var validClaudeEfforts = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
}

// 飞书渠道配置选项 key
const (
	FeishuOptionWebhookURL = "webhook_url"
	FeishuOptionSecret     = "secret"
)

// validNotifyChannels 合法通知渠道白名单
var validNotifyChannels = map[string]bool{
	"gitea":  true,
	"feishu": true,
}

func routeConfigsReferenceChannel(routes []RouteConfig, channel string) bool {
	for _, route := range routes {
		for _, chName := range route.Channels {
			if chName == channel {
				return true
			}
		}
	}
	return false
}

func usesGlobalFeishuChannel(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Notify.DefaultChannel == "feishu" || routeConfigsReferenceChannel(cfg.Notify.Routes, "feishu")
}

// Validate 校验配置的完整性与合法性。
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("配置不能为空")
	}

	var errs []error

	// server.port 范围
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("server.port 必须在 1-65535 范围内，当前值: %d", cfg.Server.Port))
	}

	// redis 必填
	if strings.TrimSpace(cfg.Redis.Addr) == "" {
		errs = append(errs, fmt.Errorf("redis.addr 不能为空"))
	}

	// gitea 必填
	if strings.TrimSpace(cfg.Gitea.URL) == "" {
		errs = append(errs, fmt.Errorf("gitea.url 不能为空"))
	} else {
		// gitea.url 格式校验：需以 http:// 或 https:// 开头且包含 host
		u, parseErr := url.Parse(cfg.Gitea.URL)
		if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("gitea.url 格式不合法，需以 http:// 或 https:// 开头: %q", cfg.Gitea.URL))
		}
	}
	if strings.TrimSpace(cfg.Gitea.Token) == "" {
		errs = append(errs, fmt.Errorf("gitea.token 不能为空"))
	}

	// claude 必填
	if strings.TrimSpace(cfg.Claude.APIKey) == "" {
		errs = append(errs, fmt.Errorf("claude.api_key 不能为空"))
	}
	if err := validateClaudeEffort("claude.effort", cfg.Claude.Effort); err != nil {
		errs = append(errs, err)
	}
	if err := validateClaudeModel("claude.model", cfg.Claude.Model); err != nil {
		errs = append(errs, err)
	}

	// webhook 必填
	if strings.TrimSpace(cfg.Webhook.Secret) == "" {
		errs = append(errs, fmt.Errorf("webhook.secret 不能为空"))
	}

	// 范围校验
	if cfg.Worker.Concurrency < 1 {
		errs = append(errs, fmt.Errorf("worker.concurrency 必须 >= 1，当前值: %d", cfg.Worker.Concurrency))
	}
	if cfg.Worker.Timeout <= 0 {
		errs = append(errs, fmt.Errorf("worker.timeout 必须大于 0，当前值: %s", cfg.Worker.Timeout))
	}

	// worker.timeouts 各字段非负校验（零值表示使用默认值，允许），且不超过 24h
	const maxTimeout = 24 * time.Hour
	for _, tc := range []struct {
		name string
		val  time.Duration
	}{
		{"worker.timeouts.review_pr", cfg.Worker.Timeouts.ReviewPR},
		{"worker.timeouts.fix_issue", cfg.Worker.Timeouts.FixIssue},
		{"worker.timeouts.gen_tests", cfg.Worker.Timeouts.GenTests},
		{"worker.timeouts.analyze_issue", cfg.Worker.Timeouts.AnalyzeIssue},
	} {
		if tc.val < 0 {
			errs = append(errs, fmt.Errorf("%s 不能为负数，当前值: %s", tc.name, tc.val))
		} else if tc.val > maxTimeout {
			errs = append(errs, fmt.Errorf("%s 不能超过 %s，当前值: %s", tc.name, maxTimeout, tc.val))
		}
	}

	// worker.image_full 格式校验（非空时检查无空格）
	if cfg.Worker.ImageFull != "" {
		if strings.Contains(cfg.Worker.ImageFull, " ") {
			errs = append(errs, fmt.Errorf("worker.image_full 格式非法: %q", cfg.Worker.ImageFull))
		}
	}

	// worker.stream_monitor 校验（仅在 enabled 时校验 activity_timeout）
	if cfg.Worker.StreamMonitor.Enabled {
		if cfg.Worker.StreamMonitor.ActivityTimeout <= 0 {
			errs = append(errs, fmt.Errorf("worker.stream_monitor.activity_timeout 启用时必须大于 0，当前值: %s", cfg.Worker.StreamMonitor.ActivityTimeout))
		}
	}

	// log.level 白名单
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if cfg.Log.Level != "" && !validLevels[strings.ToLower(cfg.Log.Level)] {
		errs = append(errs, fmt.Errorf("log.level 不合法，可选值: debug/info/warn/error，当前值: %q", cfg.Log.Level))
	}

	// log.format 白名单
	validFormats := map[string]bool{"text": true, "json": true}
	if cfg.Log.Format != "" && !validFormats[strings.ToLower(cfg.Log.Format)] {
		errs = append(errs, fmt.Errorf("log.format 不合法，可选值: text/json，当前值: %q", cfg.Log.Format))
	}

	// notify.default_channel 必须存在、enabled，且当前版本仅支持白名单渠道。
	if strings.TrimSpace(cfg.Notify.DefaultChannel) == "" {
		errs = append(errs, fmt.Errorf("notify.default_channel 不能为空"))
	} else {
		ch, ok := cfg.Notify.Channels[cfg.Notify.DefaultChannel]
		if !ok || !ch.Enabled {
			errs = append(errs, fmt.Errorf("notify.default_channel %q 未配置或未启用", cfg.Notify.DefaultChannel))
		}
		if !validNotifyChannels[cfg.Notify.DefaultChannel] {
			errs = append(errs, fmt.Errorf("notify.default_channel 当前仅支持 %v，当前值: %q", validNotifyChannelNames(), cfg.Notify.DefaultChannel))
		}
	}

	// notify.routes 引用的渠道必须已配置，且当前版本仅支持白名单渠道。
	for i, route := range cfg.Notify.Routes {
		for _, chName := range route.Channels {
			if _, ok := cfg.Notify.Channels[chName]; !ok {
				errs = append(errs, fmt.Errorf("notify.routes[%d] 引用了未配置的渠道: %q", i, chName))
				continue
			}
			if !validNotifyChannels[chName] {
				errs = append(errs, fmt.Errorf("notify.routes[%d] 当前仅支持 %v 渠道，发现: %q", i, validNotifyChannelNames(), chName))
			}
		}
	}

	// repos[].name 格式：必须是 owner/repo 格式
	for i, repo := range cfg.Repos {
		if strings.TrimSpace(repo.Name) == "" {
			errs = append(errs, fmt.Errorf("repos[%d].name 不能为空", i))
		} else if !strings.Contains(repo.Name, "/") {
			errs = append(errs, fmt.Errorf("repos[%d].name 格式必须为 owner/repo，当前值: %q", i, repo.Name))
		}
	}

	// repos[].notify.routes 引用的渠道必须已配置，且当前版本仅支持白名单渠道。
	for i, repo := range cfg.Repos {
		if repo.Notify == nil {
			continue
		}
		for j, route := range repo.Notify.Routes {
			for _, chName := range route.Channels {
				if _, ok := cfg.Notify.Channels[chName]; !ok {
					errs = append(errs, fmt.Errorf("repos[%d].notify.routes[%d] 引用了未配置的渠道: %q", i, j, chName))
					continue
				}
				if !validNotifyChannels[chName] {
					errs = append(errs, fmt.Errorf("repos[%d].notify.routes[%d] 当前仅支持 %v 渠道，发现: %q", i, j, validNotifyChannelNames(), chName))
				}
			}
		}
	}

	feishuCfg, feishuConfigured := cfg.Notify.Channels["feishu"]
	globalFeishuEnabled := feishuConfigured && feishuCfg.Enabled

	// 仓库级飞书覆盖校验
	for _, repo := range cfg.Repos {
		if repo.Notify == nil || repo.Notify.Feishu == nil {
			continue
		}
		f := repo.Notify.Feishu

		// webhook_url 必填
		if strings.TrimSpace(f.WebhookURL) == "" {
			errs = append(errs, fmt.Errorf(
				"repos[%s].notify.feishu: webhook_url 不能为空", repo.Name))
			continue
		}

		// webhook_url 格式校验
		if u, err := url.Parse(f.WebhookURL); err != nil ||
			(u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf(
				"repos[%s].notify.feishu.webhook_url 格式无效", repo.Name))
		}

		// 全局飞书渠道必须已启用
		if !globalFeishuEnabled {
			errs = append(errs, fmt.Errorf(
				"repos[%s].notify.feishu: 全局飞书渠道未启用，仓库级覆盖无效", repo.Name))
		}
	}

	// 飞书渠道专属校验：
	// 1) 仅当全局 default_channel / routes 实际会使用全局飞书时，才强制全局 webhook_url 必填
	// 2) repo.notify.routes 若引用 feishu 且未配置 repo.notify.feishu，也必须提供全局 webhook_url
	if globalFeishuEnabled {
		webhookURL := strings.TrimSpace(feishuCfg.Options[FeishuOptionWebhookURL])
		if webhookURL == "" {
			if usesGlobalFeishuChannel(cfg) {
				errs = append(errs, fmt.Errorf("notify.channels.feishu 已启用且被全局 default_channel/routes 使用，但未配置 webhook_url"))
			}
			for i, repo := range cfg.Repos {
				if repo.Notify == nil || repo.Notify.Routes == nil || repo.Notify.Feishu != nil {
					continue
				}
				if routeConfigsReferenceChannel(repo.Notify.Routes, "feishu") {
					errs = append(errs, fmt.Errorf("repos[%d].notify.routes 引用了 feishu 渠道，但既未配置 repos[%d].notify.feishu，也未配置全局 notify.channels.feishu.webhook_url", i, i))
				}
			}
		} else if u, err := url.Parse(webhookURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("notify.channels.feishu.webhook_url 格式无效，需以 http:// 或 https:// 开头: %q", webhookURL))
		}
	}

	// review.dimensions 白名单校验
	validDimensions := map[string]bool{
		"security": true, "logic": true, "architecture": true, "style": true,
	}
	for _, dim := range cfg.Review.Dimensions {
		if !validDimensions[strings.ToLower(dim)] {
			errs = append(errs, fmt.Errorf("review.dimensions 包含无效维度 %q，有效值: security, logic, architecture, style", dim))
		}
	}

	// review.severity 白名单校验
	validSeverities := map[string]bool{
		"critical": true, "error": true, "warning": true, "info": true,
	}
	if cfg.Review.Severity != "" && !validSeverities[strings.ToLower(cfg.Review.Severity)] {
		errs = append(errs, fmt.Errorf("review.severity 值无效 %q，有效值: critical, error, warning, info", cfg.Review.Severity))
	}
	if err := validateClaudeEffort("review.effort", cfg.Review.Effort); err != nil {
		errs = append(errs, err)
	}
	if err := validateClaudeModel("review.model", cfg.Review.Model); err != nil {
		errs = append(errs, err)
	}

	// review.ignore_patterns 语法校验
	for i, pattern := range cfg.Review.IgnorePatterns {
		if !doublestar.ValidatePattern(pattern) {
			errs = append(errs, fmt.Errorf("review.ignore_patterns[%d] 语法不合法: %q", i, pattern))
		}
	}

	// repos[].review 校验
	for i, repo := range cfg.Repos {
		if repo.Review == nil {
			continue
		}
		for _, dim := range repo.Review.Dimensions {
			if !validDimensions[strings.ToLower(dim)] {
				errs = append(errs, fmt.Errorf("repos[%d].review.dimensions 包含无效维度 %q，有效值: security, logic, architecture, style", i, dim))
			}
		}
		if repo.Review.Severity != "" && !validSeverities[strings.ToLower(repo.Review.Severity)] {
			errs = append(errs, fmt.Errorf("repos[%d].review.severity 值无效 %q，有效值: critical, error, warning, info", i, repo.Review.Severity))
		}
		if err := validateClaudeEffort(fmt.Sprintf("repos[%d].review.effort", i), repo.Review.Effort); err != nil {
			errs = append(errs, err)
		}
		if err := validateClaudeModel(fmt.Sprintf("repos[%d].review.model", i), repo.Review.Model); err != nil {
			errs = append(errs, err)
		}
		for j, pattern := range repo.Review.IgnorePatterns {
			if !doublestar.ValidatePattern(pattern) {
				errs = append(errs, fmt.Errorf("repos[%d].review.ignore_patterns[%d] 语法不合法: %q", i, j, pattern))
			}
		}
	}

	// daily_report 校验（仅 enabled=true 时）
	if cfg.DailyReport.Enabled {
		if _, cronErr := cron.ParseStandard(cfg.DailyReport.Cron); cronErr != nil {
			errs = append(errs, fmt.Errorf("daily_report.cron 表达式无效: %w", cronErr))
		}
		if _, tzErr := time.LoadLocation(cfg.DailyReport.Timezone); tzErr != nil {
			errs = append(errs, fmt.Errorf("daily_report.timezone 无效: %w", tzErr))
		}
		if strings.TrimSpace(cfg.DailyReport.FeishuWebhook) == "" {
			errs = append(errs, fmt.Errorf("daily_report.feishu_webhook 启用每日报告时不能为空"))
		} else if u, urlErr := url.Parse(cfg.DailyReport.FeishuWebhook); urlErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("daily_report.feishu_webhook 格式无效: %q", cfg.DailyReport.FeishuWebhook))
		}
	}

	// API Token 校验
	identities := make(map[string]bool)
	for i, tc := range cfg.API.Tokens {
		if !strings.HasPrefix(tc.Token, "dtw_") {
			errs = append(errs, fmt.Errorf("api.tokens[%d].token 必须以 dtw_ 开头", i))
		} else if len(tc.Token) < 16 {
			errs = append(errs, fmt.Errorf("api.tokens[%d].token 长度不能少于 16 字符", i))
		}
		if strings.TrimSpace(tc.Identity) == "" {
			errs = append(errs, fmt.Errorf("api.tokens[%d].identity 不能为空", i))
		} else if identities[tc.Identity] {
			errs = append(errs, fmt.Errorf("api.tokens[%d].identity \"%s\" 重复", i, tc.Identity))
		} else {
			identities[tc.Identity] = true
		}
	}

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

func validateClaudeEffort(field, effort string) error {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return nil
	}
	if !validClaudeEfforts[strings.ToLower(effort)] {
		return fmt.Errorf("%s 值无效 %q，有效值: low, medium, high", field, effort)
	}
	return nil
}

var validModelPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func validateClaudeModel(field, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	if !validModelPattern.MatchString(model) {
		return fmt.Errorf("%s 模型名格式不合法 %q，仅允许字母、数字、点、连字符和下划线", field, model)
	}
	return nil
}

// validNotifyChannelNames 返回合法渠道名列表字符串，用于错误消息
func validNotifyChannelNames() string {
	names := make([]string, 0, len(validNotifyChannels))
	for k := range validNotifyChannels {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// ValidationError 配置校验聚合错误。
//
// 说明：用单个 error 返回多个校验失败，便于 CLI 统一展示。
type ValidationError struct {
	Errors []error
}

// Unwrap 返回内部聚合的所有错误，便于调用方编程处理。
func (e *ValidationError) Unwrap() []error {
	if e == nil {
		return nil
	}
	return e.Errors
}

// Is 支持 errors.Is(err, config.ErrInvalidConfig) 语义，
// 便于调用方区分校验错误与 I/O 错误。
func (e *ValidationError) Is(target error) bool {
	return target == ErrInvalidConfig
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Errors) == 0 {
		return "配置校验失败"
	}

	var b strings.Builder
	b.WriteString("配置校验失败:")
	for _, err := range e.Errors {
		if err == nil {
			continue
		}
		b.WriteString("\n- ")
		b.WriteString(err.Error())
	}
	return b.String()
}
