package bot

import (
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
)

func (b *Bot) sendMsg(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("sendMsg to %d: %v", chatID, err)
	}
}

// sendWithKB sends a message with an inline keyboard and returns the sent message.
// The caller is responsible for handling the returned error.
func (b *Bot) sendWithKB(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) (tgbotapi.Message, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode
	msg.ReplyMarkup = kb
	return b.api.Send(msg)
}

func (b *Bot) editMsg(chatID int64, msgID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = parseMode
	if _, err := b.api.Send(edit); err != nil {
		log.Printf("editMsg to %d: %v", chatID, err)
	}
}

func (b *Bot) editMsgWithKB(chatID int64, msgID int, text string, kb tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = parseMode
	edit.ReplyMarkup = &kb
	if _, err := b.api.Send(edit); err != nil {
		log.Printf("editMsgWithKB to %d: %v", chatID, err)
	}
}

func (b *Bot) SendDailyProblem(chatID int64) {
	p, err := b.lc.FetchDaily()
	if err != nil {
		log.Printf("FetchDaily: %v", err)
		b.sendMsg(chatID, msgFetchFailed)
		return
	}
	if _, err := b.sendWithKB(chatID, leetcode.FormatProblem(p), doneKeyboard()); err != nil {
		log.Printf("SendDailyProblem to %d: %v", chatID, err)
	}
}
