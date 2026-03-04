package leetcode

import (
	"strings"
	"testing"
)

func TestFormatProblem_ValidDate(t *testing.T) {
	p := &Problem{
		Date:       "2026-03-03",
		Link:       "/problems/two-sum/",
		ID:         "1",
		Title:      "Two Sum",
		Difficulty: "Easy",
		Tags:       []string{"Array", "Hash Table"},
	}

	result := FormatProblem(p)

	if !strings.Contains(result, "March 3, 2026") {
		t.Errorf("expected 'March 3, 2026' in output, got: %s", result)
	}
	if !strings.Contains(result, "Two Sum") {
		t.Errorf("expected 'Two Sum' in output")
	}
	if !strings.Contains(result, "Easy") {
		t.Errorf("expected 'Easy' in output")
	}
	if !strings.Contains(result, "Array, Hash Table") {
		t.Errorf("expected tags in output, got: %s", result)
	}
}
