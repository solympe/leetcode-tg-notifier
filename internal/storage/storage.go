package storage

type UserStat struct {
	Name           string `json:"name"`
	Count          int    `json:"count"`
	LastSolvedDate string `json:"last_solved_date"` // "2006-01-02" UTC
}

// DailyPick is a random problem sent instead of the LeetCode daily when the
// daily's difficulty is not among the chat's subscribed difficulties.
type DailyPick struct {
	Date            string   `json:"date"`             // LeetCode daily date "2006-01-02" this pick replaces
	DailyDifficulty string   `json:"daily_difficulty"` // difficulty of that day's actual daily
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Link            string   `json:"link"`
	Difficulty      string   `json:"difficulty"`
	Tags            []string `json:"tags"`
}

type ChatConfig struct {
	ChatID       int64               `json:"chat_id"`
	NotifyTime   string              `json:"notify_time"` // "HH:MM"
	Timezone     string              `json:"timezone"`
	Members      map[string]UserStat `json:"members"`
	Difficulties []string            `json:"difficulties,omitempty"` // subscribed difficulties; empty = any
	DailyPick    *DailyPick          `json:"daily_pick,omitempty"`
}
