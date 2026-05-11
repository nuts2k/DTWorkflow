package e2e

import (
	"fmt"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
)

type promptContext struct {
	RepoFullName string
	BaseRef      string
	BaseURL      string
	DB           *config.E2EDBConfig
	Accounts     map[string]config.E2EAccountConfig
	Module       string
	CaseName     string
	Model        string
	Effort       string
}

func buildE2EPrompt(ctx promptContext) string {
	var b strings.Builder

	fmt.Fprintf(&b, "你正在为仓库 %s 执行 E2E 测试。\n\n", ctx.RepoFullName)
	fmt.Fprintf(&b, "测试环境：\n")
	fmt.Fprintf(&b, "- 应用地址：%s\n", ctx.BaseURL)
	if ctx.DB != nil {
		fmt.Fprintf(&b, "- 数据库：通过 mysql --defaults-extra-file=/tmp/.my.cnf 访问\n")
	}
	if len(ctx.Accounts) > 0 {
		b.WriteString("- 测试账号：\n")
		for name, acc := range ctx.Accounts {
			fmt.Fprintf(&b, "  - %s: 用户名 %s（密码已写入 /tmp/.e2e-accounts.json）\n", name, acc.Username)
		}
	}
	fmt.Fprintf(&b, "\n代码基线：%s\n", ctx.BaseRef)

	switch {
	case ctx.CaseName != "" && ctx.Module != "":
		fmt.Fprintf(&b, "测试范围：e2e/%s/cases/%s/\n", ctx.Module, ctx.CaseName)
	case ctx.Module != "":
		fmt.Fprintf(&b, "测试范围：e2e/%s/cases/*/\n", ctx.Module)
	default:
		b.WriteString("测试范围：e2e/*/cases/*/（全部模块）\n")
	}

	b.WriteString("\n执行步骤：\n\n")
	b.WriteString("1. 进入 e2e/ 目录，根据测试范围扫描 case.yaml 文件\n")
	b.WriteString("2. 对每个用例，按以下阶段顺序执行：\n")
	b.WriteString("   a) Setup：按 case.yaml 中 setup 数组声明的顺序执行脚本\n")
	b.WriteString("      - .sql → mysql --defaults-extra-file=/tmp/.my.cnf < script\n")
	b.WriteString("      - .js → node script\n")
	b.WriteString("      - .ts（非 .spec.ts）→ npx tsx script\n")
	b.WriteString("      - setup 失败 → 跳过 test，标记 error，仍执行 teardown\n")
	b.WriteString("   b) Test：\n")
	b.WriteString("      - .spec.ts → npx playwright test script --reporter=json\n")
	b.WriteString("      - Playwright 截图输出到 /workspace/artifacts/test-results/{case_name}/\n")
	b.WriteString("      - 如果测试失败且没有自动截图，请主动执行 page.screenshot() 保存到上述目录\n")
	b.WriteString("      - 在结果 JSON 的 screenshots 字段中填写截图的完整容器内路径（以 /workspace/artifacts/ 开头）\n")
	b.WriteString("   c) Teardown：同 setup 执行方式，无论 test 结果都执行\n")
	b.WriteString("3. 汇总所有用例结果\n\n")
	b.WriteString("重要约束：\n")
	b.WriteString("- 用例之间不共享状态\n")
	b.WriteString("- 严格按 case.yaml 声明顺序执行脚本\n")
	b.WriteString("- 不要修改任何仓库文件\n")
	b.WriteString("- 不要读取凭证文件内容到输出中\n\n")

	b.WriteString("当测试失败时，执行以下分析步骤：\n\n")
	b.WriteString("1. 确定失败阶段：setup / test / teardown\n")
	b.WriteString("2. 对于 test 阶段失败，对比 case.yaml 中的 expectations 和实际行为：\n")
	b.WriteString("   - 应用行为与 expectations 描述的业务意图不符 → \"bug\"\n")
	b.WriteString("   - 页面元素找不到、选择器失效、DOM 结构变更 → \"script_outdated\"\n")
	b.WriteString("   - 服务不可达、数据库连接超时、网络错误 → \"environment\"\n")
	b.WriteString("3. 对于 setup/teardown 阶段失败：\n")
	b.WriteString("   - SQL 连接错误 → \"environment\"\n")
	b.WriteString("   - SQL 语法错误或脚本 bug → \"script_outdated\"\n\n")
	b.WriteString("failure_analysis 字段请详细描述：\n")
	b.WriteString("- 具体哪个操作/断言失败了\n")
	b.WriteString("- 实际观察到的行为是什么\n")
	b.WriteString("- 与 expectations 的哪条预期不符\n")
	b.WriteString("- 如果是 script_outdated，指出哪个选择器或交互方式需要更新\n\n")

	b.WriteString("每个用例的结果中，请将 case.yaml 中的 expectations 原样输出到 \"expectations\" 字段：\n")
	b.WriteString("\"expectations\": [{\"step\": \"...\", \"expect\": \"...\"}]\n")
	b.WriteString("如果 case.yaml 中没有 expectations 段，该字段输出为空数组。\n\n")

	b.WriteString("以 JSON 格式输出结果（不要在 JSON 前后输出其他内容）：\n")
	b.WriteString(e2eOutputSchema)
	b.WriteString("\n")

	return b.String()
}

const e2eOutputSchema = `{
  "success": bool,
  "total_cases": int,
  "passed_cases": int,
  "failed_cases": int,
  "error_cases": int,
  "skipped_cases": int,
  "cases": [{
    "name": "string",
    "module": "string",
    "case_path": "string",
    "status": "passed|failed|error|skipped",
    "duration_ms": int,
    "setup_result": {"status": "string", "duration_ms": int, "scripts": [{"name": "string", "status": "string", "exit_code": int, "output": "string", "error_msg": "string"}]},
    "test_result": {...},
    "teardown_result": {...},
    "failure_category": "bug|script_outdated|environment",
    "failure_analysis": "string",
    "screenshots": ["/workspace/artifacts/test-results/{case_name}/screenshot-1.png"],
    "expectations": [{"step": "string", "expect": "string"}]
  }],
  "warnings": ["string"]
}`
