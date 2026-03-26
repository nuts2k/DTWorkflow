package review

import (
	"strings"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
)

func TestBuildDynamicInstructions(t *testing.T) {
	t.Run("全部四维度", func(t *testing.T) {
		dims := []string{"security", "logic", "architecture", "style"}
		result := buildDynamicInstructions(dims)

		// reviewPreamble 始终存在
		if !strings.Contains(result, "## Review Instructions") {
			t.Error("缺少 reviewPreamble 中的 ## Review Instructions")
		}
		if !strings.Contains(result, "严重程度定义") {
			t.Error("缺少 reviewPreamble 中的 严重程度定义")
		}
		// 四个维度均存在
		for _, dim := range []string{"安全 (security)", "逻辑 (logic)", "架构 (architecture)", "风格 (style)"} {
			if !strings.Contains(result, dim) {
				t.Errorf("缺少维度指令: %s", dim)
			}
		}
	})

	t.Run("仅 security 和 logic", func(t *testing.T) {
		dims := []string{"security", "logic"}
		result := buildDynamicInstructions(dims)

		// preamble 存在
		if !strings.Contains(result, "## Review Instructions") {
			t.Error("缺少 reviewPreamble")
		}
		// 启用的维度存在
		if !strings.Contains(result, "安全 (security)") {
			t.Error("缺少 security 维度指令")
		}
		if !strings.Contains(result, "逻辑 (logic)") {
			t.Error("缺少 logic 维度指令")
		}
		// 未启用的维度不存在
		if strings.Contains(result, "架构 (architecture)") {
			t.Error("不应包含 architecture 维度指令")
		}
		if strings.Contains(result, "风格 (style)") {
			t.Error("不应包含 style 维度指令")
		}
	})

	t.Run("空列表只有 preamble", func(t *testing.T) {
		result := buildDynamicInstructions([]string{})

		if !strings.Contains(result, "## Review Instructions") {
			t.Error("空维度时应包含 reviewPreamble")
		}
		// 不包含任何维度指令
		if strings.Contains(result, "安全 (security)") {
			t.Error("空维度时不应包含 security")
		}
		if strings.Contains(result, "逻辑 (logic)") {
			t.Error("空维度时不应包含 logic")
		}
	})

	t.Run("未知维度被忽略", func(t *testing.T) {
		dims := []string{"unknown_dim", "security"}
		result := buildDynamicInstructions(dims)

		// preamble 存在
		if !strings.Contains(result, "## Review Instructions") {
			t.Error("缺少 reviewPreamble")
		}
		// 已知维度存在
		if !strings.Contains(result, "安全 (security)") {
			t.Error("缺少 security 维度指令")
		}
		// 不崩溃即为通过
	})
}

func TestBuildPrompt_FileFiltering(t *testing.T) {
	svc := &Service{}

	pr := &gitea.PullRequest{
		Number: 42,
		Title:  "test PR",
		User:   &gitea.User{Login: "author"},
		Base: &gitea.PRBranch{
			Ref: "main",
			Repo: &gitea.Repository{
				FullName: "owner/repo",
			},
		},
	}

	files := []*gitea.ChangedFile{
		{Filename: "src/main.go", Additions: 10, Deletions: 2, Status: "modified"},
		{Filename: "docs/README.md", Additions: 5, Deletions: 0, Status: "added"},
		{Filename: "docs/guide.md", Additions: 3, Deletions: 1, Status: "modified"},
		{Filename: "internal/util.go", Additions: 20, Deletions: 5, Status: "modified"},
	}

	t.Run("IgnorePatterns 过滤 md 文件", func(t *testing.T) {
		cfg := ReviewConfig{
			Instructions:     defaultReviewInstructions,
			Dimensions:       []string{"security", "logic", "architecture", "style"},
			LargePRThreshold: 500,
			IgnorePatterns:   []string{"**/*.md", "docs/**"},
		}

		result := svc.buildPrompt(pr, files, cfg, 0)

		// 被忽略的文件不应出现在文件列表中
		if strings.Contains(result, "docs/README.md") {
			t.Error("docs/README.md 应被忽略，不应出现在 prompt 中")
		}
		if strings.Contains(result, "docs/guide.md") {
			t.Error("docs/guide.md 应被忽略，不应出现在 prompt 中")
		}

		// 未被忽略的文件应存在
		if !strings.Contains(result, "src/main.go") {
			t.Error("src/main.go 未被忽略，应出现在 prompt 中")
		}
		if !strings.Contains(result, "internal/util.go") {
			t.Error("internal/util.go 未被忽略，应出现在 prompt 中")
		}

		// 忽略提示文案应出现
		if !strings.Contains(result, "另有") || !strings.Contains(result, "个文件被配置忽略") {
			t.Error("应出现忽略提示文案")
		}
	})

	t.Run("无 IgnorePatterns 时无提示", func(t *testing.T) {
		cfg := ReviewConfig{
			Instructions:     defaultReviewInstructions,
			Dimensions:       []string{"security", "logic", "architecture", "style"},
			LargePRThreshold: 500,
		}

		result := svc.buildPrompt(pr, files, cfg, 0)

		// 所有文件均应出现
		if !strings.Contains(result, "src/main.go") {
			t.Error("src/main.go 应出现在 prompt 中")
		}
		if !strings.Contains(result, "docs/README.md") {
			t.Error("docs/README.md 应出现在 prompt 中")
		}

		// 不应出现忽略提示
		if strings.Contains(result, "个文件被配置忽略") {
			t.Error("无忽略时不应出现忽略提示文案")
		}
	})

	t.Run("全部文件被忽略", func(t *testing.T) {
		cfg := ReviewConfig{
			Instructions:     defaultReviewInstructions,
			Dimensions:       []string{"security"},
			LargePRThreshold: 500,
			IgnorePatterns:   []string{"**"},
		}

		result := svc.buildPrompt(pr, files, cfg, 0)

		// 所有文件被忽略，文件列表应为空
		if strings.Contains(result, "src/main.go") {
			t.Error("src/main.go 应被忽略")
		}
		// 忽略提示中应包含正确数量
		if !strings.Contains(result, "4 个文件被配置忽略") {
			t.Errorf("应提示 4 个文件被忽略，实际 prompt: %s", result[:min(200, len(result))])
		}
	})
}

