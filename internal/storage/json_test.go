package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPersistenceAcrossReload(t *testing.T) {
	f, err := os.CreateTemp("", "storage_test_*.json")
	if err != nil {
		t.Fatal(err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(name) })

	s1, err := NewJSONStorage(name)
	if err != nil {
		t.Fatal(err)
	}

	cfg := ChatConfig{ChatID: 123, NotifyTime: "09:00", Timezone: "UTC"}
	if err := s1.Set(cfg); err != nil {
		t.Fatal(err)
	}

	// Reload from disk
	s2, err := NewJSONStorage(name)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := s2.Get(123)
	if !ok {
		t.Fatal("config should survive a reload")
	}
	if got.NotifyTime != "09:00" {
		t.Errorf("NotifyTime: got %q, want %q", got.NotifyTime, "09:00")
	}
	if got.Timezone != "UTC" {
		t.Errorf("Timezone: got %q, want %q", got.Timezone, "UTC")
	}
}

func TestRoundTripPersistsFields(t *testing.T) {
	tests := []struct {
		name string
		cfg  ChatConfig
	}{
		{
			name: "difficulties and daily pick",
			cfg: ChatConfig{
				ChatID:       42,
				NotifyTime:   "10:30",
				Timezone:     "Europe/Moscow",
				Members:      map[string]UserStat{"7": {Name: "Alice", Count: 3, LastSolvedDate: "2026-09-26"}},
				Difficulties: []string{"Medium", "Hard"},
				DailyPick: &DailyPick{
					Date:            "2026-09-27",
					DailyDifficulty: "Easy",
					ID:              "42",
					Title:           "Trapping Rain Water",
					Link:            "/problems/trapping-rain-water/",
					Difficulty:      "Hard",
					Tags:            []string{"Array", "Two Pointers"},
				},
			},
		},
		{
			name: "no difficulties and no pick stay nil",
			cfg:  ChatConfig{ChatID: 43, NotifyTime: "08:00", Timezone: "UTC"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")

			s1, err := NewJSONStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := s1.Set(tt.cfg); err != nil {
				t.Fatal(err)
			}

			s2, err := NewJSONStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := s2.Get(tt.cfg.ChatID)
			if !ok {
				t.Fatal("config should survive a reload")
			}
			if !reflect.DeepEqual(got, tt.cfg) {
				t.Errorf("reloaded config:\n got %+v\nwant %+v", got, tt.cfg)
			}
		})
	}
}

// fullConfig returns a fresh config with every reference field set.
func fullConfig() ChatConfig {
	return ChatConfig{
		ChatID:       42,
		NotifyTime:   "10:30",
		Timezone:     "Europe/Moscow",
		Members:      map[string]UserStat{"7": {Name: "Alice", Count: 3, LastSolvedDate: "2026-09-26"}},
		Difficulties: []string{"Medium", "Hard"},
		DailyPick: &DailyPick{
			Date:            "2026-09-27",
			DailyDifficulty: "Easy",
			ID:              "42",
			Title:           "Trapping Rain Water",
			Link:            "/problems/trapping-rain-water/",
			Difficulty:      "Hard",
			Tags:            []string{"Array", "Two Pointers"},
		},
	}
}

// mutateConfig writes into every reference field of cfg.
func mutateConfig(cfg ChatConfig) {
	cfg.Members["8"] = UserStat{Name: "Bob", Count: 1}
	cfg.Difficulties[0] = "Easy"
	cfg.DailyPick.Title = "Mutated"
	cfg.DailyPick.Tags[0] = "Mutated"
}

