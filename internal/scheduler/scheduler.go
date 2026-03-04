package scheduler

import "github.com/solympe/leetcode-tg-notifier/internal/storage"

type SendFunc func(chatID int64)

type Scheduler interface {
	Schedule(chatID int64, cfg storage.ChatConfig) error
	Remove(chatID int64)
}
