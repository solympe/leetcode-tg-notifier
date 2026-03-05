package bot

import (
	"fmt"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

func makeCallbackQuery(cbID string, userID int64, firstName string, chatID int64) *tgbotapi.CallbackQuery {
	return &tgbotapi.CallbackQuery{
		ID:   cbID,
		From: &tgbotapi.User{ID: userID, FirstName: firstName},
		Message: &tgbotapi.Message{
			MessageID: 1,
			Chat:      &tgbotapi.Chat{ID: chatID},
		},
		Data: cbCmdDone,
	}
}

func lastCallbackText(sender *mockSender) string {
	last := sender.lastSent()
	if cb, ok := last.(tgbotapi.CallbackConfig); ok {
		return cb.Text
	}
	return ""
}

func TestHandleDone_FirstClickCounts(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	b := newTestBot(sender, store, &mockScheduler{}, &mockLCClient{})

	chatID := int64(100)
	_ = store.Set(storage.ChatConfig{ChatID: chatID, NotifyTime: "09:00", Timezone: "UTC"})

	cb := makeCallbackQuery("cb1", 1, "Alice", chatID)
	b.handleDone(cb, chatID, 1)

	cfg, _ := store.Get(chatID)
	stat := cfg.Members["1"]
	if stat.Count != 1 {
		t.Errorf("expected count 1, got %d", stat.Count)
	}
	if stat.Name != "Alice" {
		t.Errorf("expected name Alice, got %q", stat.Name)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if stat.LastSolvedDate != today {
		t.Errorf("expected LastSolvedDate %q, got %q", today, stat.LastSolvedDate)
	}
	if text := lastCallbackText(sender); text != fmt.Sprintf("✅ Counted! Your total: %d", 1) {
		t.Errorf("unexpected callback answer: %q", text)
	}
}

func TestHandleDone_SecondClickSameDayRejected(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	b := newTestBot(sender, store, &mockScheduler{}, &mockLCClient{})

	today := time.Now().UTC().Format("2006-01-02")
	chatID := int64(100)
	_ = store.Set(storage.ChatConfig{
		ChatID:     chatID,
		NotifyTime: "09:00",
		Timezone:   "UTC",
		Members: map[string]storage.UserStat{
			"1": {Name: "Alice", Count: 3, LastSolvedDate: today},
		},
	})

	cb := makeCallbackQuery("cb2", 1, "Alice", chatID)
	b.handleDone(cb, chatID, 1)

	cfg, _ := store.Get(chatID)
	if cfg.Members["1"].Count != 3 {
		t.Errorf("count should not change, got %d", cfg.Members["1"].Count)
	}
	if text := lastCallbackText(sender); text != "Already counted today!" {
		t.Errorf("unexpected callback answer: %q", text)
	}
}

func TestHandleDone_NewDayAllowsCount(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	b := newTestBot(sender, store, &mockScheduler{}, &mockLCClient{})

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	chatID := int64(100)
	_ = store.Set(storage.ChatConfig{
		ChatID:     chatID,
		NotifyTime: "09:00",
		Timezone:   "UTC",
		Members: map[string]storage.UserStat{
			"1": {Name: "Alice", Count: 5, LastSolvedDate: yesterday},
		},
	})

	cb := makeCallbackQuery("cb3", 1, "Alice", chatID)
	b.handleDone(cb, chatID, 1)

	cfg, _ := store.Get(chatID)
	if cfg.Members["1"].Count != 6 {
		t.Errorf("expected count 6, got %d", cfg.Members["1"].Count)
	}
}

func TestHandleDone_NoConfig_AnswersNotSubscribed(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	b := newTestBot(sender, store, &mockScheduler{}, &mockLCClient{})

	cb := makeCallbackQuery("cb4", 1, "Alice", 999)
	b.handleDone(cb, 999, 1)

	if text := lastCallbackText(sender); text != msgNotSubscribed {
		t.Errorf("expected %q, got %q", msgNotSubscribed, text)
	}
}

func TestHandleRating_Empty(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	b := newTestBot(sender, store, &mockScheduler{}, &mockLCClient{})

	chatID := int64(100)
	_ = store.Set(storage.ChatConfig{ChatID: chatID, NotifyTime: "09:00", Timezone: "UTC"})

	b.handleRating(chatID)

	last := sender.lastSent()
	msg, ok := last.(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("expected MessageConfig, got %T", last)
	}
	if msg.Text != msgRatingEmpty {
		t.Errorf("expected empty rating message, got %q", msg.Text)
	}
}

func TestHandleRating_SingleUser_NoName(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	b := newTestBot(sender, store, &mockScheduler{}, &mockLCClient{})

	chatID := int64(100)
	_ = store.Set(storage.ChatConfig{
		ChatID:     chatID,
		NotifyTime: "09:00",
		Timezone:   "UTC",
		Members: map[string]storage.UserStat{
			"1": {Name: "Alice", Count: 7},
		},
	})

	b.handleRating(chatID)

	last := sender.lastSent()
	msg, ok := last.(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("expected MessageConfig, got %T", last)
	}
	if findPos(msg.Text, "Alice") != -1 {
		t.Errorf("single-user rating should not contain name, got %q", msg.Text)
	}
	if findPos(msg.Text, "7") == -1 {
		t.Errorf("single-user rating should contain count, got %q", msg.Text)
	}
}

func TestHandleRating_SortedByCount(t *testing.T) {
	sender := newMockSender()
	store := newMockStorage()
	b := newTestBot(sender, store, &mockScheduler{}, &mockLCClient{})

	chatID := int64(100)
	_ = store.Set(storage.ChatConfig{
		ChatID:     chatID,
		NotifyTime: "09:00",
		Timezone:   "UTC",
		Members: map[string]storage.UserStat{
			"1": {Name: "Alice", Count: 5},
			"2": {Name: "Bob", Count: 12},
			"3": {Name: "Charlie", Count: 3},
		},
	})

	b.handleRating(chatID)

	last := sender.lastSent()
	msg, ok := last.(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("expected MessageConfig, got %T", last)
	}

	text := msg.Text
	bobPos := findPos(text, "Bob")
	alicePos := findPos(text, "Alice")
	charliePos := findPos(text, "Charlie")

	if bobPos > alicePos || alicePos > charliePos {
		t.Errorf("rating not sorted by count: %q", text)
	}
}

func findPos(s, substr string) int {
	for i := range s {
		if i+len(substr) <= len(s) && s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
