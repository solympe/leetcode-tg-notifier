package storage

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strconv"
	"sync"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// jsonFile is the on-disk format, {"chats":{"<id>":Chat}}. The domain JSON
// tags are the file's contract: renaming one needs a migration.
type jsonFile struct {
	Chats map[string]domain.Chat `json:"chats"`
}

// jsonStorage keeps every chat in memory and rewrites the whole file on each
// change. One mutex guards both, so every method is atomic. A method whose
// ctx is already done returns ctx.Err() before taking the lock; a started
// write always completes, because file I/O cannot be cancelled.
type jsonStorage struct {
	mu   sync.Mutex
	path string
	data jsonFile
}

func NewJSONStorage(path string) (*jsonStorage, error) {
	s := &jsonStorage{
		path: path,
		data: jsonFile{Chats: make(map[string]domain.Chat)},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *jsonStorage) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("load storage: %w", err)
	}
	if err := json.Unmarshal(data, &s.data); err != nil {
		log.Printf("storage: unmarshal error, starting fresh: %v", err)
		s.data = jsonFile{Chats: make(map[string]domain.Chat)}
	}
	return nil
}

// save writes the file in place, not through a temp file and rename, which
// fails with EBUSY on a bind-mounted config.json.
func (s *jsonStorage) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func key(chatID int64) string {
	return strconv.FormatInt(chatID, 10)
}

// clone deep-copies the reference fields of c, so the stored chats and the
// ones callers hold never share memory; nil fields stay nil.
func clone(c domain.Chat) domain.Chat {
	c.Members = maps.Clone(c.Members)
	c.Difficulties = slices.Clone(c.Difficulties)
	if c.DailyPick != nil {
		pick := *c.DailyPick
		pick.Tags = slices.Clone(pick.Tags)
		c.DailyPick = &pick
	}
	return c
}

func (s *jsonStorage) Get(ctx context.Context, chatID int64) (domain.Chat, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Chat{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Chats[key(chatID)]
	return clone(c), ok, nil
}

// Upsert applies fn to the chat, or to a new Chat{ChatID: chatID} when it is
// missing, and saves the result.
func (s *jsonStorage) Upsert(ctx context.Context, chatID int64, fn func(*domain.Chat)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.mutate(chatID, true, func(c *domain.Chat) bool {
		fn(c)
		return true
	})
	return err
}

// Update applies fn to the chat and saves the result when fn returns true. It
// reports whether the chat exists; a missing chat is neither passed to fn nor
// created, so a removed chat is never brought back.
func (s *jsonStorage) Update(ctx context.Context, chatID int64, fn func(*domain.Chat) bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return s.mutate(chatID, false, fn)
}

// mutate runs fn on a clone of the chat under the lock. When fn returns true
// it forces ChatID to the key, stores a clone in memory, then saves the file.
// A missing chat is created only when create is set.
func (s *jsonStorage) mutate(chatID int64, create bool, fn func(*domain.Chat) bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Chats[key(chatID)]
	if !ok && !create {
		return false, nil
	}
	if !ok {
		c = domain.Chat{ChatID: chatID}
	}
	c = clone(c)
	if !fn(&c) {
		return true, nil
	}
	c.ChatID = chatID
	s.data.Chats[key(chatID)] = clone(c)
	return true, s.save()
}

func (s *jsonStorage) Delete(ctx context.Context, chatID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Chats, key(chatID))
	return s.save()
}

// All returns every chat sorted by ChatID.
func (s *jsonStorage) All(ctx context.Context) ([]domain.Chat, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chats := make([]domain.Chat, 0, len(s.data.Chats))
	for _, c := range s.data.Chats {
		chats = append(chats, clone(c))
	}
	slices.SortFunc(chats, func(a, b domain.Chat) int { return cmp.Compare(a.ChatID, b.ChatID) })
	return chats, nil
}
