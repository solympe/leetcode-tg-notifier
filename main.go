package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/solympe/leetcode-tg-notifier/internal/app"
)

func main() {
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("BOT_TOKEN environment variable is not set")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, func() {
		stop() // a second signal gets the default behaviour and kills the process
		log.Print("shutting down; signal again to force")
	})
	a, err := app.New(ctx, app.Config{Token: token, StoragePath: os.Getenv("STORAGE_PATH")})
	if err != nil {
		log.Fatal(err) // app.New wraps as "storage: …" / "NewBotAPI: …", matching today's log lines
	}
	a.Run(ctx)
}
