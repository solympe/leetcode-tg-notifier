//go:build integration

package app

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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
	welcomeText          = "Hi! I'm <b>TestBot</b>. I help you subscribe to a daily LeetCode challenge newsletter and get the problem of the day anytime.\n\nCommands:\n• /setup — create your daily subscription\n• /difficulty — choose problem difficulty\n• /today — get today's problem (your difficulty)\n• /daily — get the official LeetCode daily (any difficulty)\n• /rating — show solve leaderboard\n• /status — check your subscription status\n• /about — learn more about this bot"
	aboutText            = "For questions, suggestions, and bug reports — DM @solympe"
	chooseTimeText       = "Choose notification time or type your own (<b>HH:MM</b>, 24h):"
	chooseDifficultyText = "Choose difficulty (tap to toggle, then Save).\nIf today's daily doesn't match, I'll send a random problem of your level instead."
	invalidTimeText      = "⚠️ Invalid format. Please enter time as <b>HH:MM</b> (e.g. <code>09:00</code>):"
	invalidTzText        = "⚠️ Unknown timezone. Try again (e.g. <code>Europe/Moscow</code>):"
	pickAtLeastOneText   = "Pick at least one difficulty"
	menuExpiredText      = "⚠️ This menu is no longer active. Use /setup or /difficulty to start again."
	messageExpiredText   = "⚠️ This message is too old. Use /today to get a fresh one."
	notSubscribedText    = "⚠️ Use /setup to subscribe first."
	disabledText         = "🛑 Notifications disabled."
	fetchFailedText      = "⚠️ Failed to fetch the problem from LeetCode. Try /today later."
	statusInactiveText   = "❌ No active subscription. Use /setup to configure."

	dailyText      = "📅 LeetCode Daily — September 28, 2026\n\n🔢 4. Median of Two Sorted Arrays\n💪 Difficulty: Hard\n🏷 Array, Binary Search, Divide and Conquer\n\n🔗 https://leetcode.com/problems/median-of-two-sorted-arrays/"
	randomEasyText = "🎲 LeetCode Random — September 28, 2026\nToday's daily is Hard, so here's a random Easy problem for you.\n\n🔢 1. Two Sum\n💪 Difficulty: Easy\n🏷 Array, Hash Table\n\n🔗 https://leetcode.com/problems/two-sum/"
)

// Expected keyboards.
var (
	startKB = keyboard{
		{{"📅 Today's problem", "/today"}, {"🗓 LeetCode daily", "/daily"}, {"⚙️ Setup", "/setup"}},
		{{"ℹ️ Status", "/status"}, {"🏆 Rating", "/rating"}},
		{{"🎚 Difficulty", "/difficulty"}, {"🛑 Unsubscribe", "/unsubscribe"}},
	}
	doneKB = keyboard{{{"✅ Done", "done"}}}
	timeKB = keyboard{
		{{"7:00", "time:07:00"}, {"8:00", "time:08:00"}, {"9:00", "time:09:00"}, {"10:00", "time:10:00"}},
		{{"18:00", "time:18:00"}, {"19:00", "time:19:00"}, {"20:00", "time:20:00"}, {"21:00", "time:21:00"}},
	}
	// tzKB is compared by callback data only: its labels change with DST.
	tzKB = keyboard{
		{{"", "tz:America/New_York"}, {"", "tz:Europe/London"}},
		{{"", "tz:Europe/Lisbon"}, {"", "tz:UTC"}},
		{{"", "tz:Europe/Moscow"}, {"", "tz:Asia/Dubai"}},
		{{"", "tz:Asia/Bangkok"}},
	}
)

// diffKB is the difficulty keyboard with the given difficulties ticked.
func diffKB(ticked ...string) keyboard {
	row := make([]button, 0, 3)
	for _, d := range []string{"Easy", "Medium", "Hard"} {
		label := "⬜ " + d
		if slices.Contains(ticked, d) {
			label = "✅ " + d
		}
		row = append(row, button{label, "diff:" + d})
	}
	return keyboard{row, {{"💾 Save", "diffsave"}}}
}

// tzPrompt is the timezone step's text after hhmm was chosen.
func tzPrompt(hhmm string) string {
	return "Time: <b>" + hhmm + "</b>\n\nChoose your timezone or type it manually (e.g. <code>Europe/Moscow</code>)\nhttps://en.wikipedia.org/wiki/List_of_tz_database_time_zones"
}

