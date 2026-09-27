package bot

import (
	"slices"
	"testing"
)

func TestStateStore_ClearAndGetPending(t *testing.T) {
	tests := []struct {
		name   string
		before func(s *stateStore, chatID int64)
		want   pendingSetup
	}{
		{
			name: "returns the whole pending setup",
			before: func(s *stateStore, chatID int64) {
				s.set(chatID, stateAwaitingDifficulty)
				s.setPendingTime(chatID, "09:00")
				s.setPendingTz(chatID, "UTC")
				s.setPendingDifficulties(chatID, []string{"Easy", "Hard"})
				s.setSetupMsgID(chatID, 42)
			},
			want: pendingSetup{notifyTime: "09:00", timezone: "UTC", difficulties: []string{"Easy", "Hard"}},
		},
		{
			name: "time only",
			before: func(s *stateStore, chatID int64) {
				s.set(chatID, stateAwaitingTimezone)
				s.setPendingTime(chatID, "09:00")
				s.setSetupMsgID(chatID, 42)
			},
			want: pendingSetup{notifyTime: "09:00"},
		},
		{
			name:   "no state returns zero value",
			before: func(*stateStore, int64) {},
			want:   pendingSetup{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStateStore()
			chatID := int64(1)
			tt.before(s, chatID)

			if got := s.clearAndGetPending(chatID); !samePending(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
			if got := s.get(chatID); got != "" {
				t.Errorf("state should be cleared, got %q", got)
			}
			if got := s.getSetupMsgID(chatID); got != 0 {
				t.Errorf("setupMsgID should be cleared, got %d", got)
			}
			if got := s.getPendingTime(chatID); got != "" {
				t.Errorf("pending time should be cleared, got %q", got)
			}
			if got := s.getPendingDifficulties(chatID); got != nil {
				t.Errorf("pending difficulties should be cleared, got %v", got)
			}
			if got := s.clearAndGetPending(chatID); !samePending(got, pendingSetup{}) {
				t.Errorf("second clear should return zero value, got %+v", got)
			}
		})
	}
}

func TestStateStore_ToggleDifficulty(t *testing.T) {
	tests := []struct {
		name    string
		initial []string
		toggles []string
		want    []string
	}{
		{
			name:    "adds to empty selection",
			toggles: []string{"Medium"},
			want:    []string{"Medium"},
		},
		{
			name:    "removes a selected difficulty",
			initial: []string{"Easy", "Medium", "Hard"},
			toggles: []string{"Medium"},
			want:    []string{"Easy", "Hard"},
		},
		{
			name:    "keeps canonical order regardless of toggle order",
			initial: []string{"Hard"},
			toggles: []string{"Medium", "Easy"},
			want:    []string{"Easy", "Medium", "Hard"},
		},
		{
			name:    "canonicalizes the initial selection",
			initial: []string{"Hard", "Easy"},
			toggles: []string{"Medium", "Medium"},
			want:    []string{"Easy", "Hard"},
		},
		{
			name:    "removing the last one leaves nothing selected",
			initial: []string{"Easy"},
			toggles: []string{"Easy"},
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStateStore()
			chatID := int64(1)
			if tt.initial != nil {
				s.setPendingDifficulties(chatID, tt.initial)
			}

			var got []string
			for _, d := range tt.toggles {
				got = s.toggleDifficulty(chatID, d)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("toggle returned %v, want %v", got, tt.want)
			}
			if stored := s.getPendingDifficulties(chatID); !slices.Equal(stored, tt.want) {
				t.Errorf("stored %v, want %v", stored, tt.want)
			}
		})
	}
}

func TestStateStore_DifficultiesAreCopies(t *testing.T) {
	tests := []struct {
		name string
		// leak returns a slice obtained from (or handed to) the store; the
		// test mutates it and checks the stored selection is unaffected.
		leak func(s *stateStore, chatID int64) []string
	}{
		{
			name: "setPendingDifficulties copies its argument",
			leak: func(s *stateStore, chatID int64) []string {
				in := []string{"Easy", "Medium"}
				s.setPendingDifficulties(chatID, in)
				return in
			},
		},
		{
			name: "getPendingDifficulties returns a copy",
			leak: func(s *stateStore, chatID int64) []string {
				s.setPendingDifficulties(chatID, []string{"Easy", "Medium"})
				return s.getPendingDifficulties(chatID)
			},
		},
		{
			name: "toggleDifficulty returns a copy",
			leak: func(s *stateStore, chatID int64) []string {
				s.setPendingDifficulties(chatID, []string{"Easy", "Medium", "Hard"})
				return s.toggleDifficulty(chatID, "Hard")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStateStore()
			chatID := int64(1)

			leaked := tt.leak(s, chatID)
			leaked[0] = "Mutated"

			want := []string{"Easy", "Medium"}
			if got := s.getPendingDifficulties(chatID); !slices.Equal(got, want) {
				t.Errorf("stored selection changed through a leaked slice: got %v, want %v", got, want)
			}
		})
	}
}
