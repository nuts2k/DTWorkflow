package validation

import (
	"fmt"
	"net/url"
	"strings"
)

// E2EModule 校验 E2E 任务的 module 字段。
func E2EModule(module string) error {
	return GenTestsModule(module)
}

// E2ECaseName 校验 E2E 任务的 case 名称。
func E2ECaseName(caseName string) error {
	if caseName == "" {
		return nil
	}
	if strings.Contains(caseName, "/") || strings.Contains(caseName, `\`) {
		return fmt.Errorf("不能包含路径分隔符: %q", caseName)
	}
	if caseName == ".." || caseName == "." {
		return fmt.Errorf("不能为相对路径引用: %q", caseName)
	}
	return nil
}

// E2EBaseURL 校验 base_url 覆盖值。
func E2EBaseURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("无效的 URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("仅支持 http/https 协议: %q", rawURL)
	}
	if u.Host == "" {
		return fmt.Errorf("缺少主机名: %q", rawURL)
	}
	return nil
}
