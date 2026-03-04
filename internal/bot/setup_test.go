package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestSetupFlow_EndToEnd(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	sched := &mockScheduler{}
	lc := &mockLCClient{}
	b := newTestBot(sender, store, sched, lc)

	chatID := int64(100)

	// Step 1: /setup — sends time-picker message, stores msgID
	b.handleSetup(chatID)
	if b.states.get(chatID) != stateAwaitingTime {
		t.Fatal("expected stateAwaitingTime after handleSetup")
	}

	// Step 2: valid time input — transitions to timezone step
	b.handleAwaitingTime(chatID, "09:00")
	if b.states.get(chatID) != stateAwaitingTimezone {
		t.Fatal("expected stateAwaitingTimezone after valid time")
	}

	// Step 3: valid timezone — finishes setup
	b.handleAwaitingTimezone(chatID, "UTC")

	// State should be fully cleared
	if b.states.get(chatID) != "" {
		t.Errorf("state should be cleared after setup, got %q", b.states.get(chatID))
	}

	// Config should be persisted
	cfg, ok := store.Get(chatID)
	if !ok {
		t.Fatal("config should be stored after setup")
	}
	if cfg.NotifyTime != "09:00" {
		t.Errorf("NotifyTime: got %q, want %q", cfg.NotifyTime, "09:00")
	}
	if cfg.Timezone != "UTC" {
		t.Errorf("Timezone: got %q, want %q", cfg.Timezone, "UTC")
	}

	// Scheduler should have been called
	if len(sched.scheduled) == 0 {
		t.Error("scheduler.Schedule should be called during setup")
	}
}

func TestHandleAwaitingTime_InvalidFormat(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	sched := &mockScheduler{}
	lc := &mockLCClient{}
	b := newTestBot(sender, store, sched, lc)

	chatID := int64(100)
	b.states.set(chatID, stateAwaitingTime)
	b.states.setSetupMsgID(chatID, 10)

	b.handleAwaitingTime(chatID, "9am")

	// State should remain unchanged
	if b.states.get(chatID) != stateAwaitingTime {
		t.Errorf("state should remain %q, got %q", stateAwaitingTime, b.states.get(chatID))
	}

	// Should have sent an edit with the error message and keyboard
	last := sender.lastSent()
	edit, ok := last.(tgbotapi.EditMessageTextConfig)
	if !ok {
		t.Fatalf("expected EditMessageTextConfig, got %T", last)
	}
	if edit.Text != msgInvalidTime {
		t.Errorf("unexpected error text %q", edit.Text)
	}
	if edit.ReplyMarkup == nil {
		t.Error("time keyboard should be re-attached on invalid input")
	}
}

func TestHandleAwaitingTimezone_Invalid(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	sched := &mockScheduler{}
	lc := &mockLCClient{}
	b := newTestBot(sender, store, sched, lc)

	chatID := int64(100)
	b.states.set(chatID, stateAwaitingTimezone)
	b.states.setPendingTime(chatID, "09:00")
	b.states.setSetupMsgID(chatID, 10)

	b.handleAwaitingTimezone(chatID, "Not/ATimezone")

	// State should remain unchanged
	if b.states.get(chatID) != stateAwaitingTimezone {
		t.Errorf("state should remain %q, got %q", stateAwaitingTimezone, b.states.get(chatID))
	}

	// Should have sent an edit with the error message and timezone keyboard
	last := sender.lastSent()
	edit, ok := last.(tgbotapi.EditMessageTextConfig)
	if !ok {
		t.Fatalf("expected EditMessageTextConfig, got %T", last)
	}
	if edit.Text != msgInvalidTz {
		t.Errorf("unexpected error text %q", edit.Text)
	}
	if edit.ReplyMarkup == nil {
		t.Error("timezone keyboard should be re-attached on invalid input")
	}
}
