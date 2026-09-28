package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// sender renders and sends through the Bot API. Every call refuses to start
// once ctx is done. tgbotapi v5.5.1 builds its requests without a ctx, so a
// call already in flight cannot be interrupted; it is bounded by the 75s
// http.Client timeout set in app.New.
type sender struct {
	api botAPI
}

func NewSender(api botAPI) *sender {
	return &sender{api: api}
}

// SendProblem sends p with the Done button. When the chat blocked the bot the
// error wraps both domain.ErrBlocked and the *tgbotapi.Error.
func (s *sender) SendProblem(ctx context.Context, chatID int64, p domain.Pick) error {
	_, err := s.send(ctx, chatID, formatPick(p), doneKeyboard())
	if isBotBlocked(err) {
		return fmt.Errorf("%w: %w", domain.ErrBlocked, err)
	}
	return err
}

func (s *sender) SendFetchFailed(ctx context.Context, chatID int64) error {
	_, err := s.send(ctx, chatID, msgFetchFailed, nil)
	return err
}

// send sends an HTML message, with kb when it is not nil, and returns the
// sent message's ID.
func (s *sender) send(ctx context.Context, chatID int64, text string, kb *tgbotapi.InlineKeyboardMarkup) (int, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode
	if kb != nil {
		msg.ReplyMarkup = *kb
	}
	sent, err := s.call(ctx, msg)
	return sent.MessageID, err
}

// text sends an HTML message without a keyboard, logging a failure.
func (s *sender) text(ctx context.Context, chatID int64, text string) {
	if _, err := s.send(ctx, chatID, text, nil); err != nil {
		log.Printf("send to %d: %v", chatID, err)
	}
}

// edit replaces message msgID's text; a nil kb removes its keyboard.
func (s *sender) edit(ctx context.Context, chatID int64, msgID int, text string, kb *tgbotapi.InlineKeyboardMarkup) {
	e := tgbotapi.NewEditMessageText(chatID, msgID, text)
	e.ParseMode = parseMode
	e.ReplyMarkup = kb
	if _, err := s.call(ctx, e); err != nil {
		log.Printf("edit %d in %d: %v", msgID, chatID, err)
	}
}

// editKeyboard replaces message msgID's keyboard and keeps its text.
func (s *sender) editKeyboard(ctx context.Context, chatID int64, msgID int, kb *tgbotapi.InlineKeyboardMarkup) {
	if _, err := s.call(ctx, tgbotapi.NewEditMessageReplyMarkup(chatID, msgID, *kb)); err != nil {
		log.Printf("edit keyboard %d in %d: %v", msgID, chatID, err)
	}
}

// answer answers callback cbID with a toast; "" only stops the spinner.
func (s *sender) answer(ctx context.Context, cbID, text string) {
	err := ctx.Err()
	if err == nil {
		_, err = s.api.Request(tgbotapi.NewCallback(cbID, text))
	}
	if err != nil {
		log.Printf("answer callback %s: %v", cbID, err)
	}
}

func (s *sender) call(ctx context.Context, c tgbotapi.Chattable) (tgbotapi.Message, error) {
	if err := ctx.Err(); err != nil {
		return tgbotapi.Message{}, err
	}
	return s.api.Send(c)
}

// isBotBlocked reports whether err is the Bot API's 403 Forbidden, which it
// returns when the chat blocked the bot.
func isBotBlocked(err error) bool {
	var tgErr *tgbotapi.Error
	return errors.As(err, &tgErr) && tgErr.Code == http.StatusForbidden
}