func TestBuildPrompt_DynamicDimensions(t *testing.T) {
	svc := &Service{}

	pr := &gitea.PullRequest{
		Number: 1,
		Title:  "dim test PR",
		User:   &gitea.User{Login: "user"},
		Base: &gitea.PRBranch{
			Ref: "main",
			Repo: &gitea.Repository{
				FullName: "org/repo",
			},
		},
	}

	files := []*gitea.ChangedFile{
		{Filename: "main.go", Additions: 5, Deletions: 0, Status: "added"},
	}

	t.Run("默认 Instructions + 指定维度", func(t *testing.T) {
		cfg := ReviewConfig{
			Instructions:     defaultReviewInstructions,
			Dimensions:       []string{"security", "logic"},
			LargePRThreshold: 500,
		}

		result := svc.buildPrompt(pr, files, cfg, 0)

		// reviewPreamble 存在
		if !strings.Contains(result, "## Review Instructions") {
			t.Error("缺少 reviewPreamble")
		}

		// 启用的维度存在
		if !strings.Contains(result, "安全 (security)") {
			t.Error("缺少 security 维度指令")
		}
		if !strings.Contains(result, "逻辑 (logic)") {
			t.Error("缺少 logic 维度指令")
		}

		// 未启用的维度不存在
		if strings.Contains(result, "架构 (architecture)") {
			t.Error("不应包含 architecture 维度指令")
		}
		if strings.Contains(result, "风格 (style)") {
			t.Error("不应包含 style 维度指令")
		}
	})

	t.Run("空 Instructions（等价于默认）使用动态组装", func(t *testing.T) {
		cfg := ReviewConfig{
			Instructions:     "",
			Dimensions:       []string{"architecture"},
			LargePRThreshold: 500,
		}

		result := svc.buildPrompt(pr, files, cfg, 0)

		if !strings.Contains(result, "## Review Instructions") {
			t.Error("缺少 reviewPreamble")
		}
		if !strings.Contains(result, "架构 (architecture)") {
			t.Error("缺少 architecture 维度指令")
		}
		if strings.Contains(result, "安全 (security)") {
			t.Error("不应包含 security 维度指令")
		}
	})

	t.Run("自定义 Instructions 不做维度裁剪", func(t *testing.T) {
		customInstr := "## Custom Review\nDo custom review."
		cfg := ReviewConfig{
			Instructions:     customInstr,
			Dimensions:       []string{"security"}, // 有维度但不影响自定义指令
			LargePRThreshold: 500,
		}

		result := svc.buildPrompt(pr, files, cfg, 0)

		// 自定义指令原样出现
		if !strings.Contains(result, "## Custom Review") {
			t.Error("自定义 instructions 应原样出现在 prompt 中")
		}
		// 不应出现动态组装的指令
		if strings.Contains(result, "## Review Instructions") {
			t.Error("自定义 instructions 时不应出现默认 reviewPreamble")
		}
	})
}

