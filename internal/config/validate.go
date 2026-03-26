package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

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

	// notify.default_channel 必须存在、enabled，且当前版本仅支持 gitea。
	if strings.TrimSpace(cfg.Notify.DefaultChannel) == "" {
		errs = append(errs, fmt.Errorf("notify.default_channel 不能为空"))
	} else {
		ch, ok := cfg.Notify.Channels[cfg.Notify.DefaultChannel]
		if !ok || !ch.Enabled {
			errs = append(errs, fmt.Errorf("notify.default_channel %q 未配置或未启用", cfg.Notify.DefaultChannel))
		}
		if cfg.Notify.DefaultChannel != "gitea" {
			errs = append(errs, fmt.Errorf("notify.default_channel 当前仅支持 %q，当前值: %q", "gitea", cfg.Notify.DefaultChannel))
		}
	}

	// notify.routes 引用的渠道必须已配置，且当前版本仅支持 gitea。
	for i, route := range cfg.Notify.Routes {
		for _, chName := range route.Channels {
			if _, ok := cfg.Notify.Channels[chName]; !ok {
				errs = append(errs, fmt.Errorf("notify.routes[%d] 引用了未配置的渠道: %q", i, chName))
				continue
			}
			if chName != "gitea" {
				errs = append(errs, fmt.Errorf("notify.routes[%d] 当前仅支持 %q 渠道，发现: %q", i, "gitea", chName))
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

	// repos[].notify.routes 引用的渠道必须已配置，且当前版本仅支持 gitea。
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
				if chName != "gitea" {
					errs = append(errs, fmt.Errorf("repos[%d].notify.routes[%d] 当前仅支持 %q 渠道，发现: %q", i, j, "gitea", chName))
				}
			}
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
		for j, pattern := range repo.Review.IgnorePatterns {
			if !doublestar.ValidatePattern(pattern) {
				errs = append(errs, fmt.Errorf("repos[%d].review.ignore_patterns[%d] 语法不合法: %q", i, j, pattern))
			}
		}
	}

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
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
