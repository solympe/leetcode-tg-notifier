package storage

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strconv"
	"sync"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// jsonFile is the on-disk format, {"chats":{"<id>":Chat}}.
type jsonFile struct {
	Chats map[string]domain.Chat `json:"chats"`
}

// jsonStorage keeps every chat in memory and rewrites the whole file on each
// change under one mutex. A done ctx is refused before locking; a started
// write always completes.
type jsonStorage struct {
	mu   sync.Mutex
	path string
	data jsonFile
}

func NewJSONStorage(path string) (*jsonStorage, error) {
	s := &jsonStorage{path: path, data: jsonFile{Chats: make(map[string]domain.Chat)}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load storage: %w", err)
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		log.Printf("storage: unmarshal error, starting fresh: %v", err)
		s.data = jsonFile{Chats: make(map[string]domain.Chat)}
	}
	return s, nil
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

// Upsert applies fn to the chat, or to a new Chat{ChatID: chatID}, and saves.
func (s *jsonStorage) Upsert(ctx context.Context, chatID int64, fn func(*domain.Chat)) error {
	_, err := s.mutate(ctx, chatID, true, func(c *domain.Chat) bool { fn(c); return true })
	return err
}

// Update applies fn to an existing chat and saves when fn returns true. A
// missing chat is neither passed to fn nor created.
func (s *jsonStorage) Update(ctx context.Context, chatID int64, fn func(*domain.Chat) bool) (bool, error) {
	return s.mutate(ctx, chatID, false, fn)
}

func (s *jsonStorage) mutate(ctx context.Context, chatID int64, create bool, fn func(*domain.Chat) bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
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
