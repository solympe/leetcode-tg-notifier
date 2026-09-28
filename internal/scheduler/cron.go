package scheduler

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// cronScheduler keeps one daily cron entry per chat. Jobs run wrapped in
// cron.Recover, so a panic is logged, not fatal.
type cronScheduler struct {
	c       *cron.Cron
	mu      sync.Mutex // guards entries; never held while a job runs
	entries map[int64]cron.EntryID
}

// New returns a scheduler that fires nothing until Start.
func New() *cronScheduler {
	return &cronScheduler{
		c:       cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger))),
		entries: make(map[int64]cron.EntryID),
	}
}

// Schedule runs job daily at notifyTime ("HH:MM") in timezone, replacing the
// chat's entry. An invalid time or zone is an error and keeps the old entry.
func (cs *cronScheduler) Schedule(chatID int64, notifyTime, timezone string, job func()) error {
	hour, minute, ok := strings.Cut(notifyTime, ":")
	if !ok {
		return fmt.Errorf("invalid time %q", notifyTime)
	}
	spec, err := cron.ParseStandard(fmt.Sprintf("CRON_TZ=%s %s %s * * *", timezone, minute, hour))
	if err != nil {
		return fmt.Errorf("parse schedule: %w", err)
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()
	if old, ok := cs.entries[chatID]; ok {
		cs.c.Remove(old)
	}
	id := cs.c.Schedule(spec, cron.FuncJob(job))
	cs.entries[chatID] = id
	log.Printf("Scheduled chat %d at %s %s (entry %d)", chatID, notifyTime, timezone, id)
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

func (cs *cronScheduler) Start() { cs.c.Start() }

// Stop stops new firings and waits for running ones, but not for a RunNow.
func (cs *cronScheduler) Stop() { <-cs.c.Stop().Done() }

// RunNow runs the chat's job synchronously, as cron would (Recover included),
// and reports whether it has one. It is a test seam, like Next.
func (cs *cronScheduler) RunNow(chatID int64) bool {
	e, ok := cs.entry(chatID)
	if ok {
		e.WrappedJob.Run()
	}
	return ok
}

// Next returns the chat's next firing, in time.Local.
func (cs *cronScheduler) Next(chatID int64) (time.Time, bool) {
	e, ok := cs.entry(chatID)
	if !ok {
		return time.Time{}, false
	}
	return e.Schedule.Next(time.Now()), true
}

// entry holds cs.mu only for the ID lookup, so a job run by RunNow may call Remove.
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
