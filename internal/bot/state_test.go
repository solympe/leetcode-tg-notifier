package bot

import "testing"

func TestStateStore_ClearAndGetPending(t *testing.T) {
	s := newStateStore()
	chatID := int64(1)

	s.set(chatID, stateAwaitingTimezone)
	s.setPendingTime(chatID, "09:00")
	s.setSetupMsgID(chatID, 42)

	got := s.clearAndGetPending(chatID)
	if got != "09:00" {
		t.Errorf("got %q, want %q", got, "09:00")
	}
	if s.get(chatID) != "" {
		t.Error("state should be cleared after clearAndGetPending")
	}
	if s.getSetupMsgID(chatID) != 0 {
		t.Errorf("setupMsgID should be cleared, got %d", s.getSetupMsgID(chatID))
	}
}
