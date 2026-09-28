package storage

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// legacyUserStat, legacyDailyPick and legacyChatConfig are verbatim copies of
// the structs the pre-refactor binary persisted (internal/storage/storage.go
// at 8879c84). Decoding with them shows what a rolled-back binary reads.
type legacyUserStat struct {
	Name           string `json:"name"`
	Count          int    `json:"count"`
	LastSolvedDate string `json:"last_solved_date"`
}

type legacyDailyPick struct {
	Date            string   `json:"date"`
	DailyDifficulty string   `json:"daily_difficulty"`
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Link            string   `json:"link"`
	Difficulty      string   `json:"difficulty"`
	Tags            []string `json:"tags"`
}

type legacyChatConfig struct {
	ChatID       int64                     `json:"chat_id"`
	NotifyTime   string                    `json:"notify_time"`
	Timezone     string                    `json:"timezone"`
	Members      map[string]legacyUserStat `json:"members"`
	Difficulties []string                  `json:"difficulties,omitempty"`
	DailyPick    *legacyDailyPick          `json:"daily_pick,omitempty"`
}

type legacyFile struct {
	Chats map[string]legacyChatConfig `json:"chats"`
}

func decodeLegacy(t *testing.T, raw []byte) legacyFile {
	t.Helper()
	var f legacyFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode with the legacy structs: %v", err)
	}
	return f
}

// fromLegacy maps what the old binary reads from raw onto domain chats, field
// by field, sorted by ChatID like All.
func fromLegacy(t *testing.T, raw []byte) []domain.Chat {
	t.Helper()
	f := decodeLegacy(t, raw)
	chats := make([]domain.Chat, 0, len(f.Chats))
	for _, c := range f.Chats {
		chat := domain.Chat{
			ChatID:       c.ChatID,
			NotifyTime:   c.NotifyTime,
			Timezone:     c.Timezone,
			Difficulties: c.Difficulties,
		}
		if c.Members != nil {
			chat.Members = make(map[string]domain.Member, len(c.Members))
			for k, m := range c.Members {
				chat.Members[k] = domain.Member{Name: m.Name, Count: m.Count, LastSolvedDate: m.LastSolvedDate}
			}
		}
		if p := c.DailyPick; p != nil {
			chat.DailyPick = &domain.Pick{
				Problem: domain.Problem{
					Date:       p.Date,
					ID:         p.ID,
					Title:      p.Title,
					Link:       p.Link,
					Difficulty: p.Difficulty,
					Tags:       p.Tags,
				},
				DailyDifficulty: p.DailyDifficulty,
			}
		}
		chats = append(chats, chat)
	}
	slices.SortFunc(chats, func(a, b domain.Chat) int { return cmp.Compare(a.ChatID, b.ChatID) })
	return chats
}

// fullChat returns a fresh chat with every reference field set.
func fullChat() domain.Chat {
	return domain.Chat{
		ChatID:       42,
		NotifyTime:   "10:30",
		Timezone:     "Europe/Moscow",
		Members:      map[string]domain.Member{"7": {Name: "Alice", Count: 3, LastSolvedDate: "2026-09-26"}},
		Difficulties: []string{"Medium", "Hard"},
		DailyPick: &domain.Pick{
			Problem: domain.Problem{
				Date:       "2026-09-27",
				ID:         "42",
				Title:      "Trapping Rain Water",
				Link:       "/problems/trapping-rain-water/",
				Difficulty: "Hard",
				Tags:       []string{"Array", "Two Pointers"},
			},
			DailyDifficulty: "Easy",
		},
	}
}

// minimalChat returns a fresh chat whose reference fields are all nil.
func minimalChat() domain.Chat {
	return domain.Chat{ChatID: 43, NotifyTime: "08:00", Timezone: "UTC"}
}

// mutateChat writes into every reference field of c.
func mutateChat(c domain.Chat) {
	c.Members["8"] = domain.Member{Name: "Bob", Count: 1}
	c.Difficulties[0] = "Easy"
	c.DailyPick.Title = "Mutated"
	c.DailyPick.Tags[0] = "Mutated"
}

