package scheduler

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

// recorder is a SendFunc that records the chats it ran for and then calls
// then, when set.
type recorder struct {
	mu    sync.Mutex
	chats []int64
	then  func(chatID int64)
}

func (r *recorder) send(chatID int64) {
	r.mu.Lock()
	r.chats = append(r.chats, chatID)
	r.mu.Unlock()
	if r.then != nil {
		r.then(chatID)
	}
}

func (r *recorder) sent() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.chats)
}

func chatAt(chatID int64, notifyTime, timezone string) storage.ChatConfig {
	return storage.ChatConfig{ChatID: chatID, NotifyTime: notifyTime, Timezone: timezone}
}

func TestNext(t *testing.T) {
	tests := []struct {
		name       string
		notifyTime string
		timezone   string
		lifecycle  func(s *cronScheduler)
	}{
		{name: "Europe/Moscow", notifyTime: "09:30", timezone: "Europe/Moscow", lifecycle: func(*cronScheduler) {}},
		{name: "Asia/Dubai", notifyTime: "07:00", timezone: "Asia/Dubai", lifecycle: func(*cronScheduler) {}},
		{name: "UTC", notifyTime: "00:00", timezone: "UTC", lifecycle: func(*cronScheduler) {}},
		{name: "second Start is a no-op", notifyTime: "09:30", timezone: "Europe/Moscow", lifecycle: func(s *cronScheduler) { s.Start() }},
		{name: "after Stop", notifyTime: "07:00", timezone: "Asia/Dubai", lifecycle: func(s *cronScheduler) { s.Stop() }},
		{name: "after Stop and Start", notifyTime: "21:15", timezone: "UTC", lifecycle: func(s *cronScheduler) { s.Stop(); s.Start() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.timezone)
			if err != nil {
				t.Fatalf("LoadLocation: %v", err)
			}
			s := NewCronScheduler((&recorder{}).send)
			t.Cleanup(s.Stop)
			if err := s.Schedule(100, chatAt(100, tt.notifyTime, tt.timezone)); err != nil {
				t.Fatalf("Schedule: %v", err)
			}
			tt.lifecycle(s)

			before := time.Now()
			got, ok := s.Next(100)
			if !ok {
				t.Fatal("Next: got false, want true")
			}
			if hm := got.In(loc).Format("15:04"); hm != tt.notifyTime {
				t.Errorf("Next in %s: got %s, want %s", tt.timezone, hm, tt.notifyTime)
			}
			if d := got.Sub(before); d <= 0 || d > 24*time.Hour {
				t.Errorf("Next is %v from now, want within (0, 24h]", d)
			}
		})
	}
}

