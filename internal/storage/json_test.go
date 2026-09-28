package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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

func chatIDs(t *testing.T, s *jsonStorage) []int64 {
	t.Helper()
	all, err := s.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(all))
	for _, c := range all {
		ids = append(ids, c.ChatID)
	}
	return ids
}

func TestStoredChatIsIndependent(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name   string
		mutate func(s *jsonStorage)
	}{
		{
			name:   "mutating a Get result leaves the store unchanged",
			mutate: func(s *jsonStorage) { got, _, _ := s.Get(ctx, 42); mutateChat(got) },
		},
		{
			name:   "mutating an All result leaves the store unchanged",
			mutate: func(s *jsonStorage) { all, _ := s.All(ctx); mutateChat(all[0]) },
		},
		{
			name: "mutating the chat inside a discarded Update leaves the store unchanged",
			mutate: func(s *jsonStorage) {
				_, _ = s.Update(ctx, 42, func(c *domain.Chat) bool { mutateChat(*c); return false })
			},
		},
		{
			// Upsert saves through the same mutate.
			name: "mutating the chat an Update saved leaves the store unchanged",
			mutate: func(s *jsonStorage) {
				var saved domain.Chat
				_, _ = s.Update(ctx, 42, func(c *domain.Chat) bool { saved = *c; return true })
				mutateChat(saved)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t, filepath.Join(t.TempDir(), "config.json"))
			put(t, s, fullChat())

			tt.mutate(s)

			got, ok, err := s.Get(ctx, 42)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("chat should be stored")
			}
			if want := fullChat(); !reflect.DeepEqual(got, want) {
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
			name:     "creates a missing chat under its key; nil fields survive a reload",
			chatID:   -100500,
			fn:       func(c *domain.Chat) { c.ChatID, c.NotifyTime, c.DailyPick = 7, "09:00", &domain.Pick{} },
			wantSeen: domain.Chat{ChatID: -100500},
			want:     domain.Chat{ChatID: -100500, NotifyTime: "09:00", DailyPick: &domain.Pick{}}, // nil Tags stay nil
		},
		{
			name:   "keeps the members and pick of an existing chat; every field survives a reload",
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
		want      domain.Chat
	}{
		{
			name:      "fn returning false saves nothing",
			chatID:    42,
			fn:        func(c *domain.Chat) bool { bumpAlice(c); return false },
			callers:   1,
			wantFound: true,
			want:      fullChat(),
		},
		{
			name:      "missing chat is neither passed to fn nor created",
			chatID:    99,
			fn:        func(*domain.Chat) bool { t.Error("fn called for a missing chat"); return true },
			callers:   1,
			wantFound: false,
			want:      fullChat(),
		},
		{
			name:      "concurrent updates of one chat are all saved",
			chatID:    42,
			fn:        bumpAlice,
			callers:   50,
			wantFound: true,
			want:      withAliceCount(53),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s := newStore(t, path)
			put(t, s, fullChat())

			var wg sync.WaitGroup
			var calls atomic.Int32
			for range tt.callers {
				wg.Go(func() {
					found, err := s.Update(ctx, tt.chatID, func(c *domain.Chat) bool { calls.Add(1); return tt.fn(c) })
					if err != nil || found != tt.wantFound {
						t.Errorf("Update: got (%v, %v), want (%v, nil)", found, err, tt.wantFound)
					}
				})
			}
			wg.Wait()

			if tt.wantFound && int(calls.Load()) != tt.callers {
				t.Errorf("fn calls: got %d, want %d", calls.Load(), tt.callers)
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

// TestDelete also pins All's ChatID order: the chats are put unsorted.
func TestDelete(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name   string
		chatID int64
		want   []int64
	}{
		{name: "removes the chat from memory and file", chatID: 42, want: []int64{-100123, 5, 43}},
		{name: "a missing chat leaves the others", chatID: 99, want: []int64{-100123, 5, 42, 43}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s := newStore(t, path)
			for _, c := range []domain.Chat{fullChat(), {ChatID: -100123}, minimalChat(), {ChatID: 5}} {
				put(t, s, c)
			}

			if err := s.Delete(ctx, tt.chatID); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			for _, store := range []*jsonStorage{s, newStore(t, path)} {
				if ids := chatIDs(t, store); !slices.Equal(ids, tt.want) {
					t.Errorf("chat IDs: got %v, want %v", ids, tt.want)
				}
			}
		})
	}
}

func TestCancelledContext(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
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

			// Every change in memory is saved, so an unchanged file is an unchanged store.
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Errorf("file changed:\n got %s\nwant %s", after, before)
			}
		})
	}
}