func TestStoredConfigIsIndependent(t *testing.T) {
	tests := []struct {
		name   string
		cfg    func() ChatConfig
		mutate func(s *jsonStorage, stored ChatConfig)
	}{
		{
			name:   "mutating a Get result leaves the store unchanged",
			cfg:    fullConfig,
			mutate: func(s *jsonStorage, _ ChatConfig) { got, _ := s.Get(42); mutateConfig(got) },
		},
		{
			name:   "mutating an All result leaves the store unchanged",
			cfg:    fullConfig,
			mutate: func(s *jsonStorage, _ ChatConfig) { mutateConfig(s.All()[0]) },
		},
		{
			name:   "mutating the config passed to Set leaves the store unchanged",
			cfg:    fullConfig,
			mutate: func(_ *jsonStorage, stored ChatConfig) { mutateConfig(stored) },
		},
		{
			name: "mutating the config inside a discarded Update leaves the store unchanged",
			cfg:  fullConfig,
			mutate: func(s *jsonStorage, _ ChatConfig) {
				_, _ = s.Update(42, func(cfg *ChatConfig) bool { mutateConfig(*cfg); return false })
			},
		},
		{
			name: "mutating the config an Update saved leaves the store unchanged",
			cfg:  fullConfig,
			mutate: func(s *jsonStorage, _ ChatConfig) {
				var saved ChatConfig
				_, _ = s.Update(42, func(cfg *ChatConfig) bool { saved = *cfg; return true })
				mutateConfig(saved)
			},
		},
		{
			name:   "nil reference fields stay nil",
			cfg:    func() ChatConfig { return ChatConfig{ChatID: 42, NotifyTime: "08:00", Timezone: "UTC"} },
			mutate: func(*jsonStorage, ChatConfig) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewJSONStorage(filepath.Join(t.TempDir(), "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			stored := tt.cfg()
			if err := s.Set(stored); err != nil {
				t.Fatal(err)
			}

			tt.mutate(s, stored)

			got, ok := s.Get(42)
			if !ok {
				t.Fatal("config should be stored")
			}
			if want := tt.cfg(); !reflect.DeepEqual(got, want) {
				t.Errorf("stored config:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	bumpAlice := func(cfg *ChatConfig) bool {
		stat := cfg.Members["7"]
		stat.Count++
		cfg.Members["7"] = stat
		return true
	}
	withAliceCount := func(n int) ChatConfig {
		cfg := fullConfig()
		cfg.Members["7"] = UserStat{Name: "Alice", Count: n, LastSolvedDate: "2026-09-26"}
		return cfg
	}

	tests := []struct {
		name      string
		chatID    int64
		fn        func(cfg *ChatConfig) bool
		callers   int
		wantFound bool
		wantCalls int
		want      ChatConfig
	}{
		{
			name:      "saves the change fn makes",
			chatID:    42,
			fn:        bumpAlice,
			callers:   1,
			wantFound: true,
			wantCalls: 1,
			want:      withAliceCount(4),
		},
		{
			name:      "fn returning false saves nothing",
			chatID:    42,
			fn:        func(cfg *ChatConfig) bool { bumpAlice(cfg); return false },
			callers:   1,
			wantFound: true,
			wantCalls: 1,
			want:      fullConfig(),
		},
		{
			name:      "missing chat is neither passed to fn nor created",
			chatID:    99,
			fn:        bumpAlice,
			callers:   1,
			wantFound: false,
			wantCalls: 0,
			want:      fullConfig(),
		},
		{
			name:      "concurrent updates of one chat are all kept",
			chatID:    42,
			fn:        bumpAlice,
			callers:   50,
			wantFound: true,
			wantCalls: 50,
			want:      withAliceCount(53),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s, err := NewJSONStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Set(fullConfig()); err != nil {
				t.Fatal(err)
			}

			var calls atomic.Int32
			var wg sync.WaitGroup
			for range tt.callers {
				wg.Go(func() {
					found, err := s.Update(tt.chatID, func(cfg *ChatConfig) bool {
						calls.Add(1)
						return tt.fn(cfg)
					})
					if err != nil {
						t.Errorf("Update: %v", err)
					}
					if found != tt.wantFound {
						t.Errorf("found: got %v, want %v", found, tt.wantFound)
					}
				})
			}
			wg.Wait()

			if got := int(calls.Load()); got != tt.wantCalls {
				t.Errorf("fn calls: got %d, want %d", got, tt.wantCalls)
			}
			reloaded, err := NewJSONStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := reloaded.Get(42); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("reloaded config:\n got %+v\nwant %+v", got, tt.want)
			}
			if _, ok := reloaded.Get(99); ok {
				t.Error("Update must not create a missing chat")
			}
		})
	}
}

func TestLoadLegacyConfig(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		chatID int64
		want   ChatConfig
	}{
		{
			name: "config without difficulty fields",
			raw: `{
  "chats": {
    "123": {
      "chat_id": 123,
      "notify_time": "09:00",
      "timezone": "Asia/Tbilisi",
      "members": {
        "7": {"name": "Alice", "count": 5, "last_solved_date": "2026-09-20"}
      }
    }
  }
}`,
			chatID: 123,
			want: ChatConfig{
				ChatID:     123,
				NotifyTime: "09:00",
				Timezone:   "Asia/Tbilisi",
				Members:    map[string]UserStat{"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-20"}},
			},
		},
		{
			name:   "config without members",
			raw:    `{"chats":{"5":{"chat_id":5,"notify_time":"21:15","timezone":"UTC"}}}`,
			chatID: 5,
			want:   ChatConfig{ChatID: 5, NotifyTime: "21:15", Timezone: "UTC"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.raw), 0o644); err != nil {
				t.Fatal(err)
			}

			s, err := NewJSONStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := s.Get(tt.chatID)
			if !ok {
				t.Fatal("legacy config should load")
			}
			if got.Difficulties != nil {
				t.Errorf("Difficulties: got %v, want nil", got.Difficulties)
			}
			if got.DailyPick != nil {
				t.Errorf("DailyPick: got %+v, want nil", got.DailyPick)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loaded config:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
