package bot

import "sync"

// chatLocks serialises work per chat without blocking other chats. A chat's
// lock exists only while it is held or waited for.
type chatLocks struct {
	mu    sync.Mutex
	locks map[int64]*chatLock
}

type chatLock struct {
	mu   sync.Mutex
	refs int // holder and waiters; guarded by chatLocks.mu
}

func newChatLocks() *chatLocks {
	return &chatLocks{locks: make(map[int64]*chatLock)}
}

// lock waits until chatID's lock is free, takes it and returns its release.
func (l *chatLocks) lock(chatID int64) (unlock func()) {
	l.mu.Lock()
	cl, ok := l.locks[chatID]
	if !ok {
		cl = &chatLock{}
		l.locks[chatID] = cl
	}
	cl.refs++
	l.mu.Unlock()

	cl.mu.Lock()
	return func() {
		cl.mu.Unlock()
		l.mu.Lock()
		defer l.mu.Unlock()
		cl.refs--
		if cl.refs == 0 {
			delete(l.locks, chatID)
		}
	}
}
