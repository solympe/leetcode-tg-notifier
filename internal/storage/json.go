package storage

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"sync"
)

type jsonFile struct {
	Chats map[string]ChatConfig `json:"chats"`
}

type jsonStorage struct {
	mu   sync.Mutex
	path string
	data jsonFile
}

func NewJSONStorage(path string) (*jsonStorage, error) {
	s := &jsonStorage{
		path: path,
		data: jsonFile{Chats: make(map[string]ChatConfig)},
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
		s.data = jsonFile{Chats: make(map[string]ChatConfig)}
	}
	return nil
}

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
	return fmt.Sprintf("%d", chatID)
}

// cloneConfig deep-copies the reference fields of cfg, so the stored configs
// and the ones callers hold never share memory; nil fields stay nil.
func cloneConfig(cfg ChatConfig) ChatConfig {
	cfg.Members = maps.Clone(cfg.Members)
	cfg.Difficulties = slices.Clone(cfg.Difficulties)
	if cfg.DailyPick != nil {
		pick := *cfg.DailyPick
		pick.Tags = slices.Clone(pick.Tags)
		cfg.DailyPick = &pick
	}
	return cfg
}

func (s *jsonStorage) Get(chatID int64) (ChatConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ok := s.data.Chats[key(chatID)]
	return cloneConfig(cfg), ok
}

func (s *jsonStorage) Set(cfg ChatConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Chats[key(cfg.ChatID)] = cloneConfig(cfg)
	return s.save()
}

// Update applies fn to the chat's config and saves the result if fn returns
// true, all under one lock, so concurrent updates of a chat never overwrite
// each other. It reports whether the chat exists; a missing one is neither
// passed to fn nor created.
func (s *jsonStorage) Update(chatID int64, fn func(cfg *ChatConfig) bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ok := s.data.Chats[key(chatID)]
	if !ok {
		return false, nil
	}
	cfg = cloneConfig(cfg)
	if !fn(&cfg) {
		return true, nil
	}
	s.data.Chats[key(chatID)] = cloneConfig(cfg)
	return true, s.save()
}

func (s *jsonStorage) Delete(chatID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Chats, key(chatID))
	return s.save()
}

func (s *jsonStorage) All() []ChatConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]ChatConfig, 0, len(s.data.Chats))
	for _, cfg := range s.data.Chats {
		result = append(result, cloneConfig(cfg))
	}
	return result
}
