package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

func TestHandleSetup_SetsStateAndCapturesMsgID(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	sched := &mockScheduler{}
	lc := &mockLCClient{}
	b := newTestBot(sender, store, sched, lc)

	b.handleSetup(100)

	if b.states.get(100) != stateAwaitingTime {
		t.Errorf("expected state %q, got %q", stateAwaitingTime, b.states.get(100))
	}
	if b.states.getSetupMsgID(100) != 42 {
		t.Errorf("expected msgID 42, got %d", b.states.getSetupMsgID(100))
	}
	last := sender.lastSent()
	msg, ok := last.(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("expected MessageConfig, got %T", last)
	}
	if msg.Text != msgChooseTime {
		t.Errorf("unexpected text %q", msg.Text)
	}
}

func TestHandleUnsubscribe_DeletesAndRemoves(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	sched := &mockScheduler{}
	lc := &mockLCClient{}
	b := newTestBot(sender, store, sched, lc)

	chatID := int64(100)
	_ = store.Set(storage.ChatConfig{ChatID: chatID, NotifyTime: "09:00", Timezone: "UTC"})

	b.handleUnsubscribe(chatID)

	if _, ok := store.Get(chatID); ok {
		t.Error("config should be deleted from store after unsubscribe")
	}
	if len(sched.removed) == 0 || sched.removed[0] != chatID {
		t.Error("scheduler.Remove should be called with the correct chatID")
	}
}
