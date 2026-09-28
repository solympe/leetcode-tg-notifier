package scheduler

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// cronScheduler runs one daily cron entry per chat at HH:MM in an IANA zone.
// Every job runs wrapped in cron.Recover, so a panic is logged, not fatal.
type cronScheduler struct {
	c       *cron.Cron
	mu      sync.Mutex // guards entries; never held while a job runs
	entries map[int64]cron.EntryID
}

// New returns a scheduler that is not started yet. Schedule, RunNow and Next
// work before Start.
func New() *cronScheduler {
	return &cronScheduler{
		c:       cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger))),
		entries: make(map[int64]cron.EntryID),
	}
}

// Schedule runs job every day at notifyTime ("HH:MM") in timezone, replacing
// the chat's previous entry. The spec is parsed first, so an invalid time or
// zone returns an error and keeps the previous entry.
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

// Start starts firing scheduled jobs. A second Start is a no-op.
func (cs *cronScheduler) Start() {
	cs.c.Start()
}

// Stop stops new firings and waits for the jobs cron has already started.
// It does not hold cs.mu while waiting, so a running job may call Remove.
// A RunNow in progress is not waited for: it runs in the caller's goroutine.
func (cs *cronScheduler) Stop() {
	<-cs.c.Stop().Done()
}

// RunNow runs the chat's scheduled job synchronously, exactly as cron would
// run it (Recover included), and reports whether the chat has one. cs.mu is
// released before the job runs, so a job that removes its own chat cannot
// deadlock. It also works after Stop. It is a test seam.
func (cs *cronScheduler) RunNow(chatID int64) bool {
	e, ok := cs.entry(chatID)
	if !ok {
		return false
	}
	e.WrappedJob.Run()
	return true
}

// Next returns the chat's next firing time, in time.Local, and whether the
// chat has a scheduled job. It works whether or not cron is running. It is a
// test seam.
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
