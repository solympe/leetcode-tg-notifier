package bot

import (
	"testing"
	"time"
)

func TestChatLocks(t *testing.T) {
	tests := []struct {
		name        string
		held        int64 // chat locked first, and held
		next        int64 // chat locked while held is held
		wantBlocked bool
	}{
		{name: "same chat waits for the holder", held: 1, next: 1, wantBlocked: true},
		{name: "another chat does not wait", held: 1, next: 2, wantBlocked: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newChatLocks()
			unlockHeld := l.lock(tt.held)
			acquired := make(chan func(), 1)
			go func() { acquired <- l.lock(tt.next) }()

			// A lock that does not wait is taken at once; one that waits is
			// still not taken after a while.
			wait := 50 * time.Millisecond
			if !tt.wantBlocked {
				wait = time.Second
			}
			var unlockNext func()
			select {
			case unlockNext = <-acquired:
				if tt.wantBlocked {
					t.Error("locked while the holder still holds the lock")
				}
			case <-time.After(wait):
				if !tt.wantBlocked {
					t.Fatal("waited for the holder of another chat's lock")
				}
			}

			unlockHeld()
			if unlockNext == nil {
				select {
				case unlockNext = <-acquired:
				case <-time.After(time.Second):
					t.Fatal("not locked after the holder released the lock")
				}
			}
			unlockNext()

			l.mu.Lock()
			defer l.mu.Unlock()
			if n := len(l.locks); n != 0 {
				t.Errorf("released locks kept: got %d, want 0", n)
			}
		})
	}
}
