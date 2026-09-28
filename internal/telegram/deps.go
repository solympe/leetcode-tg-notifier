// Package telegram is the Bot API adapter: it routes updates, runs the
// /setup and /difficulty dialogs, renders texts and keyboards and sends
// messages. Apart from app, it is the only package that imports tgbotapi.
package telegram

//go:generate mockgen -source=deps.go -destination=mocks/mock_deps.go -package=mocks

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// botAPI is the part of the Bot API the adapter calls. *tgbotapi.BotAPI
// satisfies it; tgbotapi v5.5.1 has no ctx API.
type botAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}

// service is the use-case core the handler drives, in domain types only.
// notifier's *service satisfies it.
type service interface {
	Subscribe(ctx context.Context, chatID int64, notifyTime, timezone string, difficulties []string) error
	SetDifficulties(ctx context.Context, chatID int64, difficulties []string) error
	Unsubscribe(ctx context.Context, chatID int64) error
	Subscription(ctx context.Context, chatID int64) (domain.Chat, bool, error)
	Solve(ctx context.Context, chatID, userID int64, name string) (int, error)
	SendToday(ctx context.Context, chatID int64)
	SendDaily(ctx context.Context, chatID int64)
}
