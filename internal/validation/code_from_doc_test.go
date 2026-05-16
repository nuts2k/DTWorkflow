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
