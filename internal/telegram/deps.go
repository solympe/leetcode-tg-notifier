// Package telegram is the Bot API adapter: it routes updates, runs the
// /setup and /difficulty dialogs, renders texts and keyboards and sends
// messages. Apart from app, it is the only package that imports tgbotapi.
package telegram

//go:generate mockgen -source=deps.go -destination=mocks/mock_deps.go -package=mocks

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// botAPI is the part of the Bot API the adapter calls. *tgbotapi.BotAPI
// satisfies it; tgbotapi v5.5.1 has no ctx API.
type botAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}
