package main

import (
	"log"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/bot"
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/scheduler"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

func main() {
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("BOT_TOKEN environment variable is not set")
	}

	storagePath := os.Getenv("STORAGE_PATH")
	if storagePath == "" {
		storagePath = "config.json"
	}

	store, err := storage.NewJSONStorage(storagePath)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("NewBotAPI: %v", err)
	}
	log.Printf("Authorized as @%s", api.Self.UserName)

	b := bot.New(api, api.Self.UserName, store, leetcode.NewHTTPClient(nil), nil)
	sched := scheduler.NewCronScheduler(b.SendDailyProblem)
	b.SetScheduler(sched)

	for _, cfg := range store.All() {
		if err := sched.Schedule(cfg.ChatID, cfg); err != nil {
			log.Printf("restore schedule for %d: %v", cfg.ChatID, err)
		}
	}

	b.Run()
}
