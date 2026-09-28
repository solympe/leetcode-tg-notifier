package bot

//go:generate mockgen -source=bot.go -destination=mocks/mock_deps.go -package=mocks

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

type telegramSender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}

type chatStore interface {
	Get(chatID int64) (storage.ChatConfig, bool)
	Set(cfg storage.ChatConfig) error
	Update(chatID int64, fn func(cfg *storage.ChatConfig) bool) (bool, error)
	Delete(chatID int64) error
}

type taskScheduler interface {
	Schedule(chatID int64, cfg storage.ChatConfig) error
	Remove(chatID int64)
}

type lcFetcher interface {
	FetchDaily() (*leetcode.Problem, error)
	FetchRandom(difficulties []string) (*leetcode.Problem, error)
}

type tgBot struct {
	api       telegramSender
	botName   string
	store     chatStore
	lc        lcFetcher
	sched     taskScheduler
	states    *stateStore
	pickLocks *chatLocks // one pick of the day at a time per chat
}

func New(api telegramSender, botName string, store chatStore, lc lcFetcher, sched taskScheduler) *tgBot {
	return &tgBot{
		api:       api,
		botName:   botName,
		store:     store,
		lc:        lc,
		sched:     sched,
		states:    newStateStore(),
		pickLocks: newChatLocks(),
	}
}

func (b *tgBot) SetScheduler(sched taskScheduler) {
	b.sched = sched
}

// Handle handles one update. The context is not used yet: the ports have no
// context parameter until the refactor threads one through.
func (b *tgBot) Handle(_ context.Context, u tgbotapi.Update) {
	b.handleMessage(u)
}
