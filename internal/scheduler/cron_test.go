package scheduler

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

// recorder counts the runs of named jobs.
type recorder struct {
	mu   sync.Mutex
	runs []string
}

func (r *recorder) job(name string) func() {
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.runs = append(r.runs, name)
	}
}

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.runs...)
}

// assertNext checks that the chat's next firing is at hh:mm in zone, within 24h.
func assertNext(t *testing.T, s *cronScheduler, chatID int64, hh, mm int, zone string) {
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
	if in := next.In(loc); in.Hour() != hh || in.Minute() != mm || in.Second() != 0 {
		t.Errorf("Next(%d) = %v, want %02d:%02d:00 in %s", chatID, in, hh, mm, zone)
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

func TestNext(t *testing.T) {
	tests := []struct {
		name       string
		notifyTime string
		timezone   string
		start      bool
		wantHH     int
		wantMM     int
	}{
		{name: "09:30 Europe/Moscow before Start", notifyTime: "09:30", timezone: "Europe/Moscow", wantHH: 9, wantMM: 30},
		{name: "07:00 Asia/Dubai before Start", notifyTime: "07:00", timezone: "Asia/Dubai", wantHH: 7, wantMM: 0},
		{name: "23:59 UTC before Start", notifyTime: "23:59", timezone: "UTC", wantHH: 23, wantMM: 59},
		{name: "09:30 Europe/Moscow after Start", notifyTime: "09:30", timezone: "Europe/Moscow", start: true, wantHH: 9, wantMM: 30},
		{name: "07:00 Asia/Dubai after Start", notifyTime: "07:00", timezone: "Asia/Dubai", start: true, wantHH: 7, wantMM: 0},
		{name: "23:59 UTC after Start", notifyTime: "23:59", timezone: "UTC", start: true, wantHH: 23, wantMM: 59},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			if tt.start {
				s.Start()
				t.Cleanup(s.Stop)
			}
			if err := s.Schedule(100, tt.notifyTime, tt.timezone, func() {}); err != nil {
				t.Fatalf("Schedule: %v", err)
			}

			assertNext(t, s, 100, tt.wantHH, tt.wantMM, tt.timezone)
		})
	}
}

func TestSchedule(t *testing.T) {
	tests := []struct {
		name       string
		notifyTime string
		timezone   string
		wantErr    string // "" = no error
		wantRuns   []string
		wantHH     int
		wantMM     int
		wantZone   string
	}{
		{
			name:       "rescheduling replaces the entry",
			notifyTime: "21:15",
			timezone:   "Asia/Tbilisi",
			wantRuns:   []string{"new"},
			wantHH:     21,
			wantMM:     15,
			wantZone:   "Asia/Tbilisi",
		},
		{
			name:       "time without a colon keeps the previous entry",
			notifyTime: "0900",
			timezone:   "UTC",
			wantErr:    `invalid time "0900"`,
			wantRuns:   []string{"old"},
			wantHH:     10,
			wantMM:     0,
			wantZone:   "UTC",
		},
		{
			name:       "12-hour time keeps the previous entry",
			notifyTime: "9am",
			timezone:   "UTC",
			wantErr:    `invalid time "9am"`,
			wantRuns:   []string{"old"},
			wantHH:     10,
			wantMM:     0,
			wantZone:   "UTC",
		},
		{
			name:       "hour out of range keeps the previous entry",
			notifyTime: "25:00",
			timezone:   "UTC",
			wantErr:    "25",
			wantRuns:   []string{"old"},
			wantHH:     10,
			wantMM:     0,
			wantZone:   "UTC",
		},
		{
			name:       "unknown zone keeps the previous entry",
			notifyTime: "09:00",
			timezone:   "Mars/Base",
			wantErr:    "Mars/Base",
			wantRuns:   []string{"old"},
			wantHH:     10,
			wantMM:     0,
			wantZone:   "UTC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			var rec recorder
			if err := s.Schedule(100, "10:00", "UTC", rec.job("old")); err != nil {
				t.Fatalf("first Schedule: %v", err)
			}

			err := s.Schedule(100, tt.notifyTime, tt.timezone, rec.job("new"))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Schedule: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error: got %v, want containing %q", err, tt.wantErr)
			}

			if n := len(s.c.Entries()); n != 1 {
				t.Errorf("cron entries: got %d, want 1", n)
			}
			assertNext(t, s, 100, tt.wantHH, tt.wantMM, tt.wantZone)
			if !runNow(t, s, 100) {
				t.Fatal("RunNow: nothing scheduled")
			}
			if got := rec.got(); !slices.Equal(got, tt.wantRuns) {
				t.Errorf("runs: got %v, want %v", got, tt.wantRuns)
			}
		})
	}
}

