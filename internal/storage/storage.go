package storage

type UserStat struct {
	Name           string `json:"name"`
	Count          int    `json:"count"`
	LastSolvedDate string `json:"last_solved_date"` // "2006-01-02" UTC
}

type ChatConfig struct {
	ChatID     int64               `json:"chat_id"`
	NotifyTime string              `json:"notify_time"` // "HH:MM"
	Timezone   string              `json:"timezone"`
	Members    map[string]UserStat `json:"members"`
}

type Storage interface {
	Get(chatID int64) (ChatConfig, bool)
	Set(cfg ChatConfig) error
	Delete(chatID int64) error
	All() []ChatConfig
}
