package app

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/bot"
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/scheduler"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

// Config is everything the process needs from its environment. The endpoints
// exist for the integration suite; production leaves them empty.
type Config struct {
	Token            string
	StoragePath      string // "" = "config.json"
	TelegramEndpoint string // tgbotapi format ".../bot%s/%s"; "" = tgbotapi.APIEndpoint
	LeetCodeEndpoint string // "" = https://leetcode.com/graphql
}

const (
	telegramTimeout = 75 * time.Second // long poll is 60s
	pollTimeout     = 60               // seconds, as today
	updateTimeout   = 2 * time.Minute  // per-update budget
)

type jobRunner interface { // a.sched; RunNow and Next are used only by the white-box integration suite
	Start()
	Stop()
	RunNow(chatID int64) bool
	Next(chatID int64) (time.Time, bool)
}

type updateHandler interface { // a.bot
	Handle(ctx context.Context, u tgbotapi.Update)
}

type application struct {
	api   *tgbotapi.BotAPI // concrete third-party type: GetUpdatesChan, StopReceivingUpdates, GetUpdates
	sched jobRunner
	bot   updateHandler
}

// New builds the object graph and restores every stored schedule. It calls
// getMe, so it fails on a bad token or an unreachable Telegram endpoint.
func New(_ context.Context, cfg Config) (*application, error) {
	store, err := storage.NewJSONStorage(cmp.Or(cfg.StoragePath, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	api, err := tgbotapi.NewBotAPIWithClient(cfg.Token, cmp.Or(cfg.TelegramEndpoint, tgbotapi.APIEndpoint),
		&http.Client{Timeout: telegramTimeout})
	if err != nil {
		return nil, fmt.Errorf("NewBotAPI: %w", err)
	}
	log.Printf("Authorized as @%s", api.Self.UserName)

	b := bot.New(api, api.Self.UserName, store, leetcode.NewHTTPClient(cfg.LeetCodeEndpoint, nil), nil)
	sched := scheduler.NewCronScheduler(b.SendDailyProblem)
	b.SetScheduler(sched)
	for _, c := range store.All() {
		if err := sched.Schedule(c.ChatID, c); err != nil {
			log.Printf("restore schedule for %d: %v", c.ChatID, err)
		}
	}
	return &application{api: api, sched: sched, bot: b}, nil
}

// Run handles updates one at a time until ctx is done, then shuts down
// gracefully: it drains every update already received, confirms the last
// batch and waits for running jobs. Call it once per application.
func (a *application) Run(ctx context.Context) {
	a.sched.Start()
	defer a.sched.Stop() // 4. no new firings; waits for running jobs
	updates := a.api.GetUpdatesChan(tgbotapi.UpdateConfig{Timeout: pollTimeout})
	stop := context.AfterFunc(ctx, a.api.StopReceivingUpdates) // 1. exactly once (a second call panics)
	defer stop()
	base := context.WithoutCancel(ctx) // started work must survive SIGTERM
	last := 0
	for u := range updates { // 2. drains buffered updates and the last in-flight poll
		a.handle(base, u)
		last = u.UpdateID
	}
	if last > 0 { // 3. confirm the final batch so the next start does not redeliver it
		if _, err := a.api.GetUpdates(tgbotapi.UpdateConfig{Offset: last + 1, Limit: 1}); err != nil {
			log.Printf("confirm updates: %v", err)
		}
	}
}

func (a *application) handle(base context.Context, u tgbotapi.Update) {
	ctx, cancel := context.WithTimeout(base, updateTimeout)
	defer cancel()
	a.bot.Handle(ctx, u)
}
