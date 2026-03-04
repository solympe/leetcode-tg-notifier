package bot

import "sync"

type stateStore struct {
	mu          sync.Mutex
	states      map[int64]string // "awaiting_time" | "awaiting_timezone"
	pendingTime map[int64]string // chatID → "HH:MM"
	setupMsgID  map[int64]int    // chatID → message ID of the active setup message
}

func newStateStore() *stateStore {
	return &stateStore{
		states:      make(map[int64]string),
		pendingTime: make(map[int64]string),
		setupMsgID:  make(map[int64]int),
	}
}

func (s *stateStore) get(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[chatID]
}

func (s *stateStore) set(chatID int64, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[chatID] = state
}

func (s *stateStore) setPendingTime(chatID int64, t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingTime[chatID] = t
}

func (s *stateStore) setSetupMsgID(chatID int64, msgID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setupMsgID[chatID] = msgID
}

func (s *stateStore) getSetupMsgID(chatID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setupMsgID[chatID]
}

func (s *stateStore) clearAndGetPending(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.pendingTime[chatID]
	delete(s.pendingTime, chatID)
	delete(s.states, chatID)
	delete(s.setupMsgID, chatID)
	return t
}
