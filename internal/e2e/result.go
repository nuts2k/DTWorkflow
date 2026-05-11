package e2e

import "fmt"

type E2EOutput struct {
	Success      bool         `json:"success"`
	TotalCases   int          `json:"total_cases"`
	PassedCases  int          `json:"passed_cases"`
	FailedCases  int          `json:"failed_cases"`
	ErrorCases   int          `json:"error_cases"`
	SkippedCases int          `json:"skipped_cases"`
	Cases        []CaseResult `json:"cases"`
	Warnings     []string     `json:"warnings,omitempty"`
}

type CaseResult struct {
	Name            string       `json:"name"`
	Module          string       `json:"module"`
	CasePath        string       `json:"case_path"`
	Status          string       `json:"status"`
	DurationMs      int64        `json:"duration_ms"`
	SetupResult     *PhaseResult `json:"setup_result,omitempty"`
	TestResult      *PhaseResult `json:"test_result,omitempty"`
	TeardownResult  *PhaseResult `json:"teardown_result,omitempty"`
	FailureCategory string       `json:"failure_category,omitempty"`
	FailureAnalysis string       `json:"failure_analysis,omitempty"`
	Screenshots     []string      `json:"screenshots,omitempty"`
	Expectations    []Expectation `json:"expectations,omitempty"` // M5.2: case.yaml expectations 回填
}

type PhaseResult struct {
	Status     string         `json:"status"`
	DurationMs int64          `json:"duration_ms"`
	Scripts    []ScriptResult `json:"scripts"`
}

type ScriptResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output,omitempty"`
	ErrorMsg string `json:"error_msg,omitempty"`
}

type E2EResult struct {
	Output        *E2EOutput
	RawOutput     string
	DurationMs    int64
	Environment   string           // M5.2: 解析后的环境名
	CreatedIssues map[string]int64 // M5.2: case_path → issue_number
}

const maxOutputLen = 2048

func validateE2EOutput(o *E2EOutput) error {
	if o == nil {
		return fmt.Errorf("output 为 nil")
	}
	if o.Success && (o.FailedCases > 0 || o.ErrorCases > 0) {
		return fmt.Errorf("success=true 但 failed=%d error=%d", o.FailedCases, o.ErrorCases)
	}
	total := o.PassedCases + o.FailedCases + o.ErrorCases + o.SkippedCases
	if o.TotalCases != total {
		return fmt.Errorf("total_cases=%d != passed+failed+error+skipped=%d", o.TotalCases, total)
	}
	validStatus := map[string]bool{"passed": true, "failed": true, "error": true, "skipped": true}
	validCategory := map[string]bool{"bug": true, "script_outdated": true, "environment": true}
	for i, c := range o.Cases {
		if !validStatus[c.Status] {
			return fmt.Errorf("cases[%d].status=%q 非法", i, c.Status)
		}
		if c.FailureCategory != "" && !validCategory[c.FailureCategory] {
			return fmt.Errorf("cases[%d].failure_category=%q 非法", i, c.FailureCategory)
		}
		if c.Status == "failed" && c.FailureCategory == "" {
			return fmt.Errorf("cases[%d] status=failed 但 failure_category 为空", i)
		}
	}
	return nil
}

func sanitizeE2EOutput(o *E2EOutput) {
	if o == nil {
		return
	}
	for i := range o.Warnings {
		o.Warnings[i] = truncate(o.Warnings[i], maxOutputLen)
	}
	for i := range o.Cases {
		o.Cases[i].FailureAnalysis = truncate(o.Cases[i].FailureAnalysis, maxOutputLen)
		sanitizePhaseResult(o.Cases[i].SetupResult)
		sanitizePhaseResult(o.Cases[i].TestResult)
		sanitizePhaseResult(o.Cases[i].TeardownResult)
	}
}

func sanitizePhaseResult(p *PhaseResult) {
	if p == nil {
		return
	}
	for i := range p.Scripts {
		p.Scripts[i].Output = truncate(p.Scripts[i].Output, maxOutputLen)
		p.Scripts[i].ErrorMsg = truncate(p.Scripts[i].ErrorMsg, maxOutputLen)
	}
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}
