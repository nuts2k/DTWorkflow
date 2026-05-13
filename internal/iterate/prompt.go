package iterate

import (
	"fmt"
	"strings"

	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
)

// FixPromptContext prompt 构造所需的上下文。
type FixPromptContext struct {
	Repo          string
	PRNumber      int64
	HeadRef       string
	BaseRef       string
	Issues        []review.ReviewIssue
	PreviousFixes []FixSummary
	ReportPath    string
	RoundNumber   int
	MaxRounds     int
}

// BuildFixPrompt 构造 fix_review 的 Claude Code prompt。
func BuildFixPrompt(ctx FixPromptContext) string {
	var b strings.Builder

	fmt.Fprintf(&b, "You are fixing code review issues in PR #%d of repository %s.\n", ctx.PRNumber, ctx.Repo)
	fmt.Fprintf(&b, "The repository is cloned and branch '%s' is checked out (base: '%s').\n", ctx.HeadRef, ctx.BaseRef)
	fmt.Fprintf(&b, "This is iteration round %d of %d.\n\n", ctx.RoundNumber, ctx.MaxRounds)

	if len(ctx.PreviousFixes) > 0 {
		b.WriteString("## Previous Fix Rounds\n\n")
		for _, fix := range ctx.PreviousFixes {
			fmt.Fprintf(&b, "- Round %d: fixed %d issues. %s\n", fix.Round, fix.IssuesFixed, fix.Summary)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Issues to Fix\n\n")
	for i, issue := range ctx.Issues {
		fmt.Fprintf(&b, "### Issue %d [%s] %s\n", i+1, issue.Severity, issue.Category)
		fmt.Fprintf(&b, "- File: %s", issue.File)
		if issue.Line > 0 {
			fmt.Fprintf(&b, ":%d", issue.Line)
		}
		if issue.EndLine > 0 {
			fmt.Fprintf(&b, "-%d", issue.EndLine)
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "- Problem: %s\n", issue.Message)
		if issue.Suggestion != "" {
			fmt.Fprintf(&b, "- Suggestion: %s\n", issue.Suggestion)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Fix each issue listed above. For each issue, choose one of:\n")
	b.WriteString("   - **modified**: Apply the fix directly\n")
	b.WriteString("   - **alternative_chosen**: Choose a different approach (list all alternatives considered with pros/cons)\n")
	b.WriteString("   - **skipped**: Skip with clear justification\n")
	b.WriteString("2. When multiple reasonable approaches exist, you MUST list all alternatives with descriptions, pros, cons, and why each was not chosen.\n")
	fmt.Fprintf(&b, "3. Generate a fix report markdown file at: %s\n", ctx.ReportPath)
	b.WriteString("4. Commit ALL changes (code fixes + report file) in a SINGLE commit. Do NOT push; DTWorkflow will push the current PR branch after this task exits successfully.\n")
	b.WriteString("5. Do not run any push command. Push credentials are intentionally unavailable inside this task.\n")
	b.WriteString("6. The commit message should start with 'fix(review):' and summarize the fixes.\n\n")

	b.WriteString("## Output Format\n\n")
	b.WriteString("After completing fixes, output a JSON object with this schema:\n")
	b.WriteString("```json\n")
	b.WriteString(`{
  "fixes": [
    {
      "file": "path/to/file",
      "line": 10,
      "issue_ref": "Issue 1",
      "severity": "ERROR",
      "action": "modified",
      "what": "what was changed",
      "why": "reasoning",
      "alternatives": [
        {"description": "...", "pros": "...", "cons": "...", "why_not": "..."}
      ]
    }
  ],
  "summary": "Overall summary of changes made"
}`)
	b.WriteString("\n```\n")

	return b.String()
}
