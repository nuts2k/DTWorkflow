package config

import (
	"fmt"
	"strings"
)

// Validate 校验配置的完整性与合法性。
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("配置不能为空")
	}

	var errs []error

	// 必填项（本轮只覆盖任务要求的最小字段）
	if strings.TrimSpace(cfg.Webhook.Secret) == "" {
		errs = append(errs, fmt.Errorf("webhook.secret 不能为空"))
	}

	// 范围校验
	if cfg.Worker.Concurrency < 1 {
		errs = append(errs, fmt.Errorf("worker.concurrency 必须 >= 1，当前值: %d", cfg.Worker.Concurrency))
	}

	// notify.default_channel 必须存在且 enabled
	if strings.TrimSpace(cfg.Notify.DefaultChannel) == "" {
		errs = append(errs, fmt.Errorf("notify.default_channel 不能为空"))
	} else {
		ch, ok := cfg.Notify.Channels[cfg.Notify.DefaultChannel]
		if !ok || !ch.Enabled {
			errs = append(errs, fmt.Errorf("notify.default_channel %q 未配置或未启用", cfg.Notify.DefaultChannel))
		}
	}

	// notify.routes 引用的渠道必须已配置
	for i, route := range cfg.Notify.Routes {
		for _, chName := range route.Channels {
			if _, ok := cfg.Notify.Channels[chName]; !ok {
				errs = append(errs, fmt.Errorf("notify.routes[%d] 引用了未配置的渠道: %q", i, chName))
			}
		}
	}

	// repos[].notify.routes 引用的渠道必须已配置（与全局 routes 同口径：只校验存在性）。
	for i, repo := range cfg.Repos {
		if repo.Notify == nil {
			continue
		}
		for j, route := range repo.Notify.Routes {
			for _, chName := range route.Channels {
				if _, ok := cfg.Notify.Channels[chName]; !ok {
					errs = append(errs, fmt.Errorf("repos[%d].notify.routes[%d] 引用了未配置的渠道: %q", i, j, chName))
				}
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