func TestRunNow(t *testing.T) {
	tests := []struct {
		name          string
		schedule      bool // chat 100 gets job before the call
		remove        bool // chat 100 is removed before the call
		stop          bool // the scheduler is started and stopped before the call
		chatID        int64
		job           func(s *cronScheduler, ran *atomic.Int32) func()
		wantOK        bool
		wantRan       int32
		wantScheduled bool // chat 100 is still scheduled after the call
	}{
		{
			name:          "runs the scheduled job",
			schedule:      true,
			chatID:        100,
			job:           func(_ *cronScheduler, ran *atomic.Int32) func() { return func() { ran.Add(1) } },
			wantOK:        true,
			wantRan:       1,
			wantScheduled: true,
		},
		{
			name:     "a job that removes its own chat does not deadlock",
			schedule: true,
			chatID:   100,
			job: func(s *cronScheduler, ran *atomic.Int32) func() {
				return func() { ran.Add(1); s.Remove(100) }
			},
			wantOK:        true,
			wantRan:       1,
			wantScheduled: false,
		},
		{
			name:     "a panicking job is recovered",
			schedule: true,
			chatID:   100,
			job: func(_ *cronScheduler, ran *atomic.Int32) func() {
				return func() { ran.Add(1); panic("boom") }
			},
			wantOK:        true,
			wantRan:       1,
			wantScheduled: true,
		},
		{
			name:          "works after Stop",
			schedule:      true,
			stop:          true,
			chatID:        100,
			job:           func(_ *cronScheduler, ran *atomic.Int32) func() { return func() { ran.Add(1) } },
			wantOK:        true,
			wantRan:       1,
			wantScheduled: true,
		},
		{
			name:          "false after Remove",
			schedule:      true,
			remove:        true,
			chatID:        100,
			job:           func(_ *cronScheduler, ran *atomic.Int32) func() { return func() { ran.Add(1) } },
			wantOK:        false,
			wantRan:       0,
			wantScheduled: false,
		},
		{
			name:          "false for an unknown chat",
			schedule:      true,
			chatID:        200,
			job:           func(_ *cronScheduler, ran *atomic.Int32) func() { return func() { ran.Add(1) } },
			wantOK:        false,
			wantRan:       0,
			wantScheduled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			var ran atomic.Int32
			if tt.schedule {
				if err := s.Schedule(100, "10:00", "UTC", tt.job(s, &ran)); err != nil {
					t.Fatalf("Schedule: %v", err)
				}
			}
			if tt.remove {
				s.Remove(100)
			}
			if tt.stop {
				s.Start()
				s.Stop()
			}

			if ok := runNow(t, s, tt.chatID); ok != tt.wantOK {
				t.Errorf("RunNow(%d): got %v, want %v", tt.chatID, ok, tt.wantOK)
			}
			if n := ran.Load(); n != tt.wantRan {
				t.Errorf("job runs: got %d, want %d", n, tt.wantRan)
			}
			if _, ok := s.Next(100); ok != tt.wantScheduled {
				t.Errorf("Next(100) ok: got %v, want %v", ok, tt.wantScheduled)
			}
			if _, ok := s.Next(tt.chatID); ok && !tt.wantOK {
				t.Errorf("Next(%d) ok: got true for a chat RunNow did not find", tt.chatID)
			}
		})
	}
}

// fireOnce is a cron.Schedule that fires a single time, at at.
type fireOnce struct{ at time.Time }

func (f fireOnce) Next(t time.Time) time.Time {
	if t.Before(f.at) {
		return f.at
	}
	return time.Time{} // never again
}

func TestStop(t *testing.T) {
	tests := []struct {
		name     string
		running  bool // a firing started by cron is still running when Stop is called
		remove   bool // the running job removes its own chat once released
		wantNext bool // chat 100 is still scheduled after Stop
	}{
		{name: "waits for a running job", running: true, wantNext: true},
		{name: "a running job may call Remove", running: true, remove: true, wantNext: false},
		{name: "returns with no running job", running: false, wantNext: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			started := make(chan struct{})
			release := make(chan struct{})
			var finished atomic.Bool
			job := func() {}
			if tt.running {
				job = func() {
					close(started)
					<-release
					if tt.remove {
						s.Remove(100)
					}
					finished.Store(true)
				}
			}
			// White-box: Schedule only takes daily times, so the entry is
			// added to the cron directly to fire once, in 10ms.
			id := s.c.Schedule(fireOnce{at: time.Now().Add(10 * time.Millisecond)}, cron.FuncJob(job))
			s.mu.Lock()
			s.entries[100] = id
			s.mu.Unlock()
			s.Start()
			if tt.running {
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					t.Fatal("cron did not start the job")
				}
			}

			stopped := make(chan struct{})
			go func() { s.Stop(); close(stopped) }()
			if tt.running {
				select {
				case <-stopped:
					t.Fatal("Stop returned while a job was running")
				case <-time.After(50 * time.Millisecond):
				}
				close(release)
			}
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("Stop did not return")
			}
			if finished.Load() != tt.running {
				t.Errorf("job finished before Stop returned: got %v, want %v", finished.Load(), tt.running)
			}
			if _, ok := s.Next(100); ok != tt.wantNext {
				t.Errorf("Next(100) ok: got %v, want %v", ok, tt.wantNext)
			}
		})
	}
}
