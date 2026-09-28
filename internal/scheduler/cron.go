package scheduler

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

type cronScheduler struct {
	c       *cron.Cron
	send    SendFunc
	mu      sync.Mutex
	entries map[int64]cron.EntryID
}

func NewCronScheduler(send SendFunc) *cronScheduler {
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

// Start starts firing scheduled jobs. NewCronScheduler has already started
// the scheduler and cron ignores a second Start, so calling it is always safe.
func (cs *cronScheduler) Start() {
	cs.c.Start()
}

// Stop stops new firings and waits for the jobs cron has already started.
// It does not hold cs.mu while waiting, so a running job may call Remove.
func (cs *cronScheduler) Stop() {
	<-cs.c.Stop().Done()
}

// RunNow runs the chat's scheduled job synchronously, exactly as cron would
// run it, and reports whether the chat has one. cs.mu is released before the
// job runs, so a job that removes its own chat cannot deadlock. It also works
// after Stop.
func (cs *cronScheduler) RunNow(chatID int64) bool {
	e, ok := cs.entry(chatID)
	if !ok {
		return false
	}
	e.WrappedJob.Run()
	return true
}

// Next returns the chat's next firing time, in time.Local, and whether the
// chat has a scheduled job. It works whether or not cron is running.
func (cs *cronScheduler) Next(chatID int64) (time.Time, bool) {
	e, ok := cs.entry(chatID)
	if !ok {
		return time.Time{}, false
	}
	return e.Schedule.Next(time.Now()), true
}

// entry returns a snapshot of the chat's cron entry. It holds cs.mu only to
// look up the entry ID.
func (cs *cronScheduler) entry(chatID int64) (cron.Entry, bool) {
	cs.mu.Lock()
	id, ok := cs.entries[chatID]
	cs.mu.Unlock()
	if !ok {
		return cron.Entry{}, false
	}
	e := cs.c.Entry(id)
	return e, e.Valid()
}
