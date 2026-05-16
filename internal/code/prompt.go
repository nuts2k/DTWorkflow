package code

import (
	"fmt"
	"strings"
)

// PromptContext 构建 prompt 所需的上下文信息。
type PromptContext struct {
	Owner          string
	Repo           string
	Branch         string
	BaseRef        string
	DocPath        string
	MaxRetryRounds int
}

// BuildCodeFromDocPrompt 构建传入容器 stdin 的四段式 prompt。
func BuildCodeFromDocPrompt(ctx PromptContext) string {
	var sb strings.Builder

	// 第一段：上下文
	sb.WriteString(fmt.Sprintf(
		"You are working on repository %s/%s on branch %s.\n"+
			"Current code is based on ref: %s.\n\n"+
			"Your task is to implement features based on the following design document:\n"+
			"- Document path: %s\n\n"+
			"Please read the document first to understand requirements, architecture design, interface definitions, and constraints.\n\n",
		ctx.Owner, ctx.Repo, ctx.Branch, ctx.BaseRef, ctx.DocPath,
	))

	// 第二段：编码指令
	maxRetry := ctx.MaxRetryRounds
	if maxRetry <= 0 {
		maxRetry = 3
	}
	sb.WriteString(fmt.Sprintf(
		"Execution steps:\n"+
			"1. Read the design document thoroughly to understand the complete requirements\n"+
			"2. Read the existing project code structure to understand coding conventions and architecture patterns\n"+
			"3. If CLAUDE.md / .code-standards or similar convention files exist, follow their constraints\n"+
			"4. Implement code according to the design document, including:\n"+
			"   - Core functional code\n"+
			"   - Necessary unit tests\n"+
			"   - Integration tests if required by the document\n"+
			"5. Run tests to verify (mvn test / npm test / go test ./...). If tests fail, fix automatically, up to %d rounds\n"+
			"6. Commit after each logical unit is complete (commit message format: feat: {brief description})\n"+
			"7. After everything is done, run git push\n\n",
		maxRetry,
	))

	// 第三段：约束
	sb.WriteString(
		"Constraints:\n" +
			"- Strictly implement within the scope described in the design document, do NOT add features not described in the document\n" +
			"- Do NOT modify the design document itself\n" +
			"- Do NOT delete existing code and tests (unless the design document explicitly requires refactoring)\n" +
			"- Do NOT access external network APIs\n" +
			"- If the design document does not contain sufficient information to complete implementation, mark info_sufficient=false in output and list missing information\n\n",
	)

	// 第四段：输出格式
	sb.WriteString(
		"Output format (respond with ONLY this JSON, no other text):\n" +
			"```json\n" +
			"{\n" +
			"  \"success\": true/false,\n" +
			"  \"info_sufficient\": true/false,\n" +
			"  \"missing_info\": [\"...\"],\n" +
			"  \"branch_name\": \"the branch name you pushed to\",\n" +
			"  \"commit_sha\": \"final commit SHA after push\",\n" +
			"  \"modified_files\": [{\"path\": \"...\", \"action\": \"created/modified/deleted\", \"description\": \"...\"}],\n" +
			"  \"test_results\": {\"passed\": 0, \"failed\": 0, \"skipped\": 0, \"all_passed\": true/false},\n" +
			"  \"analysis\": \"brief analysis of the design document\",\n" +
			"  \"implementation\": \"brief summary of what was implemented\",\n" +
			"  \"failure_category\": \"none/info_insufficient/test_failure/infrastructure\",\n" +
			"  \"failure_reason\": \"reason if not successful\"\n" +
			"}\n" +
			"```\n",
	)

	return sb.String()
}
