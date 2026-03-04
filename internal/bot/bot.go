package bot

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/scheduler"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

// TelegramSender is the subset of *tgbotapi.BotAPI used by Bot.
// *tgbotapi.BotAPI satisfies this interface without any adapter.
type TelegramSender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
	GetUpdatesChan(config tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel
}

type Bot struct {
	api     TelegramSender
	botName string
	store   storage.Storage
	lc      leetcode.Client
	sched   scheduler.Scheduler
	states  *stateStore
}

func New(api TelegramSender, botName string, store storage.Storage, lc leetcode.Client, sched scheduler.Scheduler) *Bot {
	return &Bot{
		api:     api,
		botName: botName,
		store:   store,
		lc:      lc,
		sched:   sched,
		states:  newStateStore(),
	}
}

func (b *Bot) SetScheduler(sched scheduler.Scheduler) {
	b.sched = sched
}

func (b *Bot) Run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)
	for update := range updates {
		b.handleMessage(update)
	}
}
