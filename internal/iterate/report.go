package iterate

import (
	"fmt"
	"strings"
	"time"
)

// FixReportContext 修复报告生成上下文。
type FixReportContext struct {
	PRNumber    int64
	RoundNumber int
	Output      FixReviewOutput
	Repo        string
	Timestamp   time.Time
}

// GenerateFixReport 生成修复报告 markdown 内容。
func GenerateFixReport(ctx FixReportContext) string {
	var b strings.Builder

	ts := ctx.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	fmt.Fprintf(&b, "# PR #%d Round %d Fix Report\n\n", ctx.PRNumber, ctx.RoundNumber)
	fmt.Fprintf(&b, "- Repository: %s\n", ctx.Repo)
	fmt.Fprintf(&b, "- Date: %s\n", ts.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- Summary: %s\n\n", ctx.Output.Summary)

	modified := 0
	skipped := 0
	altChosen := 0
	for _, fix := range ctx.Output.Fixes {
		switch fix.Action {
		case "modified":
			modified++
		case "skipped":
			skipped++
		case "alternative_chosen":
			altChosen++
		}
	}
	fmt.Fprintf(&b, "## Statistics\n\n")
	fmt.Fprintf(&b, "| Action | Count |\n|--------|-------|\n")
	fmt.Fprintf(&b, "| modified | %d |\n", modified)
	fmt.Fprintf(&b, "| alternative_chosen | %d |\n", altChosen)
	fmt.Fprintf(&b, "| skipped | %d |\n\n", skipped)

	b.WriteString("## Details\n\n")
	for i, fix := range ctx.Output.Fixes {
		fmt.Fprintf(&b, "### %d. [%s] %s", i+1, fix.Severity, fix.File)
		if fix.Line > 0 {
			fmt.Fprintf(&b, ":%d", fix.Line)
		}
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "- **Action**: %s\n", fix.Action)
		fmt.Fprintf(&b, "- **What**: %s\n", fix.What)
		fmt.Fprintf(&b, "- **Why**: %s\n", fix.Why)

		if len(fix.Alternatives) > 0 {
			b.WriteString("\n**Alternatives Considered:**\n\n")
			for j, alt := range fix.Alternatives {
				fmt.Fprintf(&b, "%d. %s\n", j+1, alt.Description)
				fmt.Fprintf(&b, "   - Pros: %s\n", alt.Pros)
				fmt.Fprintf(&b, "   - Cons: %s\n", alt.Cons)
				fmt.Fprintf(&b, "   - Why not: %s\n", alt.WhyNot)
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

// BuildReportPath 构造修复报告在仓库中的路径。
func BuildReportPath(reportDir string, prNumber int64, roundNumber int) string {
	return BuildReportPathAt(reportDir, prNumber, roundNumber, time.Now())
}

// BuildReportPathAt 使用指定时间构造修复报告路径，便于确定性测试。
func BuildReportPathAt(reportDir string, prNumber int64, roundNumber int, t time.Time) string {
	date := t.Format("2006-01-02")
	return fmt.Sprintf("%s/%d-%s-round%d.md", reportDir, prNumber, date, roundNumber)
}
