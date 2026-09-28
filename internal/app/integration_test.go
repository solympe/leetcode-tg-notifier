//go:build integration

package app

import (
	"os"
	"testing"
	"time"
)

// TestMain starts the suite early in a minute. The suite takes a few seconds,
// so it never crosses a cron minute boundary and real cron never fires a
// schedule mid-test: every firing goes through fire().
func TestMain(m *testing.M) {
	if s := time.Now().Second(); s > 45 {
		time.Sleep(time.Duration(61-s) * time.Second)
	}
	os.Exit(m.Run())
}

// Expected texts: literal copies of today's messages.
const (
	welcomeText = "Hi! I'm <b>TestBot</b>. I help you subscribe to a daily LeetCode challenge newsletter and get the problem of the day anytime.\n\nCommands:\n• /setup — create your daily subscription\n• /difficulty — choose problem difficulty\n• /today — get today's problem (your difficulty)\n• /daily — get the official LeetCode daily (any difficulty)\n• /rating — show solve leaderboard\n• /status — check your subscription status\n• /about — learn more about this bot"
	aboutText   = "For questions, suggestions, and bug reports — DM @solympe"
)

// Expected keyboards.
var startKB = keyboard{
	{{"📅 Today's problem", "/today"}, {"🗓 LeetCode daily", "/daily"}, {"⚙️ Setup", "/setup"}},
	{{"ℹ️ Status", "/status"}, {"🏆 Rating", "/rating"}},
	{{"🎚 Difficulty", "/difficulty"}, {"🛑 Unsubscribe", "/unsubscribe"}},
}

func TestIntegration(t *testing.T) {
	tests := []struct {
		name string
		run  func(e *env)
	}{
		{
			name: "01 menu",
			run: func(e *env) {
				e.say(100, alice, "/start")
				e.sent(100, welcomeText, startKB)
				e.say(100, alice, "/start@TestBot")
				e.sent(100, welcomeText, startKB)
				e.say(100, alice, "/about")
				e.sent(100, aboutText, nil)
				e.say(100, alice, "hello")
				e.expectQuiet(100)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.start()
			tt.run(e)
		})
	}
}
