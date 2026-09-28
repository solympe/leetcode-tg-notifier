package scheduler

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

// assertNext checks that the chat's next firing is at hhmm in zone, within 24h.
func assertNext(t *testing.T, s *cronScheduler, chatID int64, hhmm, zone string) {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	next, ok := s.Next(chatID)
	if !ok {
		t.Fatalf("Next(%d): nothing scheduled", chatID)
	}
	if got := next.In(loc).Format("15:04:05"); got != hhmm+":00" {
		t.Errorf("Next(%d) = %s in %s, want %s:00", chatID, got, zone, hhmm)
	}
	if !next.After(now) || next.Sub(now) > 24*time.Hour {
		t.Errorf("Next(%d) = %v, want within 24h after %v", chatID, next, now)
	}
}

// runNow calls RunNow and fails instead of hanging when it deadlocks.
func runNow(t *testing.T, s *cronScheduler, chatID int64) bool {
	t.Helper()
	done := make(chan bool, 1)
	go func() { done <- s.RunNow(chatID) }()
	select {
	case ok := <-done:
		return ok
	case <-time.After(2 * time.Second):
		t.Fatalf("RunNow(%d) did not return: deadlock", chatID)
		return false
	}
}

func TestSchedule(t *testing.T) {
	tests := []struct {
		name, notifyTime, timezone string
		wantErr                    string // "" = the new entry replaces the 10:00 UTC one
	}{
		{name: "09:30 Europe/Moscow replaces the entry", notifyTime: "09:30", timezone: "Europe/Moscow"},
		{name: "07:00 Asia/Dubai replaces the entry", notifyTime: "07:00", timezone: "Asia/Dubai"},
		{name: "23:59 UTC replaces the entry", notifyTime: "23:59", timezone: "UTC"},
		{name: "time without a colon keeps the previous entry", notifyTime: "0900", timezone: "UTC", wantErr: `invalid time "0900"`},
		{name: "12-hour time keeps the previous entry", notifyTime: "9am", timezone: "UTC", wantErr: `invalid time "9am"`},
		{name: "hour out of range keeps the previous entry", notifyTime: "25:00", timezone: "UTC", wantErr: "25"},
		{name: "unknown zone keeps the previous entry", notifyTime: "09:00", timezone: "Mars/Base", wantErr: "Mars/Base"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New() // never started: jobs run only through RunNow
			var runs []string
			if err := s.Schedule(100, "10:00", "UTC", func() { runs = append(runs, "old") }); err != nil {
				t.Fatalf("first Schedule: %v", err)
			}

			err := s.Schedule(100, tt.notifyTime, tt.timezone, func() { runs = append(runs, "new") })
			want, hhmm, zone := "new", tt.notifyTime, tt.timezone
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error: got %v, want containing %q", err, tt.wantErr)
				}
				want, hhmm, zone = "old", "10:00", "UTC"
			} else if err != nil {
				t.Fatalf("Schedule: %v", err)
			}

			if n := len(s.c.Entries()); n != 1 {
				t.Errorf("cron entries: got %d, want 1", n)
			}
			assertNext(t, s, 100, hhmm, zone)
			if !runNow(t, s, 100) || !slices.Equal(runs, []string{want}) {
				t.Errorf("runs: got %v, want [%s]", runs, want)
			}
		})
	}
}

func TestRunNow(t *testing.T) {
	tests := []struct {
		name        string
		chatID      int64                  // RunNow and Next are called for it
		before      func(s *cronScheduler) // runs once chat 100 is scheduled
		inJob       func(s *cronScheduler) // runs inside chat 100's job
		wantRun     bool                   // RunNow returns true and the job ran once
		wantNext    bool
		wantEntries int // entries left, in cron and in the map
	}{
		{name: "runs the scheduled job", chatID: 100, wantRun: true, wantNext: true, wantEntries: 1},
		{name: "a job that removes its own chat does not deadlock", chatID: 100, inJob: func(s *cronScheduler) { s.Remove(100) }, wantRun: true},
		{name: "a panicking job is recovered", chatID: 100, inJob: func(*cronScheduler) { panic("boom") }, wantRun: true, wantNext: true, wantEntries: 1},
		{name: "works while cron runs", chatID: 100, before: func(s *cronScheduler) { s.Start() }, wantRun: true, wantNext: true, wantEntries: 1},
		{name: "works after Stop", chatID: 100, before: func(s *cronScheduler) { s.Start(); s.Stop() }, wantRun: true, wantNext: true, wantEntries: 1},
		{name: "false after Remove", chatID: 100, before: func(s *cronScheduler) { s.Remove(100) }},
		{name: "false for an unknown chat", chatID: 200, wantEntries: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			t.Cleanup(s.Stop)
			var runs atomic.Int32
			if err := s.Schedule(100, "10:00", "UTC", func() {
				runs.Add(1)
				if tt.inJob != nil {
					tt.inJob(s)
				}
			}); err != nil {
				t.Fatalf("Schedule: %v", err)
			}
			if tt.before != nil {
				tt.before(s)
			}

			if ok := runNow(t, s, tt.chatID); ok != tt.wantRun || (runs.Load() == 1) != tt.wantRun {
				t.Errorf("RunNow(%d) = %v with %d runs, want %v", tt.chatID, ok, runs.Load(), tt.wantRun)
			}
			if _, ok := s.Next(tt.chatID); ok != tt.wantNext {
				t.Errorf("Next(%d) ok: got %v, want %v", tt.chatID, ok, tt.wantNext)
			}
			if n, m := len(s.c.Entries()), len(s.entries); n != tt.wantEntries || m != tt.wantEntries {
				t.Errorf("entries: %d in cron, %d in the map, want %d", n, m, tt.wantEntries)
			}
		})
	}
}

// fireOnce is a cron.Schedule that fires once, 10ms after cron first asks.
type fireOnce struct{ asked atomic.Bool }

func (f *fireOnce) Next(t time.Time) time.Time {
	if f.asked.Swap(true) {
		return time.Time{} // never again
	}
	return t.Add(10 * time.Millisecond)
}

func TestStop(t *testing.T) {
	tests := []struct {
		name     string
		remove   bool // the running job removes its own chat once released
		wantNext bool // chat 100 is still scheduled after Stop
	}{
		{name: "waits for a running job", wantNext: true},
		{name: "a running job may call Remove", remove: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			started, release := make(chan struct{}), make(chan struct{})
			// White-box: Schedule only takes daily times, so the entry is
			// added to the cron directly.
			s.entries[100] = s.c.Schedule(&fireOnce{}, cron.FuncJob(func() {
				close(started)
				<-release
				if tt.remove {
					s.Remove(100)
				}
			}))
			s.Start()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("cron did not start the job")
			}

			stopped := make(chan struct{})
			go func() { s.Stop(); close(stopped) }()
			select {
			case <-stopped:
				t.Fatal("Stop returned while a job was running")
			case <-time.After(50 * time.Millisecond):
			}
			close(release)
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("Stop did not return")
			}
			if _, ok := s.Next(100); ok != tt.wantNext {
				t.Errorf("Next(100) ok: got %v, want %v", ok, tt.wantNext)
			}
		})
	}
}
