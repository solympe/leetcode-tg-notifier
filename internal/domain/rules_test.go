package domain

import (
	"reflect"
	"slices"
	"testing"
)

func TestWants(t *testing.T) {
	tests := []struct {
		name       string
		set        []string
		difficulty string
		want       bool
	}{
		{name: "nil set means any", set: nil, difficulty: Hard, want: true},
		{name: "empty set means any", set: []string{}, difficulty: Hard, want: true},
		{name: "subscribed difficulty", set: []string{Easy, Hard}, difficulty: Hard, want: true},
		{name: "case-insensitive match", set: []string{"hard"}, difficulty: Hard, want: true},
		{name: "not subscribed", set: []string{Easy, Medium}, difficulty: Hard, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Chat{Difficulties: tt.set}
			if got := c.Wants(tt.difficulty); got != tt.want {
				t.Errorf("Chat{Difficulties: %v}.Wants(%q) = %v, want %v", tt.set, tt.difficulty, got, tt.want)
			}
		})
	}
}

func TestPickFor(t *testing.T) {
	daily := Problem{
		Date:       "2026-09-28",
		ID:         "4",
		Title:      "Median of Two Sorted Arrays",
		Link:       "/problems/median-of-two-sorted-arrays/",
		Difficulty: Hard,
		Tags:       []string{"Array", "Binary Search", "Divide and Conquer"},
	}
	// easyPick is a random Easy problem that replaced a Hard daily of date.
	easyPick := func(date string) *Pick {
		return &Pick{
			Problem: Problem{
				Date:       date,
				ID:         "1",
				Title:      "Two Sum",
				Link:       "/problems/two-sum/",
				Difficulty: Easy,
				Tags:       []string{"Array", "Hash Table"},
			},
			DailyDifficulty: Hard,
		}
	}

	tests := []struct {
		name   string
		chat   Chat
		want   Pick
		wantOK bool
	}{
		{
			name: "no pick",
			chat: Chat{Difficulties: []string{Easy}},
		},
		{
			name: "pick for another date",
			chat: Chat{Difficulties: []string{Easy}, DailyPick: easyPick("2026-09-27")},
		},
		{
			name: "pick of a difficulty no longer subscribed",
			chat: Chat{Difficulties: []string{Medium}, DailyPick: easyPick("2026-09-28")},
		},
		{
			name:   "valid pick",
			chat:   Chat{Difficulties: []string{Easy}, DailyPick: easyPick("2026-09-28")},
			want:   *easyPick("2026-09-28"),
			wantOK: true,
		},
		{
			name:   "valid pick under a case-insensitive set",
			chat:   Chat{Difficulties: []string{"easy"}, DailyPick: easyPick("2026-09-28")},
			want:   *easyPick("2026-09-28"),
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.chat.PickFor(daily)
			if ok != tt.wantOK || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PickFor() = %#v, %v; want %#v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRecordSolve(t *testing.T) {
	const day = "2026-09-28"

	tests := []struct {
		name        string
		members     map[string]Member
		userID      int64
		userName    string
		wantTotal   int
		wantCounted bool
		wantMembers map[string]Member
	}{
		{
			name:        "first solve with nil members is counted",
			members:     nil,
			userID:      7,
			userName:    "Alice",
			wantTotal:   1,
			wantCounted: true,
			wantMembers: map[string]Member{"7": {Name: "Alice", Count: 1, LastSolvedDate: day}},
		},
		{
			name:        "same day is rejected with the total and keeps the name",
			members:     map[string]Member{"7": {Name: "Alice", Count: 3, LastSolvedDate: day}},
			userID:      7,
			userName:    "Alicia",
			wantTotal:   3,
			wantCounted: false,
			wantMembers: map[string]Member{"7": {Name: "Alice", Count: 3, LastSolvedDate: day}},
		},
		{
			name:        "next day is counted and refreshes the name",
			members:     map[string]Member{"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-27"}},
			userID:      7,
			userName:    "Alicia",
			wantTotal:   6,
			wantCounted: true,
			wantMembers: map[string]Member{"7": {Name: "Alicia", Count: 6, LastSolvedDate: day}},
		},
		{
			name:        "another member is keyed by decimal user ID and leaves others alone",
			members:     map[string]Member{"7": {Name: "Alice", Count: 2, LastSolvedDate: day}},
			userID:      5000000008,
			userName:    "@bob",
			wantTotal:   1,
			wantCounted: true,
			wantMembers: map[string]Member{
				"7":          {Name: "Alice", Count: 2, LastSolvedDate: day},
				"5000000008": {Name: "@bob", Count: 1, LastSolvedDate: day},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Chat{ChatID: 100, Members: tt.members}
			total, counted := c.RecordSolve(tt.userID, tt.userName, day)
			if total != tt.wantTotal || counted != tt.wantCounted {
				t.Errorf("RecordSolve() = %d, %v; want %d, %v", total, counted, tt.wantTotal, tt.wantCounted)
			}
			if !reflect.DeepEqual(c.Members, tt.wantMembers) {
				t.Errorf("Members:\n got %v\nwant %v", c.Members, tt.wantMembers)
			}
		})
	}
}

func TestStandings(t *testing.T) {
	tests := []struct {
		name    string
		members map[string]Member
		want    []Member
	}{
		{
			name:    "no members",
			members: nil,
			want:    nil,
		},
		{
			name: "count descending",
			members: map[string]Member{
				"1": {Name: "Alice", Count: 5},
				"2": {Name: "Bob", Count: 12},
				"3": {Name: "Charlie", Count: 3},
			},
			want: []Member{{Name: "Bob", Count: 12}, {Name: "Alice", Count: 5}, {Name: "Charlie", Count: 3}},
		},
		{
			name: "ties ordered by name",
			members: map[string]Member{
				"1": {Name: "Carol", Count: 2},
				"2": {Name: "Alice", Count: 2},
				"3": {Name: "Bob", Count: 3},
			},
			want: []Member{{Name: "Bob", Count: 3}, {Name: "Alice", Count: 2}, {Name: "Carol", Count: 2}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Chat{Members: tt.members}).Standings(); !slices.Equal(got, tt.want) {
				t.Errorf("Standings() = %v, want %v", got, tt.want)
			}
		})
	}
}
