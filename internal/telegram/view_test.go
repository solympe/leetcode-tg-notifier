package telegram

import (
	"reflect"
	"slices"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// Valid dates are pinned by TestSender and the integration suite; these rows
// cover the raw-date fallback of both templates.
func TestFormatPick(t *testing.T) {
	tests := []struct {
		name string
		p    domain.Pick
		want string
	}{
		{
			name: "daily: unparseable date falls back to raw string",
			p: domain.Pick{Problem: domain.Problem{
				Date:       "someday",
				Link:       "/problems/lru-cache/",
				ID:         "146",
				Title:      "LRU Cache",
				Difficulty: "Medium",
				Tags:       []string{"Design"},
			}},
			want: "📅 LeetCode Daily — someday\n\n" +
				"🔢 146. LRU Cache\n💪 Difficulty: Medium\n🏷 Design\n\n" +
				"🔗 https://leetcode.com/problems/lru-cache/",
		},
		{
			name: "random: unparseable date falls back to raw string",
			p: domain.Pick{
				Problem: domain.Problem{
					Date:       "27.09.2026",
					Link:       "/problems/add-two-numbers/",
					ID:         "2",
					Title:      "Add Two Numbers",
					Difficulty: "Medium",
					Tags:       []string{"Linked List", "Math"},
				},
				DailyDifficulty: "Hard",
			},
			want: "🎲 LeetCode Random — 27.09.2026\n" +
				"Today's daily is Hard, so here's a random Medium problem for you.\n\n" +
				"🔢 2. Add Two Numbers\n💪 Difficulty: Medium\n🏷 Linked List, Math\n\n" +
				"🔗 https://leetcode.com/problems/add-two-numbers/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPick(tt.p); got != tt.want {
				t.Errorf("formatPick():\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestFormatRating(t *testing.T) {
	tests := []struct {
		name      string
		standings []domain.Member
		want      string
	}{
		{
			name: "4th place and later are numbered",
			standings: []domain.Member{
				{Name: "Bob", Count: 5}, {Name: "Alice", Count: 4}, {Name: "Carol", Count: 3},
				{Name: "Dave", Count: 2}, {Name: "@eve", Count: 1},
			},
			want: "🏆 <b>Rating</b>\n\n🥇 Bob — 5\n🥈 Alice — 4\n🥉 Carol — 3\n4. Dave — 2\n5. @eve — 1\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRating(tt.standings); got != tt.want {
				t.Errorf("formatRating():\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestKeyboards(t *testing.T) {
	type button struct{ label, data string }
	tests := []struct {
		name string
		kb   *tgbotapi.InlineKeyboardMarkup
		want [][]button
	}{
		{
			name: "timezone: DST zones are labelled by tzLabel",
			kb:   tzKeyboard(),
			want: [][]button{
				{{tzLabel("New York", "America/New_York"), "tz:America/New_York"}, {tzLabel("London", "Europe/London"), "tz:Europe/London"}},
				{{tzLabel("Lisbon", "Europe/Lisbon"), "tz:Europe/Lisbon"}, {"UTC+0", "tz:UTC"}},
				{{"UTC+3 Moscow", "tz:Europe/Moscow"}, {"UTC+4 Dubai", "tz:Asia/Dubai"}},
				{{"UTC+7 Bangkok", "tz:Asia/Bangkok"}},
			},
		},
		{
			name: "difficulty: canonical button order, unknown values ignored",
			kb:   difficultyKeyboard([]string{"Hard", "Extreme", "Easy"}),
			want: [][]button{
				{{"✅ Easy", "diff:Easy"}, {"⬜ Medium", "diff:Medium"}, {"✅ Hard", "diff:Hard"}},
				{{"💾 Save", "diffsave"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make([][]button, len(tt.kb.InlineKeyboard))
			for i, row := range tt.kb.InlineKeyboard {
				for _, b := range row {
					if b.CallbackData == nil {
						t.Fatalf("button %q has no callback data", b.Text)
					}
					got[i] = append(got[i], button{b.Text, *b.CallbackData})
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("keyboard:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestTzLabel(t *testing.T) {
	tests := []struct {
		name, city, zone, want string
	}{
		{name: "whole hours east", city: "Dubai", zone: "Asia/Dubai", want: "UTC+4 Dubai"},
		{name: "half-hour zone", city: "Kolkata", zone: "Asia/Kolkata", want: "UTC+5:30 Kolkata"},
		{name: "west of UTC", city: "Bogota", zone: "America/Bogota", want: "UTC-5 Bogota"},
		{name: "half-hour zone west of UTC", city: "Marquesas", zone: "Pacific/Marquesas", want: "UTC-9:30 Marquesas"},
		{name: "UTC", city: "UTC", zone: "UTC", want: "UTC+0 UTC"},
		{name: "unknown zone falls back to the city", city: "Mars", zone: "Mars/Base", want: "Mars"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tzLabel(tt.city, tt.zone); got != tt.want {
				t.Errorf("tzLabel(%q, %q) = %q, want %q", tt.city, tt.zone, got, tt.want)
			}
		})
	}
}

func TestValidTime(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"00:00", true},
		{"09:00", true},
		{"23:59", true},
		{"9:00", false},
		{"24:00", false},
		{"12:60", false},
		{"0900", false},
		{" 09:00", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := validTime(tt.in); got != tt.want {
				t.Errorf("validTime(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidTimezone(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"Europe/Moscow", true},
		{"America/New_York", true},
		{"UTC", true},
		{"", false},
		{"Local", false},
		{"Mars/Base", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := validTimezone(tt.in); got != tt.want {
				t.Errorf("validTimezone(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDifficultySets(t *testing.T) {
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{name: "canonical orders and drops unknown values", got: canonical([]string{"Hard", "Extreme", "Easy"}), want: []string{"Easy", "Hard"}},
		{name: "canonical of nil is empty", got: canonical(nil), want: nil},
		{name: "initial: nothing saved means all", got: initialDifficulties(nil), want: []string{"Easy", "Medium", "Hard"}},
		{name: "initial: the saved set, canonical", got: initialDifficulties([]string{"Hard", "Easy"}), want: []string{"Easy", "Hard"}},
		{name: "toggle adds in canonical order", got: toggle([]string{"Hard"}, "Easy"), want: []string{"Easy", "Hard"}},
		{name: "toggle adds to an empty set", got: toggle(nil, "Medium"), want: []string{"Medium"}},
		{name: "toggle removes", got: toggle([]string{"Easy", "Hard"}, "Hard"), want: []string{"Easy"}},
		{name: "toggle removes the last one", got: toggle([]string{"Easy"}, "Easy"), want: nil},
		{
			name: "toggle leaves its input alone",
			got: func() []string {
				in := []string{"Easy", "Hard"}
				toggle(in, "Easy")
				return in
			}(),
			want: []string{"Easy", "Hard"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Equal(tt.got, tt.want) {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