// randomText is the text of a random pick replacing a daily.
func randomText(p *legacyPick) string {
	date, err := time.Parse(time.DateOnly, p.Date)
	if err != nil {
		return "unparseable pick date " + p.Date
	}
	return "🎲 LeetCode Random — " + date.Format("January 2, 2006") +
		"\nToday's daily is " + p.DailyDifficulty + ", so here's a random " + p.Difficulty + " problem for you.\n\n" +
		"🔢 " + p.ID + ". " + p.Title + "\n💪 Difficulty: " + p.Difficulty + "\n🏷 " + strings.Join(p.Tags, ", ") +
		"\n\n🔗 https://leetcode.com" + p.Link
}

// subscribe runs /setup by typing hhmm and zone and saves all three
// difficulties, asserting every reply. The chat must have no saved difficulties.
func (e *env) subscribe(chatID int64, hhmm, zone string) {
	e.t.Helper()
	e.say(chatID, alice, "/setup")
	m := e.sent(chatID, chooseTimeText, timeKB)
	e.say(chatID, alice, hhmm)
	e.edited(chatID, m, tzPrompt(hhmm), tzKB)
	e.say(chatID, alice, zone)
	e.edited(chatID, m, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
	e.answered(e.press(chatID, alice, "diffsave"), "")
	e.edited(chatID, m, "✅ All set! I'll send you the daily problem at <b>"+hhmm+"</b> ("+zone+")\nDifficulty: <b>Easy, Medium, Hard</b>", nil)
}

func today() string {
	return time.Now().UTC().Format(time.DateOnly)
}

func TestIntegration(t *testing.T) {
	tests := []struct {
		name string
		seed string // config.json written before start; "" = none
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
		{
			name: "02 subscribe with buttons",
			run: func(e *env) {
				e.say(200, alice, "/start")
				e.sent(200, welcomeText, startKB)
				e.answered(e.press(200, alice, "/setup"), "")
				m := e.sent(200, chooseTimeText, timeKB)
				e.answered(e.press(200, alice, "time:09:00"), "")
				e.edited(200, m, tzPrompt("09:00"), tzKB)
				e.answered(e.press(200, alice, "tz:Europe/Moscow"), "")
				e.edited(200, m, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
				e.answered(e.press(200, alice, "diff:Hard"), "")
				e.rekeyed(200, m, diffKB("Easy", "Medium"))
				e.answered(e.press(200, alice, "diffsave"), "")
				e.edited(200, m, "✅ All set! I'll send you the daily problem at <b>09:00</b> (Europe/Moscow)\nDifficulty: <b>Easy, Medium</b>", nil)
				e.storedEq(200, legacyChat{ChatID: 200, NotifyTime: "09:00", Timezone: "Europe/Moscow", Difficulties: []string{"Easy", "Medium"}})
				e.scheduledAt(200, "09:00", "Europe/Moscow")
			},
		},
		{
			name: "03 subscribe by typing in a group",
			run: func(e *env) {
				e.say(-100500, alice, "/setup@TestBot")
				m := e.sent(-100500, chooseTimeText, timeKB)
				e.say(-100500, alice, "9:00")
				e.edited(-100500, m, invalidTimeText, timeKB)
				e.say(-100500, alice, "21:15")
				e.edited(-100500, m, tzPrompt("21:15"), tzKB)
				e.say(-100500, alice, "Local")
				e.edited(-100500, m, invalidTzText, tzKB)
				e.say(-100500, alice, "Mars/Base")
				e.edited(-100500, m, invalidTzText, tzKB)
				e.say(-100500, alice, "Asia/Tbilisi")
				e.edited(-100500, m, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
				e.answered(e.press(-100500, alice, "diffsave"), "")
				e.edited(-100500, m, "✅ All set! I'll send you the daily problem at <b>21:15</b> (Asia/Tbilisi)\nDifficulty: <b>Easy, Medium, Hard</b>", nil)
				e.storedEq(-100500, legacyChat{ChatID: -100500, NotifyTime: "21:15", Timezone: "Asia/Tbilisi", Difficulties: []string{"Easy", "Medium", "Hard"}})
				e.scheduledAt(-100500, "21:15", "Asia/Tbilisi")
			},
		},
		{
			name: "04 stale and forged buttons",
			run: func(e *env) {
				e.say(400, alice, "/setup")
				first := e.sent(400, chooseTimeText, timeKB)
				e.say(400, alice, "/setup")
				m := e.sent(400, chooseTimeText, timeKB)
				e.answered(e.pressOn(400, alice, first, "time:09:00"), menuExpiredText)
				e.answered(e.press(400, alice, "time:09:00"), "")
				e.edited(400, m, tzPrompt("09:00"), tzKB)
				e.answered(e.pressOn(400, alice, m, "tz:Local"), menuExpiredText)
				e.answered(e.pressOn(400, alice, m, "tz:Mars/Base"), menuExpiredText)
				e.answered(e.press(400, alice, "tz:UTC"), "")
				e.edited(400, m, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
				e.answered(e.pressOn(400, alice, m, "diff:Insane"), "")
				e.answered(e.press(400, alice, "diff:Easy"), "")
				e.rekeyed(400, m, diffKB("Medium", "Hard"))
				e.answered(e.press(400, alice, "diff:Medium"), "")
				e.rekeyed(400, m, diffKB("Hard"))
				e.answered(e.press(400, alice, "diff:Hard"), "")
				e.rekeyed(400, m, diffKB())
				e.answered(e.press(400, alice, "diffsave"), pickAtLeastOneText)
				e.answered(e.press(400, alice, "diff:Easy"), "")
				e.rekeyed(400, m, diffKB("Easy"))
				e.answered(e.press(400, alice, "diffsave"), "")
				e.edited(400, m, "✅ All set! I'll send you the daily problem at <b>09:00</b> (UTC)\nDifficulty: <b>Easy</b>", nil)
				e.answered(e.pressOn(400, alice, first, "time:07:00"), menuExpiredText)
				e.answered(e.pressInline(alice, "/today"), messageExpiredText)
				e.answered(e.pressOn(400, alice, m, "diffsave"), menuExpiredText)
				e.expectQuiet(400)
				e.storedEq(400, legacyChat{ChatID: 400, NotifyTime: "09:00", Timezone: "UTC", Difficulties: []string{"Easy"}})
			},
		},
		{
			name: "05 re-running setup keeps rating and pick",
			seed: `{"chats":{"500":{"chat_id":500,"notify_time":"08:00","timezone":"UTC",` +
				`"members":{"7":{"name":"Alice","count":3,"last_solved_date":"2026-09-27"}},"difficulties":["Easy"],` +
				`"daily_pick":{"date":"2026-09-28","daily_difficulty":"Hard","id":"1","title":"Two Sum","link":"/problems/two-sum/","difficulty":"Easy","tags":["Array","Hash Table"]}}}}`,
			run: func(e *env) {
				e.scheduledAt(500, "08:00", "UTC")
				e.say(500, alice, "/setup")
				m := e.sent(500, chooseTimeText, timeKB)
				e.answered(e.press(500, alice, "time:10:00"), "")
				e.edited(500, m, tzPrompt("10:00"), tzKB)
				e.answered(e.press(500, alice, "tz:Asia/Dubai"), "")
				e.edited(500, m, chooseDifficultyText, diffKB("Easy"))
				e.answered(e.press(500, alice, "diff:Medium"), "")
				e.rekeyed(500, m, diffKB("Easy", "Medium"))
				e.answered(e.press(500, alice, "diffsave"), "")
				e.edited(500, m, "✅ All set! I'll send you the daily problem at <b>10:00</b> (Asia/Dubai)\nDifficulty: <b>Easy, Medium</b>", nil)
				e.storedEq(500, legacyChat{
					ChatID:       500,
					NotifyTime:   "10:00",
					Timezone:     "Asia/Dubai",
					Members:      map[string]legacyStat{"7": {Name: "Alice", Count: 3, LastSolvedDate: "2026-09-27"}},
					Difficulties: []string{"Easy", "Medium"},
					DailyPick: &legacyPick{
						Date: "2026-09-28", DailyDifficulty: "Hard", ID: "1", Title: "Two Sum",
						Link: "/problems/two-sum/", Difficulty: "Easy", Tags: []string{"Array", "Hash Table"},
					},
				})
				e.scheduledAt(500, "10:00", "Asia/Dubai")
			},
		},
		{
			name: "06 scheduled daily, done and a single rating",
			seed: `{"chats":{"600":{"chat_id":600,"notify_time":"09:00","timezone":"UTC","members":null}}}`,
			run: func(e *env) {
				if !e.fire(600) {
					e.t.Fatal("fire(600): nothing scheduled")
				}
				e.sent(600, dailyText, doneKB)
				e.answered(e.press(600, alice, "done"), "✅ Counted! Your total: 1")
				e.answered(e.press(600, alice, "done"), "Already counted today!")
				e.say(600, alice, "/rating")
				e.sent(600, "🏆 Solved: <b>1</b>", nil)
				e.storedEq(600, legacyChat{
					ChatID:     600,
					NotifyTime: "09:00",
					Timezone:   "UTC",
					Members:    map[string]legacyStat{"7": {Name: "Alice", Count: 1, LastSolvedDate: today()}},
				})
			},
		},
		{
			name: "07 group rating without ties",
			seed: `{"chats":{"-100123":{"chat_id":-100123,"notify_time":"09:00","timezone":"UTC",` +
				`"members":{"7":{"name":"Alice","count":2,"last_solved_date":"2026-09-01"}}}}}`,
			run: func(e *env) {
				e.say(-100123, alice, "/today@TestBot")
				e.sent(-100123, dailyText, doneKB)
				e.answered(e.press(-100123, alice, "done"), "✅ Counted! Your total: 3")
				e.answered(e.press(-100123, bob, "done"), "✅ Counted! Your total: 1")
				e.say(-100123, alice, "/rating")
				e.sent(-100123, "🏆 <b>Rating</b>\n\n🥇 Alice — 3\n🥈 @bob — 1\n", nil)
				e.storedEq(-100123, legacyChat{
					ChatID:     -100123,
					NotifyTime: "09:00",
					Timezone:   "UTC",
					Members: map[string]legacyStat{
						"7": {Name: "Alice", Count: 3, LastSolvedDate: today()},
						"8": {Name: "@bob", Count: 1, LastSolvedDate: today()},
					},
				})
			},
		},
		{
			name: "08 difficulty leads to a random pick repeated all day",
			run: func(e *env) {
				e.subscribe(800, "09:00", "UTC")
				e.say(800, alice, "/difficulty")
				d := e.sent(800, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
				e.answered(e.press(800, alice, "diff:Medium"), "")
				e.rekeyed(800, d, diffKB("Easy", "Hard"))
				e.answered(e.press(800, alice, "diff:Hard"), "")
				e.rekeyed(800, d, diffKB("Easy"))
				e.answered(e.press(800, alice, "diffsave"), "")
				e.edited(800, d, "✅ Difficulty updated: <b>Easy</b>", nil)

				if !e.fire(800) {
					e.t.Fatal("fire(800): nothing scheduled")
				}
				e.sent(800, randomEasyText, doneKB)
				pick := legacyPick{
					Date: "2026-09-28", DailyDifficulty: "Hard", ID: "1", Title: "Two Sum",
					Link: "/problems/two-sum/", Difficulty: "Easy", Tags: []string{"Array", "Hash Table"},
				}
				e.storedEq(800, legacyChat{ChatID: 800, NotifyTime: "09:00", Timezone: "UTC", Difficulties: []string{"Easy"}, DailyPick: &pick})
				lists := e.lc.listCalls()

				e.say(800, alice, "/today")
				e.sent(800, randomEasyText, doneKB)
				e.say(800, alice, "/start")
				e.sent(800, welcomeText, startKB)
				e.answered(e.press(800, alice, "/today"), "")
				e.sent(800, randomEasyText, doneKB)
				if got := e.lc.listCalls(); got != lists {
					e.t.Errorf("list calls: got %d, want %d (the pick is resent, not fetched again)", got, lists)
				}

				e.lc.setDaily("2026-09-29", "Hard")
				if !e.fire(800) {
					e.t.Fatal("fire(800): nothing scheduled")
				}
				e.sent(800, "🎲 LeetCode Random — September 29, 2026\nToday's daily is Hard, so here's a random Easy problem for you.\n\n🔢 1. Two Sum\n💪 Difficulty: Easy\n🏷 Array, Hash Table\n\n🔗 https://leetcode.com/problems/two-sum/", doneKB)
				if got := e.lc.listCalls(); got <= lists {
					e.t.Errorf("list calls: got %d, want more than %d (a new day makes a new pick)", got, lists)
				}
				pick.Date = "2026-09-29"
				e.storedEq(800, legacyChat{ChatID: 800, NotifyTime: "09:00", Timezone: "UTC", Difficulties: []string{"Easy"}, DailyPick: &pick})
			},
		},
		{
			name: "09 concurrent sends agree",
			seed: `{"chats":{"900":{"chat_id":900,"notify_time":"09:00","timezone":"UTC","members":null,"difficulties":["Easy"]}}}`,
			run: func(e *env) {
				e.lc.setRotating(true)
				e.lc.setListDelay(200 * time.Millisecond)
				fired := make(chan bool, 1)
				go func() { fired <- e.fire(900) }()
				e.say(900, alice, "/today")
				first, second := e.expectMessage(900), e.expectMessage(900)
				select {
				case ok := <-fired:
					if !ok {
						e.t.Fatal("fire(900): nothing scheduled")
					}
				case <-time.After(waitFor):
					e.t.Fatal("fire(900) did not return")
				}
				got, ok := e.stored(900)
				if !ok || got.DailyPick == nil {
					e.t.Fatalf("chat 900: no daily_pick stored: %s", asJSON(got))
				}
				want := randomText(got.DailyPick)
				for _, c := range []tgCall{first, second} {
					if c.method != "sendMessage" || c.blocked || c.text != want || !doneKB.matches(c.kb) {
						e.t.Errorf("chat 900:\n got %s\nwant sendMessage text=%q kb=%s", c, want, doneKB)
					}
				}
			},
		},
		{
			name: "10 daily ignores subscription and pick",
			seed: `{"chats":{"1001":{"chat_id":1001,"notify_time":"09:00","timezone":"UTC","members":null,"difficulties":["Easy"]}}}`,
			run: func(e *env) {
				e.say(1000, alice, "/daily")
				e.sent(1000, dailyText, doneKB)
				e.say(1001, alice, "/daily")
				e.sent(1001, dailyText, doneKB)
				e.say(1001, alice, "/start")
				e.sent(1001, welcomeText, startKB)
				e.answered(e.press(1001, alice, "/daily"), "")
				e.sent(1001, dailyText, doneKB)
				e.notStored(1000)
				e.storedEq(1001, legacyChat{ChatID: 1001, NotifyTime: "09:00", Timezone: "UTC", Difficulties: []string{"Easy"}})
				if got := e.lc.listCalls(); got != 0 {
					e.t.Errorf("list calls: got %d, want 0", got)
				}
			},
		},
		{
			name: "11 status and not-subscribed paths",
			seed: `{"chats":{"1101":{"chat_id":1101,"notify_time":"07:00","timezone":"UTC","members":null}}}`,
			run: func(e *env) {
				e.say(1100, alice, "/status")
				e.sent(1100, statusInactiveText, nil)
				e.subscribe(1100, "09:00", "Europe/Moscow")
				e.say(1100, alice, "/status")
				e.sent(1100, "✅ Subscription active\nTime: <b>09:00</b> (Europe/Moscow)\nDifficulty: <b>Easy, Medium, Hard</b>", nil)
				e.say(1101, alice, "/status")
				e.sent(1101, "✅ Subscription active\nTime: <b>07:00</b> (UTC)\nDifficulty: <b>Any</b>", nil)

				e.say(1102, alice, "/difficulty")
				e.sent(1102, notSubscribedText, nil)
				e.say(1102, alice, "/daily")
				e.sent(1102, dailyText, doneKB)
				e.answered(e.press(1102, alice, "done"), notSubscribedText)

				e.say(1100, alice, "/difficulty")
				d := e.sent(1100, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
				e.say(1100, alice, "/unsubscribe")
				e.sent(1100, disabledText, nil)
				e.answered(e.press(1100, alice, "diffsave"), "")
				e.edited(1100, d, notSubscribedText, nil)
				e.notStored(1100)
				e.notStored(1102)
			},
		},
		{
			name: "12 unsubscribe stops sends",
			run: func(e *env) {
				e.subscribe(1200, "09:00", "UTC")
				e.say(1200, alice, "/start")
				e.sent(1200, welcomeText, startKB)
				e.answered(e.press(1200, alice, "/unsubscribe"), "")
				e.sent(1200, disabledText, nil)
				if e.fire(1200) {
					e.t.Error("fire(1200): still scheduled after unsubscribe")
				}
				e.notScheduled(1200)
				e.notStored(1200)
				e.say(1200, alice, "/status")
				e.sent(1200, statusInactiveText, nil)
			},
		},
		{
			name: "13 blocked bot auto-unsubscribes",
			run: func(e *env) {
				e.subscribe(1300, "09:00", "UTC")
				e.block(1300)
				if !e.fire(1300) {
					e.t.Fatal("fire(1300): nothing scheduled")
				}
				if got := e.blockedAttempt(1300); got != dailyText {
					e.t.Errorf("blocked attempt text: got %q, want %q", got, dailyText)
				}
				e.notStored(1300)
				if e.fire(1300) {
					e.t.Error("fire(1300): still scheduled after the 403")
				}
				e.notScheduled(1300)
			},
		},
		{
			name: "14 leetcode outage",
			seed: `{"chats":{"1400":{"chat_id":1400,"notify_time":"09:00","timezone":"UTC","members":null},` +
				`"1401":{"chat_id":1401,"notify_time":"10:00","timezone":"UTC","members":null,"difficulties":["Easy"]}}}`,
			run: func(e *env) {
				e.lc.failDaily(true)
				e.say(1400, alice, "/today")
				e.sent(1400, fetchFailedText, nil)
				if !e.fire(1400) {
					e.t.Fatal("fire(1400): nothing scheduled")
				}
				e.sent(1400, fetchFailedText, nil)
				e.say(1400, alice, "/daily")
				e.sent(1400, fetchFailedText, nil)
				if got := e.lc.dailyCalls(); got != 3 {
					e.t.Errorf("daily calls: got %d, want 3 (one per attempt)", got)
				}
				e.storedEq(1400, legacyChat{ChatID: 1400, NotifyTime: "09:00", Timezone: "UTC"})
				e.scheduledAt(1400, "09:00", "UTC")

				e.lc.failDaily(false)
				e.lc.failList(true)
				e.say(1401, alice, "/today")
				e.sent(1401, fetchFailedText, nil)
				if !e.fire(1401) {
					e.t.Fatal("fire(1401): nothing scheduled")
				}
				e.sent(1401, fetchFailedText, nil)
				e.storedEq(1401, legacyChat{ChatID: 1401, NotifyTime: "10:00", Timezone: "UTC", Difficulties: []string{"Easy"}})
				e.scheduledAt(1401, "10:00", "UTC")
			},
		},
		{
			name: "15 restart restores schedules",
			seed: `{
  "chats": {
    "1500": {
      "chat_id": 1500,
      "notify_time": "09:30",
      "timezone": "Europe/Moscow",
      "members": {
        "7": {"name": "Alice", "count": 5, "last_solved_date": "2026-09-27"},
        "8": {"name": "@bob", "count": 2, "last_solved_date": "2026-09-27"}
      },
      "difficulties": ["Easy"],
      "daily_pick": {
        "date": "2026-09-28",
        "daily_difficulty": "Hard",
        "id": "9",
        "title": "Palindrome Number",
        "link": "/problems/palindrome-number/",
        "difficulty": "Easy",
        "tags": ["Math"]
      }
    },
    "1501": {
      "chat_id": 1501,
      "notify_time": "07:00",
      "timezone": "Asia/Dubai",
      "members": null
    }
  }
}`,
			run: func(e *env) {
				seeded := legacyChat{
					ChatID:     1500,
					NotifyTime: "09:30",
					Timezone:   "Europe/Moscow",
					Members: map[string]legacyStat{
						"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-27"},
						"8": {Name: "@bob", Count: 2, LastSolvedDate: "2026-09-27"},
					},
					Difficulties: []string{"Easy"},
					DailyPick: &legacyPick{
						Date: "2026-09-28", DailyDifficulty: "Hard", ID: "9", Title: "Palindrome Number",
						Link: "/problems/palindrome-number/", Difficulty: "Easy", Tags: []string{"Math"},
					},
				}
				legacy := legacyChat{ChatID: 1501, NotifyTime: "07:00", Timezone: "Asia/Dubai"}
				e.scheduledAt(1500, "09:30", "Europe/Moscow")
				e.scheduledAt(1501, "07:00", "Asia/Dubai")

				if !e.fire(1500) {
					e.t.Fatal("fire(1500): nothing scheduled")
				}
				e.sent(1500, "🎲 LeetCode Random — September 28, 2026\nToday's daily is Hard, so here's a random Easy problem for you.\n\n🔢 9. Palindrome Number\n💪 Difficulty: Easy\n🏷 Math\n\n🔗 https://leetcode.com/problems/palindrome-number/", doneKB)
				if got := e.lc.listCalls(); got != 0 {
					e.t.Errorf("list calls: got %d, want 0 (the seeded pick is resent)", got)
				}
				if !e.fire(1501) {
					e.t.Fatal("fire(1501): nothing scheduled")
				}
				e.sent(1501, dailyText, doneKB)
				e.say(1500, alice, "/rating")
				e.sent(1500, "🏆 <b>Rating</b>\n\n🥇 Alice — 5\n🥈 @bob — 2\n", nil)

				e.subscribe(1502, "18:30", "Asia/Bangkok")
				e.restart()
				e.scheduledAt(1500, "09:30", "Europe/Moscow")
				e.scheduledAt(1501, "07:00", "Asia/Dubai")
				e.scheduledAt(1502, "18:30", "Asia/Bangkok")
				if !e.fire(1502) {
					e.t.Fatal("fire(1502): nothing scheduled after restart")
				}
				e.sent(1502, dailyText, doneKB)
				e.storedEq(1500, seeded)
				e.storedEq(1501, legacy)
				e.storedEq(1502, legacyChat{ChatID: 1502, NotifyTime: "18:30", Timezone: "Asia/Bangkok", Difficulties: []string{"Easy", "Medium", "Hard"}})
			},
		},
		{
			name: "16 shutdown drains exactly once",
			run: func(e *env) {
				e.sync() // the poller is up and in a long poll
				e.say(1600, alice, "/about")
				e.stop()
				e.start()
				e.sent(1600, aboutText, nil)
				e.expectQuiet(1600)
			},
		},
		{
			// Deviation (a): today these callbacks are never answered.
			name: "17 unknown callbacks are answered",
			run: func(e *env) {
				e.say(1700, alice, "/about")
				m := e.sent(1700, aboutText, nil)
				for _, data := range []string{"bogus", "/start", "done:x"} {
					if got := e.expectAnswer(e.pressOn(1700, alice, m, data)); got != menuExpiredText {
						e.t.Errorf("%q answered %q, want %q", data, got, menuExpiredText)
					}
				}
				e.expectQuiet(1700)
			},
		},
		{
			// Deviation (b): today ties come in random map order.
			name: "18 rating ties are ordered by name",
			seed: `{"chats":{"-1800":{"chat_id":-1800,"notify_time":"20:00","timezone":"UTC","members":{` +
				`"9":{"name":"Carol","count":2,"last_solved_date":"2026-09-27"},` +
				`"7":{"name":"Alice","count":2,"last_solved_date":"2026-09-27"},` +
				`"8":{"name":"Bob","count":3,"last_solved_date":"2026-09-27"}}}}}`,
			run: func(e *env) {
				e.say(-1800, alice, "/rating")
				e.sent(-1800, "🏆 <b>Rating</b>\n\n🥇 Bob — 3\n🥈 Alice — 2\n🥉 Carol — 2\n", nil)
			},
		},
		{
			// A job run after Run returned gets a cancelled ctx: the daily
			// request never leaves the client, and the fetch-failed notice is
			// refused by the sender, so the strict invariants at cleanup see
			// no Bot API call for the chat.
			name: "19 jobs root is cancelled when Run returns",
			seed: `{"chats":{"1900":{"chat_id":1900,"notify_time":"09:00","timezone":"UTC","members":null}}}`,
			run: func(e *env) {
				e.sync()
				e.stop()
				n := e.lc.dailyCalls()
				if !e.fire(1900) {
					e.t.Fatal("fire(1900) = false, want the restored entry")
				}
				if got := e.lc.dailyCalls(); got != n {
					e.t.Errorf("dailyCalls after Run returned: got %d, want %d", got, n)
				}
			},
		},
		{
			// Review focus: a deploy mid-dialog. The sessions die with the old
			// process, so every dialog button on a prompt sent before the
			// restart has expired and typed input is plain text again, while
			// the buttons that need no session keep working.
			name: "20 restart mid-dialog expires its buttons, not Done",
			seed: `{"chats":{"2000":{"chat_id":2000,"notify_time":"08:00","timezone":"UTC","members":null,"difficulties":["Hard"]}}}`,
			run: func(e *env) {
				e.say(2000, alice, "/start")
				menu := e.sent(2000, welcomeText, startKB)
				e.say(2000, alice, "/today")
				problem := e.sent(2000, dailyText, doneKB)
				e.say(2000, alice, "/difficulty")
				d := e.sent(2000, chooseDifficultyText, diffKB("Hard"))
				e.say(2001, alice, "/setup")
				s := e.sent(2001, chooseTimeText, timeKB)
				e.answered(e.press(2001, alice, "time:09:00"), "")
				e.edited(2001, s, tzPrompt("09:00"), tzKB)

				e.restart()

				e.answered(e.pressOn(2001, alice, s, "tz:Europe/Moscow"), menuExpiredText)
				e.say(2001, alice, "Europe/Moscow")
				e.expectQuiet(2001)
				e.notStored(2001)
				e.answered(e.pressOn(2000, alice, d, "diff:Easy"), menuExpiredText)
				e.answered(e.pressOn(2000, alice, d, "diffsave"), menuExpiredText)
				e.answered(e.pressOn(2000, alice, problem, "done"), "✅ Counted! Your total: 1")
				e.answered(e.pressOn(2000, alice, menu, "/today"), "")
				e.sent(2000, dailyText, doneKB)
				e.expectQuiet(2000)
				e.storedEq(2000, legacyChat{
					ChatID:       2000,
					NotifyTime:   "08:00",
					Timezone:     "UTC",
					Members:      map[string]legacyStat{"7": {Name: "Alice", Count: 1, LastSolvedDate: today()}},
					Difficulties: []string{"Hard"},
				})
			},
		},
		{
			// Review focus: one stored chat that no longer schedules (a
			// hand-edited time, a zone gone from tzdata) is logged at restore;
			// startup and every other chat carry on, and nothing is deleted.
			name: "21 an unschedulable stored chat does not block the others",
			seed: `{"chats":{` +
				`"2100":{"chat_id":2100,"notify_time":"0900","timezone":"UTC","members":null},` +
				`"2101":{"chat_id":2101,"notify_time":"09:00","timezone":"Mars/Base","members":null},` +
				`"2102":{"chat_id":2102,"notify_time":"07:00","timezone":"Asia/Dubai","members":null}}}`,
			run: func(e *env) {
				e.notScheduled(2100)
				e.notScheduled(2101)
				e.scheduledAt(2102, "07:00", "Asia/Dubai")
				if !e.fire(2102) {
					e.t.Fatal("fire(2102): nothing scheduled")
				}
				e.sent(2102, dailyText, doneKB)
				e.storedEq(2100, legacyChat{ChatID: 2100, NotifyTime: "0900", Timezone: "UTC"})
				e.storedEq(2101, legacyChat{ChatID: 2101, NotifyTime: "09:00", Timezone: "Mars/Base"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			if tt.seed != "" {
				e.seed(tt.seed)
			}
			e.start()
			tt.run(e)
		})
	}
}

// TestNewFails pins the two startup errors an operator sees before main
// exits (spec §11): New returns them wrapped with today's prefixes.
func TestNewFails(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name       string
		cfg        func(tg *fakeTelegram, dir string) Config
		wantPrefix string
		wantCode   int // the Bot API error code; 0 = not a Bot API error
	}{
		{
			name: "a wrong BOT_TOKEN",
			cfg: func(tg *fakeTelegram, dir string) Config {
				return Config{Token: "WRONG:TOKEN", StoragePath: filepath.Join(dir, "config.json"), TelegramEndpoint: tg.srv.URL + "/bot%s/%s"}
			},
			wantPrefix: "NewBotAPI: ",
			wantCode:   http.StatusUnauthorized,
		},
		{
			name: "a STORAGE_PATH that is a directory",
			cfg: func(tg *fakeTelegram, dir string) Config {
				return Config{Token: testToken, StoragePath: dir, TelegramEndpoint: tg.srv.URL + "/bot%s/%s"}
			},
			wantPrefix: "storage: load storage: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := New(ctx, tt.cfg(newFakeTelegram(t), t.TempDir()))
			if err == nil {
				a.sched.Stop()
				t.Fatal("New: got nil error")
			}
			if !strings.HasPrefix(err.Error(), tt.wantPrefix) {
				t.Errorf("New: got %q, want the prefix %q", err, tt.wantPrefix)
			}
			gotCode := 0
			var tgErr *tgbotapi.Error
			if errors.As(err, &tgErr) {
				gotCode = tgErr.Code
			}
			if gotCode != tt.wantCode {
				t.Errorf("New: Bot API error code %d, want %d (%v)", gotCode, tt.wantCode, err)
			}
		})
	}
}
