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

func TestFormatProblem(t *testing.T) {
	tests := []struct {
		name string
		p    *Problem
		want string
	}{
		{
			name: "valid date",
			p: &Problem{
				Date:       "2026-03-03",
				Link:       "/problems/two-sum/",
				ID:         "1",
				Title:      "Two Sum",
				Difficulty: "Easy",
				Tags:       []string{"Array", "Hash Table"},
			},
			want: "📅 LeetCode Daily — March 3, 2026\n\n" +
				"🔢 1. Two Sum\n💪 Difficulty: Easy\n🏷 Array, Hash Table\n\n" +
				"🔗 https://leetcode.com/problems/two-sum/",
		},
		{
			name: "unparseable date falls back to raw string",
			p: &Problem{
				Date:       "someday",
				Link:       "/problems/lru-cache/",
				ID:         "146",
				Title:      "LRU Cache",
				Difficulty: "Medium",
				Tags:       []string{"Design"},
			},
			want: "📅 LeetCode Daily — someday\n\n" +
				"🔢 146. LRU Cache\n💪 Difficulty: Medium\n🏷 Design\n\n" +
				"🔗 https://leetcode.com/problems/lru-cache/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatProblem(tt.p); got != tt.want {
				t.Errorf("FormatProblem():\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestFormatRandomProblem(t *testing.T) {
	tests := []struct {
		name            string
		p               *Problem
		dailyDifficulty string
		want            string
	}{
		{
			name: "valid date",
			p: &Problem{
				Date:       "2026-09-27",
				Link:       "/problems/trapping-rain-water/",
				ID:         "42",
				Title:      "Trapping Rain Water",
				Difficulty: "Hard",
				Tags:       []string{"Array", "Two Pointers"},
			},
			dailyDifficulty: "Easy",
			want: "🎲 LeetCode Random — September 27, 2026\n" +
				"Today's daily is Easy, so here's a random Hard problem for you.\n\n" +
				"🔢 42. Trapping Rain Water\n💪 Difficulty: Hard\n🏷 Array, Two Pointers\n\n" +
				"🔗 https://leetcode.com/problems/trapping-rain-water/",
		},
		{
			name: "unparseable date falls back to raw string",
			p: &Problem{
				Date:       "27.09.2026",
				Link:       "/problems/add-two-numbers/",
				ID:         "2",
				Title:      "Add Two Numbers",
				Difficulty: "Medium",
				Tags:       []string{"Linked List", "Math"},
			},
			dailyDifficulty: "Hard",
			want: "🎲 LeetCode Random — 27.09.2026\n" +
				"Today's daily is Hard, so here's a random Medium problem for you.\n\n" +
				"🔢 2. Add Two Numbers\n💪 Difficulty: Medium\n🏷 Linked List, Math\n\n" +
				"🔗 https://leetcode.com/problems/add-two-numbers/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatRandomProblem(tt.p, tt.dailyDifficulty); got != tt.want {
				t.Errorf("FormatRandomProblem():\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}
