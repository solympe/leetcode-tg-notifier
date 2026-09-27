package bot

import (
	"maps"
	"slices"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

// textMsg is the message sendMsg produces.
func textMsg(chatID int64, text string) tgbotapi.MessageConfig {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode
	return msg
}

// kbMsg is the message sendWithKB produces.
func kbMsg(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) tgbotapi.MessageConfig {
	msg := textMsg(chatID, text)
	msg.ReplyMarkup = kb
	return msg
}

// editText is the edit editMsg produces.
func editText(chatID int64, msgID int, text string) tgbotapi.EditMessageTextConfig {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = parseMode
	return edit
}

// editTextKB is the edit editMsgWithKB produces.
func editTextKB(chatID int64, msgID int, text string, kb tgbotapi.InlineKeyboardMarkup) tgbotapi.EditMessageTextConfig {
	edit := editText(chatID, msgID, text)
	edit.ReplyMarkup = &kb
	return edit
}

// pressButton builds the callback query Telegram sends when a user presses
// the inline button with data on message msgID in chatID.
func pressButton(cbID string, chatID int64, msgID int, data string) *tgbotapi.CallbackQuery {
	return &tgbotapi.CallbackQuery{
		ID:   cbID,
		From: &tgbotapi.User{ID: 1, FirstName: "Alice"},
		Message: &tgbotapi.Message{
			MessageID: msgID,
			Chat:      &tgbotapi.Chat{ID: chatID},
		},
		Data: data,
	}
}

// answered is a successful answerCallbackQuery response.
var answered = &tgbotapi.APIResponse{Ok: true}

// samePending compares pending setups, treating nil and empty selections as equal.
func samePending(a, b pendingSetup) bool {
	return a.notifyTime == b.notifyTime && a.timezone == b.timezone && slices.Equal(a.difficulties, b.difficulties)
}

// updateVia plays chatStore.Update on m for a chat holding stored: fn runs on
// a copy, and a save goes through m.Set, so a Set expectation checks what
// Update saved and a missing one fails any save.
func updateVia(m *mocks.MockchatStore, stored storage.ChatConfig) func(int64, func(*storage.ChatConfig) bool) (bool, error) {
	return func(_ int64, fn func(*storage.ChatConfig) bool) (bool, error) {
		cfg := stored
		cfg.Members = maps.Clone(stored.Members)
		cfg.Difficulties = slices.Clone(stored.Difficulties)
		if !fn(&cfg) {
			return true, nil
		}
		return true, m.Set(cfg)
	}
}
