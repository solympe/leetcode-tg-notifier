package scheduler

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

type cronScheduler struct {
	c       *cron.Cron
	send    SendFunc
	mu      sync.Mutex
	entries map[int64]cron.EntryID
}

func NewCronScheduler(send SendFunc) Scheduler {
	c := cron.New()
	c.Start()
	return &cronScheduler{
		c:       c,
		send:    send,
		entries: make(map[int64]cron.EntryID),
	}
}

func (cs *cronScheduler) Schedule(chatID int64, cfg storage.ChatConfig) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if id, ok := cs.entries[chatID]; ok {
		cs.c.Remove(id)
	}

	parts := strings.Split(cfg.NotifyTime, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid time %q", cfg.NotifyTime)
	}
	hour, minute := parts[0], parts[1]
	spec := fmt.Sprintf("CRON_TZ=%s %s %s * * *", cfg.Timezone, minute, hour)

	id, err := cs.c.AddFunc(spec, func() {
		cs.send(chatID)
	})
	if err != nil {
		return fmt.Errorf("AddFunc: %w", err)
	}
	cs.entries[chatID] = id
	log.Printf("Scheduled chat %d at %s %s (entry %d)", chatID, cfg.NotifyTime, cfg.Timezone, id)
	return nil
}

func (cs *cronScheduler) Remove(chatID int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if id, ok := cs.entries[chatID]; ok {
		cs.c.Remove(id)
		delete(cs.entries, chatID)
	}
}