func TestRunNow(t *testing.T) {
	// Twelve and thirteen hours from now, so cron itself never fires these
	// entries while the test runs.
	idle := time.Now().UTC().Add(12 * time.Hour).Format("15:04")
	later := time.Now().UTC().Add(13 * time.Hour).Format("15:04")

	tests := []struct {
		name        string
		setup       func(s *cronScheduler) error
		then        func(s *cronScheduler, chatID int64) // runs inside the job
		wantRun     bool
		wantSent    []int64
		wantNext    bool
		wantEntries int
	}{
		{
			name:        "scheduled chat runs its job synchronously",
			setup:       func(s *cronScheduler) error { return s.Schedule(100, chatAt(100, idle, "UTC")) },
			wantRun:     true,
			wantSent:    []int64{100},
			wantNext:    true,
			wantEntries: 1,
		},
		{
			name: "only the chat's own job runs",
			setup: func(s *cronScheduler) error {
				if err := s.Schedule(200, chatAt(200, idle, "UTC")); err != nil {
					return err
				}
				return s.Schedule(100, chatAt(100, idle, "UTC"))
			},
			wantRun:     true,
			wantSent:    []int64{100},
			wantNext:    true,
			wantEntries: 2,
		},
		{
			name: "rescheduling leaves one entry",
			setup: func(s *cronScheduler) error {
				if err := s.Schedule(100, chatAt(100, idle, "UTC")); err != nil {
					return err
				}
				return s.Schedule(100, chatAt(100, later, "UTC"))
			},
			wantRun:     true,
			wantSent:    []int64{100},
			wantNext:    true,
			wantEntries: 1,
		},
		{
			name:  "unknown chat",
			setup: func(*cronScheduler) error { return nil },
		},
		{
			name: "removed chat",
			setup: func(s *cronScheduler) error {
				if err := s.Schedule(100, chatAt(100, idle, "UTC")); err != nil {
					return err
				}
				s.Remove(100)
				return nil
			},
		},
		{
			name: "runs after Stop",
			setup: func(s *cronScheduler) error {
				if err := s.Schedule(100, chatAt(100, idle, "UTC")); err != nil {
					return err
				}
				s.Stop()
				return nil
			},
			wantRun:     true,
			wantSent:    []int64{100},
			wantNext:    true,
			wantEntries: 1,
		},
		{
			// A blocked chat unsubscribes from inside its own job.
			name:     "a job that removes its own chat does not deadlock",
			setup:    func(s *cronScheduler) error { return s.Schedule(100, chatAt(100, idle, "UTC")) },
			then:     func(s *cronScheduler, chatID int64) { s.Remove(chatID) },
			wantRun:  true,
			wantSent: []int64{100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			var s *cronScheduler
			if tt.then != nil {
				rec.then = func(chatID int64) { tt.then(s, chatID) }
			}
			s = NewCronScheduler(rec.send)
			t.Cleanup(s.Stop)
			if err := tt.setup(s); err != nil {
				t.Fatalf("setup: %v", err)
			}

			done := make(chan bool, 1)
			go func() { done <- s.RunNow(100) }()
			select {
			case got := <-done:
				if got != tt.wantRun {
					t.Errorf("RunNow: got %v, want %v", got, tt.wantRun)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("RunNow did not return: deadlock")
			}
			if got := rec.sent(); !slices.Equal(got, tt.wantSent) {
				t.Errorf("sent: got %v, want %v", got, tt.wantSent)
			}
			if _, ok := s.Next(100); ok != tt.wantNext {
				t.Errorf("Next: got %v, want %v", ok, tt.wantNext)
			}
			if got := len(s.c.Entries()); got != tt.wantEntries {
				t.Errorf("cron entries: got %d, want %d", got, tt.wantEntries)
			}
		})
	}
}

func TestStop(t *testing.T) {
	tests := []struct {
		name     string
		job      func(s *cronScheduler) // runs once the job is released
		wantNext bool
	}{
		{name: "waits for a running job", job: func(*cronScheduler) {}, wantNext: true},
		{name: "a running job may call Remove", job: func(s *cronScheduler) { s.Remove(100) }, wantNext: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewCronScheduler((&recorder{}).send)
			t.Cleanup(s.Stop)
			started, release := make(chan struct{}), make(chan struct{})
			releaseJob := sync.OnceFunc(func() { close(release) })
			t.Cleanup(releaseJob) // runs before s.Stop, so a failed test never hangs

			// A one-second schedule makes cron itself start the job; the
			// daily specs Schedule builds never fire within a test.
			var runs atomic.Int32
			id := s.c.Schedule(cron.Every(time.Second), cron.FuncJob(func() {
				if runs.Add(1) > 1 {
					return
				}
				close(started)
				<-release
				tt.job(s)
			}))
			s.mu.Lock()
			s.entries[100] = id
			s.mu.Unlock()

			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("cron did not start the job")
			}
			stopped := make(chan struct{})
			go func() {
				s.Stop()
				close(stopped)
			}()
			select {
			case <-stopped:
				t.Fatal("Stop returned while the job was running")
			case <-time.After(100 * time.Millisecond):
			}
			releaseJob()
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("Stop did not return after the job finished")
			}
			if _, ok := s.Next(100); ok != tt.wantNext {
				t.Errorf("Next: got %v, want %v", ok, tt.wantNext)
			}
		})
	}
}
