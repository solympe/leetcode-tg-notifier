// Package domain holds the stdlib-only model, its rules and sentinel errors.
// The JSON tags are the config.json contract: renaming one needs a migration.
package domain

// Difficulty levels, spelled as LeetCode returns them.
const Easy, Medium, Hard = "Easy", "Medium", "Hard"

// Difficulties returns every difficulty in canonical order. The slice is
// freshly allocated on each call, so callers may modify it.
func Difficulties() []string {
	return []string{Easy, Medium, Hard}
}

type Problem struct {
	Date       string   `json:"date"` // "2006-01-02"; empty on a random draw until stamped
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Link       string   `json:"link"` // "/problems/<slug>/"
	Difficulty string   `json:"difficulty"`
	Tags       []string `json:"tags"`
}

// Pick is what a chat is sent. DailyDifficulty == "" means Problem is the official daily.
// Otherwise Problem is a random problem replacing a daily of that difficulty; a stored
// pick of the day always has it set.
type Pick struct {
	Problem
	DailyDifficulty string `json:"daily_difficulty"`
}

type Member struct {
	Name           string `json:"name"`
	Count          int    `json:"count"`
	LastSolvedDate string `json:"last_solved_date"` // "2006-01-02", UTC
}

type Chat struct {
	ChatID       int64             `json:"chat_id"`
	NotifyTime   string            `json:"notify_time"`            // "HH:MM"
	Timezone     string            `json:"timezone"`               // IANA
	Members      map[string]Member `json:"members"`                // key: user ID in decimal; nil is written as null
	Difficulties []string          `json:"difficulties,omitempty"` // nil or empty = any
	DailyPick    *Pick             `json:"daily_pick,omitempty"`
}
