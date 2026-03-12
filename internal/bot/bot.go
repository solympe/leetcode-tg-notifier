package bot

//go:generate mockgen -source=bot.go -destination=mocks/mock_deps.go -package=mocks

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

type telegramSender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
	GetUpdatesChan(config tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel
}

type chatStore interface {
	Get(chatID int64) (storage.ChatConfig, bool)
	Set(cfg storage.ChatConfig) error
	Delete(chatID int64) error
}

type taskScheduler interface {
	Schedule(chatID int64, cfg storage.ChatConfig) error
	Remove(chatID int64)
}

type lcFetcher interface {
	FetchDaily() (*leetcode.Problem, error)
}

type tgBot struct {
	api     telegramSender
	botName string
	store   chatStore
	lc      lcFetcher
	sched   taskScheduler
	states  *stateStore
}

func New(api telegramSender, botName string, store chatStore, lc lcFetcher, sched taskScheduler) *tgBot {
	return &tgBot{
		api:     api,
		botName: botName,
		store:   store,
		lc:      lc,
		sched:   sched,
		states:  newStateStore(),
	}
}

func (b *tgBot) SetScheduler(sched taskScheduler) {
	b.sched = sched
}

func (b *tgBot) Run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)
	for update := range updates {
		b.handleMessage(update)
	}
}