func newStore(t *testing.T, path string) *jsonStorage {
	t.Helper()
	s, err := NewJSONStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// put stores c as is through Upsert.
func put(t *testing.T, s *jsonStorage, c domain.Chat) {
	t.Helper()
	if err := s.Upsert(t.Context(), c.ChatID, func(stored *domain.Chat) { *stored = c }); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTrip(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name string
		chat func() domain.Chat
	}{
		{name: "every field survives a reload", chat: fullChat},
		{name: "nil reference fields stay nil after a reload", chat: minimalChat},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			put(t, newStore(t, path), tt.chat())

			got, ok, err := newStore(t, path).Get(ctx, tt.chat().ChatID)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("chat should survive a reload")
			}
			if want := tt.chat(); !reflect.DeepEqual(got, want) {
				t.Errorf("reloaded chat:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestStoredChatIsIndependent(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name   string
		chat   func() domain.Chat
		mutate func(s *jsonStorage)
	}{
		{
			name: "mutating a Get result leaves the store unchanged",
			chat: fullChat,
			mutate: func(s *jsonStorage) {
				got, _, _ := s.Get(ctx, 42)
				mutateChat(got)
			},
		},
		{
			name: "mutating an All result leaves the store unchanged",
			chat: fullChat,
			mutate: func(s *jsonStorage) {
				all, _ := s.All(ctx)
				mutateChat(all[0])
			},
		},
		{
			name: "mutating the chat an Upsert saved leaves the store unchanged",
			chat: fullChat,
			mutate: func(s *jsonStorage) {
				var saved domain.Chat
				_ = s.Upsert(ctx, 42, func(c *domain.Chat) { saved = *c })
				mutateChat(saved)
			},
		},
		{
			name: "mutating the chat inside a discarded Update leaves the store unchanged",
			chat: fullChat,
			mutate: func(s *jsonStorage) {
				_, _ = s.Update(ctx, 42, func(c *domain.Chat) bool { mutateChat(*c); return false })
			},
		},
		{
			name: "mutating the chat an Update saved leaves the store unchanged",
			chat: fullChat,
			mutate: func(s *jsonStorage) {
				var saved domain.Chat
				_, _ = s.Update(ctx, 42, func(c *domain.Chat) bool { saved = *c; return true })
				mutateChat(saved)
			},
		},
		{
			name:   "nil reference fields stay nil",
			chat:   func() domain.Chat { c := minimalChat(); c.ChatID = 42; return c },
			mutate: func(*jsonStorage) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t, filepath.Join(t.TempDir(), "config.json"))
			put(t, s, tt.chat())

			tt.mutate(s)

			got, ok, err := s.Get(ctx, 42)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("chat should be stored")
			}
			if want := tt.chat(); !reflect.DeepEqual(got, want) {
				t.Errorf("stored chat:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestUpsert(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name     string
		before   []domain.Chat
		chatID   int64
		fn       func(c *domain.Chat)
		wantSeen domain.Chat // what fn is given
		want     domain.Chat
	}{
		{
			name:     "creates a missing chat with its ChatID",
			chatID:   100,
			fn:       func(c *domain.Chat) { c.NotifyTime, c.Timezone = "09:00", "UTC" },
			wantSeen: domain.Chat{ChatID: 100},
			want:     domain.Chat{ChatID: 100, NotifyTime: "09:00", Timezone: "UTC"},
		},
		{
			name:   "keeps the members and pick of an existing chat",
			before: []domain.Chat{fullChat()},
			chatID: 42,
			fn: func(c *domain.Chat) {
				c.NotifyTime, c.Timezone, c.Difficulties = "21:15", "Asia/Tbilisi", []string{"Easy"}
			},
			wantSeen: fullChat(),
			want: func() domain.Chat {
				c := fullChat()
				c.NotifyTime, c.Timezone, c.Difficulties = "21:15", "Asia/Tbilisi", []string{"Easy"}
				return c
			}(),
		},
		{
			name:     "the key wins over a ChatID set by fn",
			chatID:   -100500,
			fn:       func(c *domain.Chat) { c.ChatID, c.NotifyTime = 7, "07:00" },
			wantSeen: domain.Chat{ChatID: -100500},
			want:     domain.Chat{ChatID: -100500, NotifyTime: "07:00"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s := newStore(t, path)
			for _, c := range tt.before {
				put(t, s, c)
			}

			calls := 0
			var seen domain.Chat
			if err := s.Upsert(ctx, tt.chatID, func(c *domain.Chat) {
				calls++
				seen = clone(*c)
				tt.fn(c)
			}); err != nil {
				t.Fatalf("Upsert: %v", err)
			}

			if calls != 1 {
				t.Errorf("fn calls: got %d, want 1", calls)
			}
			if !reflect.DeepEqual(seen, tt.wantSeen) {
				t.Errorf("fn got:\n got %+v\nwant %+v", seen, tt.wantSeen)
			}
			all, err := newStore(t, path).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 1 || !reflect.DeepEqual(all[0], tt.want) {
				t.Errorf("reloaded chats:\n got %+v\nwant [%+v]", all, tt.want)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	ctx := t.Context()
	bumpAlice := func(c *domain.Chat) bool {
		m := c.Members["7"]
		m.Count++
		c.Members["7"] = m
		return true
	}
	withAliceCount := func(n int) domain.Chat {
		c := fullChat()
		c.Members["7"] = domain.Member{Name: "Alice", Count: n, LastSolvedDate: "2026-09-26"}
		return c
	}

	tests := []struct {
		name      string
		chatID    int64
		fn        func(c *domain.Chat) bool
		callers   int
		wantFound bool
		wantCalls int
		want      domain.Chat
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
			fn:        func(c *domain.Chat) bool { bumpAlice(c); return false },
			callers:   1,
			wantFound: true,
			wantCalls: 1,
			want:      fullChat(),
		},
		{
			name:      "missing chat is neither passed to fn nor created",
			chatID:    99,
			fn:        bumpAlice,
			callers:   1,
			wantFound: false,
			wantCalls: 0,
			want:      fullChat(),
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
			s := newStore(t, path)
			put(t, s, fullChat())

			var calls atomic.Int32
			var wg sync.WaitGroup
			for range tt.callers {
				wg.Go(func() {
					found, err := s.Update(ctx, tt.chatID, func(c *domain.Chat) bool {
						calls.Add(1)
						return tt.fn(c)
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
			all, err := newStore(t, path).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 1 || !reflect.DeepEqual(all[0], tt.want) {
				t.Errorf("reloaded chats:\n got %+v\nwant [%+v]", all, tt.want)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name   string
		chatID int64
		want   []int64
	}{
		{name: "removes the chat from memory and file", chatID: 42, want: []int64{43}},
		{name: "a missing chat leaves the others", chatID: 99, want: []int64{42, 43}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s := newStore(t, path)
			put(t, s, fullChat())
			put(t, s, minimalChat())

			if err := s.Delete(ctx, tt.chatID); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			for _, store := range []*jsonStorage{s, newStore(t, path)} {
				all, err := store.All(ctx)
				if err != nil {
					t.Fatal(err)
				}
				ids := make([]int64, 0, len(all))
				for _, c := range all {
					ids = append(ids, c.ChatID)
				}
				if !slices.Equal(ids, tt.want) {
					t.Errorf("chat IDs: got %v, want %v", ids, tt.want)
				}
			}
		})
	}
}

func TestAll(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name  string
		chats []int64
		want  []int64
	}{
		{name: "sorted by ChatID", chats: []int64{42, -100123, 5}, want: []int64{-100123, 5, 42}},
		{name: "an empty store has no chats", chats: nil, want: []int64{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t, filepath.Join(t.TempDir(), "config.json"))
			for _, id := range tt.chats {
				put(t, s, domain.Chat{ChatID: id})
			}

			all, err := s.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, 0, len(all))
			for _, c := range all {
				ids = append(ids, c.ChatID)
			}
			if !slices.Equal(ids, tt.want) {
				t.Errorf("chat IDs: got %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestCancelledContext(t *testing.T) {
	ctx := t.Context()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	mustNotRun := func(t *testing.T) func(*domain.Chat) bool {
		return func(*domain.Chat) bool { t.Error("fn must not run on a done ctx"); return true }
	}

	tests := []struct {
		name string
		call func(t *testing.T, s *jsonStorage) error
	}{
		{
			name: "Get",
			call: func(_ *testing.T, s *jsonStorage) error { _, _, err := s.Get(cancelled, 42); return err },
		},
		{
			name: "Upsert",
			call: func(t *testing.T, s *jsonStorage) error {
				return s.Upsert(cancelled, 99, func(c *domain.Chat) { mustNotRun(t)(c) })
			},
		},
		{
			name: "Update",
			call: func(t *testing.T, s *jsonStorage) error { _, err := s.Update(cancelled, 42, mustNotRun(t)); return err },
		},
		{
			name: "Delete",
			call: func(_ *testing.T, s *jsonStorage) error { return s.Delete(cancelled, 42) },
		},
		{
			name: "All",
			call: func(_ *testing.T, s *jsonStorage) error { _, err := s.All(cancelled); return err },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s := newStore(t, path)
			put(t, s, fullChat())
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if err := tt.call(t, s); !errors.Is(err, context.Canceled) {
				t.Errorf("error: got %v, want %v", err, context.Canceled)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Errorf("file changed:\n got %s\nwant %s", after, before)
			}
			all, err := s.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if want := []domain.Chat{fullChat()}; !reflect.DeepEqual(all, want) {
				t.Errorf("chats in memory:\n got %+v\nwant %+v", all, want)
			}
		})
	}
}

func TestLoadLegacyConfig(t *testing.T) {
	ctx := t.Context()
	golden, err := os.ReadFile(filepath.Join("testdata", "legacy_config.json"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		raw  []byte
		want []domain.Chat
	}{
		{
			name: "config without difficulty fields",
			raw: []byte(`{
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
}`),
			want: []domain.Chat{{
				ChatID:     123,
				NotifyTime: "09:00",
				Timezone:   "Asia/Tbilisi",
				Members:    map[string]domain.Member{"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-20"}},
			}},
		},
		{
			name: "config without members",
			raw:  []byte(`{"chats":{"5":{"chat_id":5,"notify_time":"21:15","timezone":"UTC"}}}`),
			want: []domain.Chat{{ChatID: 5, NotifyTime: "21:15", Timezone: "UTC"}},
		},
		{
			// Written by the pre-refactor jsonStorage; the flat daily_pick
			// must fill both Pick.Problem and Pick.DailyDifficulty.
			name: "golden fixture written by the old binary",
			raw:  golden,
			want: fromLegacy(t, golden),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, tt.raw, 0o644); err != nil {
				t.Fatal(err)
			}

			got, err := newStore(t, path).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loaded chats:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// TestGoldenFixtureShape pins what the fixture must cover, so the golden rows
// above and below cannot pass on a fixture that lost a case.
func TestGoldenFixtureShape(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "legacy_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	chats := fromLegacy(t, golden)

	tests := []struct {
		name  string
		match func(c domain.Chat) bool
	}{
		{name: "a negative group ID", match: func(c domain.Chat) bool { return c.ChatID < 0 }},
		{name: "a legacy chat with members null and no difficulties", match: func(c domain.Chat) bool {
			return c.Members == nil && c.Difficulties == nil
		}},
		{name: "a chat with members and a full flat daily_pick", match: func(c domain.Chat) bool {
			p := c.DailyPick
			return len(c.Members) > 0 && p != nil && p.Date != "" && p.DailyDifficulty != "" &&
				p.ID != "" && p.Title != "" && p.Link != "" && p.Difficulty != "" && len(p.Tags) > 0
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.ContainsFunc(chats, tt.match) {
				t.Errorf("testdata/legacy_config.json has no chat with %s: %+v", tt.name, chats)
			}
		})
	}
}

func TestRollbackSafety(t *testing.T) {
	ctx := t.Context()
	golden, err := os.ReadFile(filepath.Join("testdata", "legacy_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	newPick := domain.Pick{
		Problem: domain.Problem{
			Date:       "2026-09-29",
			ID:         "2",
			Title:      "Add Two Numbers",
			Link:       "/problems/add-two-numbers/",
			Difficulty: "Medium",
			Tags:       []string{"Linked List", "Math", "Recursion"},
		},
		DailyDifficulty: "Hard",
	}

	tests := []struct {
		name string
		fn   func(c *domain.Chat) bool // applied to every chat through Update
		// want returns what the old binary must read back.
		want func(t *testing.T) legacyFile
		// sameBytes: re-encoding what the old binary reads gives the fixture back.
		sameBytes bool
	}{
		{
			name:      "a rewrite by the new code reads back unchanged",
			fn:        func(*domain.Chat) bool { return true },
			want:      func(t *testing.T) legacyFile { return decodeLegacy(t, golden) },
			sameBytes: true,
		},
		{
			name: "a pick saved by the new code is the old flat daily_pick",
			fn: func(c *domain.Chat) bool {
				p := newPick
				p.Tags = slices.Clone(newPick.Tags)
				c.DailyPick = &p
				return true
			},
			want: func(t *testing.T) legacyFile {
				f := decodeLegacy(t, golden)
				for k, c := range f.Chats {
					c.DailyPick = &legacyDailyPick{
						Date:            "2026-09-29",
						DailyDifficulty: "Hard",
						ID:              "2",
						Title:           "Add Two Numbers",
						Link:            "/problems/add-two-numbers/",
						Difficulty:      "Medium",
						Tags:            []string{"Linked List", "Math", "Recursion"},
					}
					f.Chats[k] = c
				}
				return f
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, golden, 0o644); err != nil {
				t.Fatal(err)
			}
			s := newStore(t, path)
			all, err := s.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range all {
				if _, err := s.Update(ctx, c.ChatID, tt.fn); err != nil {
					t.Fatalf("Update %d: %v", c.ChatID, err)
				}
			}

			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			old := decodeLegacy(t, written)
			if want := tt.want(t); !reflect.DeepEqual(old, want) {
				t.Errorf("old binary reads:\n got %+v\nwant %+v", old, want)
			}
			if !tt.sameBytes {
				return
			}
			reencoded, err := json.MarshalIndent(old, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(reencoded, bytes.TrimSpace(golden)) {
				t.Errorf("old binary re-encodes:\n%s\nwant the fixture:\n%s", reencoded, golden)
			}
		})
	}
}

// TestSaveInPlace pins the bind-mount contract (spec §4.7): every save
// rewrites config.json in place. A temp file renamed over it fails with EBUSY
// when config.json is bind-mounted into the container.
func TestSaveInPlace(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name string
		save func(s *jsonStorage) error
	}{
		{
			name: "Upsert",
			save: func(s *jsonStorage) error {
				return s.Upsert(ctx, 43, func(c *domain.Chat) { c.NotifyTime = "07:00" })
			},
		},
		{
			name: "Update",
			save: func(s *jsonStorage) error {
				_, err := s.Update(ctx, 42, func(c *domain.Chat) bool { c.NotifyTime = "07:00"; return true })
				return err
			},
		},
		{
			name: "Delete",
			save: func(s *jsonStorage) error { return s.Delete(ctx, 42) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			s := newStore(t, path)
			put(t, s, fullChat())
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			if err := tt.save(s); err != nil {
				t.Fatal(err)
			}

			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) {
				t.Error("config.json was replaced by another file, want it rewritten in place")
			}
			if after.Mode() != before.Mode() {
				t.Errorf("mode: got %v, want %v", after.Mode(), before.Mode())
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "config.json" {
				t.Errorf("directory holds %v, want only config.json", entries)
			}
		})
	}
}

// TestLoadDamagedFile pins today's start on a damaged config.json, which a
// crash mid-write leaves behind (spec §12): the error is logged, the store
// starts empty rather than half-loaded, and the next save writes a valid
// file. Follow-up 1 deliberately turns this into a startup error.
func TestLoadDamagedFile(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty file", raw: ""},
		{name: "truncated mid-write", raw: `{"chats":{"42":{"chat_id":42,"notify_time":"09:00","timezone":"UTC","members":{"7":{"name":"Ali`},
		{name: "a field of the wrong type", raw: `{"chats":{"42":{"chat_id":42,"notify_time":"09:00","timezone":"UTC","members":{"7":{"name":"Alice","count":"five"}}}}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.raw), 0o644); err != nil {
				t.Fatal(err)
			}

			s, err := NewJSONStorage(path)
			if err != nil {
				t.Fatalf("NewJSONStorage: %v, want an empty store", err)
			}
			all, err := s.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 0 {
				t.Errorf("chats: got %+v, want none", all)
			}

			put(t, s, minimalChat())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := fromLegacy(t, raw), []domain.Chat{minimalChat()}; !reflect.DeepEqual(got, want) {
				t.Errorf("file after the next save:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}
