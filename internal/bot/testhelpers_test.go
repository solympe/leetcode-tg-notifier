package bot

import (
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

// mockSender captures outgoing Chattable values for assertion.
type mockSender struct {
	mu      sync.Mutex
	sent    []tgbotapi.Chattable
	updates chan tgbotapi.Update
}

func newMockSender() *mockSender {
	return &mockSender{updates: make(chan tgbotapi.Update, 10)}
}

func (m *mockSender) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, c)
	return tgbotapi.Message{MessageID: 42}, nil
}

func (m *mockSender) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, c)
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (m *mockSender) GetUpdatesChan(_ tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel {
	return m.updates
}

func (m *mockSender) lastSent() tgbotapi.Chattable {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		return nil
	}
	return m.sent[len(m.sent)-1]
}

// mockStorage is an in-memory Storage for tests.
type mockStorage struct {
	mu      sync.Mutex
	data    map[int64]storage.ChatConfig
	deleted []int64
}

func newMockStorage() *mockStorage {
	return &mockStorage{data: make(map[int64]storage.ChatConfig)}
}

func (s *mockStorage) Get(chatID int64) (storage.ChatConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ok := s.data[chatID]
	return cfg, ok
}

func (s *mockStorage) Set(cfg storage.ChatConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[cfg.ChatID] = cfg
	return nil
}

func (s *mockStorage) Delete(chatID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, chatID)
	s.deleted = append(s.deleted, chatID)
	return nil
}

func (s *mockStorage) All() []storage.ChatConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]storage.ChatConfig, 0, len(s.data))
	for _, cfg := range s.data {
		result = append(result, cfg)
	}
	return result
}

// mockScheduler records Schedule/Remove calls.
type mockScheduler struct {
	mu        sync.Mutex
	scheduled []storage.ChatConfig
	removed   []int64
}

func (s *mockScheduler) Schedule(_ int64, cfg storage.ChatConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduled = append(s.scheduled, cfg)
	return nil
}

func (s *mockScheduler) Remove(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, chatID)
}

// mockLCClient returns a fixed problem or error.
type mockLCClient struct {
	problem *leetcode.Problem
	err     error
}

func (m *mockLCClient) FetchDaily() (*leetcode.Problem, error) {
	return m.problem, m.err
}

func newTestBot(sender *mockSender, store *mockStorage, sched *mockScheduler, lc *mockLCClient) *tgBot {
	return New(sender, "TestBot", store, lc, sched).(*tgBot)
}
