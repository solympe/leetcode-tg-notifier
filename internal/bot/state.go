package bot

import (
	"slices"
	"sync"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
)

type stateStore struct {
	mu                  sync.Mutex
	states              map[int64]string   // stateAwaiting* | stateEditingDifficulty
	pendingTime         map[int64]string   // chatID → "HH:MM"
	pendingTz           map[int64]string   // chatID → IANA timezone
	pendingDifficulties map[int64][]string // chatID → selected difficulties, canonical order
	setupMsgID          map[int64]int      // chatID → message ID of the active setup message
}

// pendingSetup is what a chat has chosen so far in /setup or /difficulty.
type pendingSetup struct {
	notifyTime   string
	timezone     string
	difficulties []string
}

func newStateStore() *stateStore {
	return &stateStore{
		states:              make(map[int64]string),
		pendingTime:         make(map[int64]string),
		pendingTz:           make(map[int64]string),
		pendingDifficulties: make(map[int64][]string),
		setupMsgID:          make(map[int64]int),
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

func (s *stateStore) getPendingTime(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingTime[chatID]
}

func (s *stateStore) setPendingTz(chatID int64, tz string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingTz[chatID] = tz
}

func (s *stateStore) setPendingDifficulties(chatID int64, ds []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingDifficulties[chatID] = canonicalDifficulties(ds)
}

func (s *stateStore) getPendingDifficulties(chatID int64) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.pendingDifficulties[chatID])
}

// toggleDifficulty adds d to the pending selection or removes it, and returns
// the new selection.
func (s *stateStore) toggleDifficulty(chatID int64, d string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	selected := slices.Clone(s.pendingDifficulties[chatID])
	if i := slices.Index(selected, d); i >= 0 {
		selected = slices.Delete(selected, i, i+1)
	} else {
		selected = append(selected, d)
	}
	s.pendingDifficulties[chatID] = canonicalDifficulties(selected)
	return slices.Clone(s.pendingDifficulties[chatID])
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

// clearAndGetPending drops all per-chat state and returns what was pending.
func (s *stateStore) clearAndGetPending(chatID int64) pendingSetup {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := pendingSetup{
		notifyTime:   s.pendingTime[chatID],
		timezone:     s.pendingTz[chatID],
		difficulties: slices.Clone(s.pendingDifficulties[chatID]),
	}
	delete(s.states, chatID)
	delete(s.pendingTime, chatID)
	delete(s.pendingTz, chatID)
	delete(s.pendingDifficulties, chatID)
	delete(s.setupMsgID, chatID)
	return p
}

// canonicalDifficulties returns the known difficulties in ds, in canonical
// order, as a new slice.
func canonicalDifficulties(ds []string) []string {
	return slices.DeleteFunc(leetcode.AllDifficulties(), func(d string) bool { return !slices.Contains(ds, d) })
}