// TestLoad pins how an existing config.json is read. The golden fixture was
// written by the pre-refactor jsonStorage and is never regenerated. A damaged
// file, which a crash mid-write leaves behind, is logged and the store starts
// empty (spec §12); follow-up 1 turns it into a startup error.
func TestLoad(t *testing.T) {
	ctx := t.Context()
	golden, err := os.ReadFile(filepath.Join("testdata", "legacy_config.json"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		raw  string
		want []domain.Chat
	}{
		{
			name: "golden fixture written by the old binary",
			raw:  string(golden),
			want: []domain.Chat{
				{
					ChatID: -1001234567890, NotifyTime: "07:00", Timezone: "America/New_York",
					Members: map[string]domain.Member{
						"7": {Name: "Alice", Count: 1, LastSolvedDate: "2026-09-26"},
						"8": {Name: "@bob", Count: 4, LastSolvedDate: "2026-09-27"},
					},
					Difficulties: []string{"Medium", "Hard"},
				},
				{
					ChatID: 100, NotifyTime: "09:00", Timezone: "Europe/Moscow",
					Members:      map[string]domain.Member{"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-27"}},
					Difficulties: []string{"Easy"},
					DailyPick: &domain.Pick{
						Problem: domain.Problem{Date: "2026-09-28", ID: "1", Title: "Two Sum", Link: "/problems/two-sum/",
							Difficulty: "Easy", Tags: []string{"Array", "Hash Table"}},
						DailyDifficulty: "Hard",
					},
				},
				{ChatID: 200, NotifyTime: "21:15", Timezone: "Asia/Tbilisi"},
			},
		},
		{
			name: "config without members",
			raw:  `{"chats":{"5":{"chat_id":5,"notify_time":"21:15","timezone":"UTC"}}}`,
			want: []domain.Chat{{ChatID: 5, NotifyTime: "21:15", Timezone: "UTC"}},
		},
		{name: "empty file", raw: "", want: []domain.Chat{}},
		{name: "truncated mid-write", raw: `{"chats":{"42":{"chat_id":42,"notify_time":"09:00","timezone":"UTC","members":{"7":{"name":"Ali`, want: []domain.Chat{}},
		{name: "a field of the wrong type", raw: `{"chats":{"42":{"chat_id":42,"notify_time":"09:00","timezone":"UTC","members":{"7":{"name":"Alice","count":"five"}}}}}`, want: []domain.Chat{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.raw), 0o644); err != nil {
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

// TestRollbackSafety pins what the old binary reads after the new code saves
// the golden fixture: the same chats, and the same bytes apart from the key
// order inside daily_pick (spec §11 g).
func TestRollbackSafety(t *testing.T) {
	ctx := t.Context()
	golden, err := os.ReadFile(filepath.Join("testdata", "legacy_config.json"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		save func(s *jsonStorage) error
	}{
		{
			name: "Update",
			save: func(s *jsonStorage) error {
				_, err := s.Update(ctx, 100, func(*domain.Chat) bool { return true })
				return err
			},
		},
		{
			name: "Delete of a missing chat",
			save: func(s *jsonStorage) error { return s.Delete(ctx, 99) },
		},
	}

	want := strings.NewReplacer(
		"        \"daily_difficulty\": \"Hard\",\n", "",
		"\"Hash Table\"\n        ]\n", "\"Hash Table\"\n        ],\n        \"daily_difficulty\": \"Hard\"\n",
	).Replace(string(golden))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, golden, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := tt.save(newStore(t, path)); err != nil {
				t.Fatal(err)
			}

			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(written) != want {
				t.Errorf("written:\n%s\nwant:\n%s", written, want)
			}
			if got, want := decodeLegacy(t, written), decodeLegacy(t, golden); !reflect.DeepEqual(got, want) {
				t.Errorf("old binary reads:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

// TestSaveInPlace pins the bind-mount contract (spec §4.7): a save rewrites
// config.json in place. A temp file renamed over it fails with EBUSY when
// config.json is bind-mounted into the container.
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
			name: "Delete",
			save: func(s *jsonStorage) error { return s.Delete(ctx, 42) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
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
		})
	}
}
