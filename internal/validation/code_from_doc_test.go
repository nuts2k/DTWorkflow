package validation

import "testing"

func TestValidateBranchRef(t *testing.T) {
	valid := []string{
		"",
		"feat/auth",
		"auto-code/foo_1.2",
	}
	for _, branch := range valid {
		if err := ValidateBranchRef(branch); err != nil {
			t.Errorf("ValidateBranchRef(%q) 返回错误: %v", branch, err)
		}
	}

	invalid := []string{
		"foo);echo${IFS}hacked;#",
		"../x",
		"/x",
		"x..y",
		"x//y",
		"-bad",
		"foo/.bar",
		"x.lock",
		"x@{y",
	}
	for _, branch := range invalid {
		if err := ValidateBranchRef(branch); err == nil {
			t.Errorf("ValidateBranchRef(%q) 应返回错误", branch)
		}
	}
}

func TestValidateBaseRef(t *testing.T) {
	valid := []string{
		"",
		"main",
		"release/2026.05",
		"7f3a9c1",
	}
	for _, ref := range valid {
		if err := ValidateBaseRef(ref); err != nil {
			t.Errorf("ValidateBaseRef(%q) 返回错误: %v", ref, err)
		}
	}

	invalid := []string{
		"main:refs/heads/pwn",
		"main feature",
		"../main",
		"/main",
		"main.lock",
		"main@{1}",
	}
	for _, ref := range invalid {
		if err := ValidateBaseRef(ref); err == nil {
			t.Errorf("ValidateBaseRef(%q) 应返回错误", ref)
		}
	}
}
