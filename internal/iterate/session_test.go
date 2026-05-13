package iterate

import "testing"

func TestFilterBySeverity(t *testing.T) {
	issues := []ReviewIssueSummary{
		{Severity: "CRITICAL"},
		{Severity: "ERROR"},
		{Severity: "WARNING"},
		{Severity: "INFO"},
	}

	// threshold=error → CRITICAL + ERROR
	filtered := FilterBySeverity(issues, "error")
	if len(filtered) != 2 {
		t.Errorf("error threshold: got %d, want 2", len(filtered))
	}

	// threshold=warning → CRITICAL + ERROR + WARNING
	filtered = FilterBySeverity(issues, "warning")
	if len(filtered) != 3 {
		t.Errorf("warning threshold: got %d, want 3", len(filtered))
	}

	// threshold=critical → CRITICAL only
	filtered = FilterBySeverity(issues, "critical")
	if len(filtered) != 1 {
		t.Errorf("critical threshold: got %d, want 1", len(filtered))
	}
}

func TestContainsLabel(t *testing.T) {
	labels := []string{"bug", "auto-iterate", "enhancement"}
	if !ContainsLabel(labels, "auto-iterate") {
		t.Error("should find auto-iterate")
	}
	if ContainsLabel(labels, "not-exist") {
		t.Error("should not find non-existent label")
	}
	if ContainsLabel(nil, "auto-iterate") {
		t.Error("nil labels should return false")
	}
}

func TestSeverityRank(t *testing.T) {
	if SeverityRank("CRITICAL") <= SeverityRank("ERROR") {
		t.Error("CRITICAL should rank higher than ERROR")
	}
	if SeverityRank("ERROR") <= SeverityRank("WARNING") {
		t.Error("ERROR should rank higher than WARNING")
	}
}
