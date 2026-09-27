package bot

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

const (
	testChatID    = int64(794812315)
	testDailyDate = "2026-09-27"
)

func testDaily(difficulty string) *leetcode.Problem {
	return &leetcode.Problem{
		Date:       testDailyDate,
		Link:       "/problems/two-sum/",
		ID:         "1",
		Title:      "Two Sum",
		Difficulty: difficulty,
		Tags:       []string{"Array", "Hash Table"},
	}
}

// testRandom is what FetchRandom returns: no Date, the caller sets it.
func testRandom() *leetcode.Problem {
	return &leetcode.Problem{
		Link:       "/problems/climbing-stairs/",
		ID:         "70",
		Title:      "Climbing Stairs",
		Difficulty: "Easy",
		Tags:       []string{"Math", "Dynamic Programming"},
	}
}

// testPick is the pick persisted after testRandom replaced a Hard daily of date.
func testPick(date string) *storage.DailyPick {
	return &storage.DailyPick{
		Date:            date,
		DailyDifficulty: "Hard",
		ID:              "70",
		Title:           "Climbing Stairs",
		Link:            "/problems/climbing-stairs/",
		Difficulty:      "Easy",
		Tags:            []string{"Math", "Dynamic Programming"},
	}
}

// testMediumRandom is a Medium problem returned by FetchRandom.
func testMediumRandom() *leetcode.Problem {
	return &leetcode.Problem{
		Link:       "/problems/add-two-numbers/",
		ID:         "2",
		Title:      "Add Two Numbers",
		Difficulty: "Medium",
		Tags:       []string{"Linked List", "Math"},
	}
}

// randomText is the text sent when testRandom replaces today's Hard daily.
func randomText() string {
	p := testRandom()
	p.Date = testDailyDate
	return leetcode.FormatRandomProblem(p, "Hard")
}

func problemMsg(text string) tgbotapi.MessageConfig {
	return kbMsg(testChatID, text, doneKeyboard())
}

func subscribedCfg(difficulties ...string) storage.ChatConfig {
	return storage.ChatConfig{ChatID: testChatID, NotifyTime: "09:00", Timezone: "UTC", Difficulties: difficulties}
}

func withPick(cfg storage.ChatConfig, pick *storage.DailyPick) storage.ChatConfig {
	cfg.DailyPick = pick
	return cfg
}

func TestIsBotBlocked(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic error", errors.New("something"), false},
		{"tg error 400", &tgbotapi.Error{Code: http.StatusBadRequest}, false},
		{"tg error 403", &tgbotapi.Error{Code: http.StatusForbidden}, true},
		{"wrapped tg error 403", fmt.Errorf("wrap: %w", &tgbotapi.Error{Code: http.StatusForbidden}), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBotBlocked(tt.err); got != tt.want {
				t.Errorf("isBotBlocked(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestSendDailyProblem(t *testing.T) {
	members := map[string]storage.UserStat{"1": {Name: "Alice", Count: 3, LastSolvedDate: testDailyDate}}
	storedPick := &storage.DailyPick{
		Date:            testDailyDate,
		DailyDifficulty: "Hard",
		ID:              "13",
		Title:           "Roman to Integer",
		Link:            "/problems/roman-to-integer/",
		Difficulty:      "Easy",
		Tags:            []string{"Math", "String"},
	}
	storedPickText := "🎲 LeetCode Random — September 27, 2026\n" +
		"Today's daily is Hard, so here's a random Easy problem for you.\n\n" +
		"🔢 13. Roman to Integer\n💪 Difficulty: Easy\n🏷 Math, String\n\n" +
		"🔗 https://leetcode.com/problems/roman-to-integer/"

	tests := []struct {
		name       string
		lcMock     func(*gomock.Controller) *mocks.MocklcFetcher
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock  func(*gomock.Controller) *mocks.MockchatStore
		schedMock  func(*gomock.Controller) *mocks.MocktaskScheduler
	}{
		{
			name: "fetch error sends fetch-failed message",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(nil, errors.New("timeout"))
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(testChatID, msgFetchFailed))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: mocks.NewMockchatStore,
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "bot blocked by user removes subscription",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Easy"), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, &tgbotapi.Error{
					Code:    http.StatusForbidden,
					Message: "Forbidden: bot was blocked by the user",
				})
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(testChatID).Return(storage.ChatConfig{}, false),
					m.EXPECT().Delete(testChatID).Return(nil),
				)
				return m
			},
			schedMock: func(ctrl *gomock.Controller) *mocks.MocktaskScheduler {
				m := mocks.NewMocktaskScheduler(ctrl)
				m.EXPECT().Remove(testChatID)
				return m
			},
		},
		{
			// No Remove or Delete expected — gomock fails the test if they are called.
			name: "non-403 send error keeps subscription intact",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Easy"), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, &tgbotapi.Error{Code: http.StatusTooManyRequests})
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(testChatID).Return(storage.ChatConfig{}, false)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "not subscribed sends the daily",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(leetcode.FormatProblem(testDaily("Hard"))))).
					Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(testChatID).Return(storage.ChatConfig{}, false)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "empty difficulties send the daily without a random fetch",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(leetcode.FormatProblem(testDaily("Hard"))))).
					Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(testChatID).Return(subscribedCfg(), true)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "daily of a subscribed difficulty is sent as is",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(leetcode.FormatProblem(testDaily("Hard"))))).
					Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(testChatID).Return(withPick(subscribedCfg("Easy", "Hard"), testPick("2026-09-26")), true)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "subscribed daily wins over a same-day pick",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(leetcode.FormatProblem(testDaily("Hard"))))).
					Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(testChatID).Return(withPick(subscribedCfg("Easy", "Hard"), testPick(testDailyDate)), true)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "mismatch without a pick sends and saves a random problem",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				m.EXPECT().FetchRandom(gomock.Eq([]string{"Easy"})).Return(testRandom(), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(randomText()))).Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(testChatID).Return(subscribedCfg("Easy"), true),
					m.EXPECT().Update(gomock.Eq(testChatID), gomock.Any()).DoAndReturn(updateVia(m, subscribedCfg("Easy"))),
					m.EXPECT().Set(gomock.Eq(withPick(subscribedCfg("Easy"), testPick(testDailyDate)))).Return(nil),
				)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "random fetch gets every subscribed difficulty",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				m.EXPECT().FetchRandom(gomock.Eq([]string{"Easy", "Medium"})).Return(testRandom(), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(randomText()))).Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(testChatID).Return(subscribedCfg("Easy", "Medium"), true),
					m.EXPECT().Update(gomock.Eq(testChatID), gomock.Any()).DoAndReturn(updateVia(m, subscribedCfg("Easy", "Medium"))),
					m.EXPECT().Set(gomock.Eq(withPick(subscribedCfg("Easy", "Medium"), testPick(testDailyDate)))).Return(nil),
				)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "pick is saved onto the current config so concurrent changes survive",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				m.EXPECT().FetchRandom(gomock.Eq([]string{"Easy"})).Return(testRandom(), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(randomText()))).Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				updated := subscribedCfg("Easy")
				updated.Members = members
				gomock.InOrder(
					m.EXPECT().Get(testChatID).Return(subscribedCfg("Easy"), true),
					m.EXPECT().Update(gomock.Eq(testChatID), gomock.Any()).DoAndReturn(updateVia(m, updated)),
					m.EXPECT().Set(gomock.Eq(withPick(updated, testPick(testDailyDate)))).Return(nil),
				)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "same-day pick of a subscribed difficulty is resent without fetching or saving",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(storedPickText))).Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(testChatID).Return(withPick(subscribedCfg("Easy"), storedPick), true)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			// The chat switched from Easy to Medium after today's Easy pick was saved.
			name: "same-day pick of a difficulty no longer subscribed is replaced",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				m.EXPECT().FetchRandom(gomock.Eq([]string{"Medium"})).Return(testMediumRandom(), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				p := testMediumRandom()
				p.Date = testDailyDate
				m.EXPECT().Send(gomock.Eq(problemMsg(leetcode.FormatRandomProblem(p, "Hard")))).
					Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				outdated := withPick(subscribedCfg("Medium"), testPick(testDailyDate))
				gomock.InOrder(
					m.EXPECT().Get(testChatID).Return(outdated, true),
					m.EXPECT().Update(gomock.Eq(testChatID), gomock.Any()).DoAndReturn(updateVia(m, outdated)),
					m.EXPECT().Set(gomock.Eq(withPick(subscribedCfg("Medium"), &storage.DailyPick{
						Date:            testDailyDate,
						DailyDifficulty: "Hard",
						ID:              "2",
						Title:           "Add Two Numbers",
						Link:            "/problems/add-two-numbers/",
						Difficulty:      "Medium",
						Tags:            []string{"Linked List", "Math"},
					}))).Return(nil),
				)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "stale pick from another day is replaced",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				m.EXPECT().FetchRandom(gomock.Eq([]string{"Easy"})).Return(testRandom(), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(randomText()))).Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				stale := withPick(subscribedCfg("Easy"), testPick("2026-09-26"))
				gomock.InOrder(
					m.EXPECT().Get(testChatID).Return(stale, true),
					m.EXPECT().Update(gomock.Eq(testChatID), gomock.Any()).DoAndReturn(updateVia(m, stale)),
					m.EXPECT().Set(gomock.Eq(withPick(subscribedCfg("Easy"), testPick(testDailyDate)))).Return(nil),
				)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "random fetch error sends fetch-failed and saves nothing",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				m.EXPECT().FetchRandom(gomock.Eq([]string{"Easy"})).Return(nil, errors.New("timeout"))
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(testChatID, msgFetchFailed))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(testChatID).Return(subscribedCfg("Easy"), true)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "chat deleted before the pick is saved still gets the problem",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				m.EXPECT().FetchRandom(gomock.Eq([]string{"Easy"})).Return(testRandom(), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(randomText()))).Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(testChatID).Return(subscribedCfg("Easy"), true),
					m.EXPECT().Update(gomock.Eq(testChatID), gomock.Any()).Return(false, nil),
				)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "pick save error still sends the problem",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(testDaily("Hard"), nil)
				m.EXPECT().FetchRandom(gomock.Eq([]string{"Easy"})).Return(testRandom(), nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(randomText()))).Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(testChatID).Return(subscribedCfg("Easy"), true),
					m.EXPECT().Update(gomock.Eq(testChatID), gomock.Any()).DoAndReturn(updateVia(m, subscribedCfg("Easy"))),
					m.EXPECT().Set(gomock.Eq(withPick(subscribedCfg("Easy"), testPick(testDailyDate)))).Return(errors.New("disk full")),
				)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			b := New(
				tt.senderMock(ctrl),
				"TestBot",
				tt.storeMock(ctrl),
				tt.lcMock(ctrl),
				tt.schedMock(ctrl),
			)

			b.SendDailyProblem(testChatID)
		})
	}
}

// TestSendDailyProblemConcurrentDone runs cron sends that save a new pick
// against Done presses on the update loop, sharing a real JSON storage: the
// pick save marshals every chat while Done updates members, and in one chat
// neither save may drop the other's change.
func TestSendDailyProblemConcurrentDone(t *testing.T) {
	// -race flagged the unsynchronized map access in every run from 50 rounds
	// on; before saves were atomic, the same-chat case lost Done presses in 19
	// of 20 runs at 200 rounds, which takes under a second.
	const rounds = 200
	firstDay := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	alice := map[string]storage.UserStat{"1": {Name: "Alice", Count: 1, LastSolvedDate: "2026-09-26"}}

	anySender := func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
		m := mocks.NewMocktelegramSender(ctrl)
		m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{MessageID: 1}, nil).AnyTimes()
		m.EXPECT().Request(gomock.Any()).Return(answered, nil).AnyTimes()
		return m
	}
	// Every send sees a Hard daily of a new date, so each one saves a new pick.
	hardDailies := func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
		day := 0
		m := mocks.NewMocklcFetcher(ctrl)
		m.EXPECT().FetchDaily().DoAndReturn(func() (*leetcode.Problem, error) {
			p := testDaily("Hard")
			p.Date = firstDay.AddDate(0, 0, day).Format("2006-01-02")
			day++
			return p, nil
		}).Times(rounds)
		m.EXPECT().FetchRandom(gomock.Eq([]string{"Easy"})).
			DoAndReturn(func([]string) (*leetcode.Problem, error) { return testRandom(), nil }).
			Times(rounds)
		return m
	}

	tests := []struct {
		name        string
		chats       []storage.ChatConfig
		sendChatID  int64
		doneChatID  int64
		senderMock  func(*gomock.Controller) *mocks.MocktelegramSender
		lcMock      func(*gomock.Controller) *mocks.MocklcFetcher
		schedMock   func(*gomock.Controller) *mocks.MocktaskScheduler
		wantMembers int
	}{
		{
			name: "pick saves in one chat and Done presses in another",
			chats: []storage.ChatConfig{
				{ChatID: 1, NotifyTime: "09:00", Timezone: "UTC", Difficulties: []string{"Easy"}},
				{ChatID: 2, NotifyTime: "09:00", Timezone: "UTC", Members: alice},
			},
			sendChatID:  1,
			doneChatID:  2,
			senderMock:  anySender,
			lcMock:      hardDailies,
			schedMock:   mocks.NewMocktaskScheduler,
			wantMembers: len(alice) + rounds,
		},
		{
			name: "pick saves and Done presses in the same chat",
			chats: []storage.ChatConfig{
				{ChatID: 1, NotifyTime: "09:00", Timezone: "UTC", Difficulties: []string{"Easy"}, Members: alice},
			},
			sendChatID:  1,
			doneChatID:  1,
			senderMock:  anySender,
			lcMock:      hardDailies,
			schedMock:   mocks.NewMocktaskScheduler,
			wantMembers: len(alice) + rounds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store, err := storage.NewJSONStorage(filepath.Join(t.TempDir(), "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, cfg := range tt.chats {
				if err := store.Set(cfg); err != nil {
					t.Fatal(err)
				}
			}
			b := New(tt.senderMock(ctrl), "TestBot", store, tt.lcMock(ctrl), tt.schedMock(ctrl))

			var wg sync.WaitGroup
			wg.Go(func() {
				for range rounds {
					b.SendDailyProblem(tt.sendChatID)
				}
			})
			wg.Go(func() {
				for i := range rounds {
					b.handleCallback(makeCallbackQuery(fmt.Sprintf("cb%d", i), int64(100+i), "User", tt.doneChatID))
				}
			})
			wg.Wait()

			if got, _ := store.Get(tt.doneChatID); len(got.Members) != tt.wantMembers {
				t.Errorf("members: got %d, want %d", len(got.Members), tt.wantMembers)
			}
			wantDate := firstDay.AddDate(0, 0, rounds-1).Format("2006-01-02")
			if got, _ := store.Get(tt.sendChatID); got.DailyPick == nil || got.DailyPick.Date != wantDate {
				t.Errorf("pick: got %+v, want one dated %s", got.DailyPick, wantDate)
			}
		})
	}
}

// TestSendDailyProblemConcurrentPick runs concurrent sends to one chat whose
// daily is not subscribed, as a cron send and /today can be, sharing a real
// JSON storage: one of them fetches and saves the pick while the others wait
// for it and resend it, so the chat gets one problem, not several.
func TestSendDailyProblemConcurrentPick(t *testing.T) {
	// Before the per-chat lock, every caller fetched and sent a problem of its
	// own in each of 30 runs: the slow fetch keeps the first one in flight
	// while the others find no pick yet.
	const callers = 8
	mediumRandom := testMediumRandom()
	mediumRandom.Date = testDailyDate

	// slowRandom fetches a Hard daily for every caller and random, of one of
	// difficulties, only once; the fetch is slow enough for the other callers
	// to arrive while it is in flight.
	slowRandom := func(difficulties []string, random func() *leetcode.Problem) func(*gomock.Controller) *mocks.MocklcFetcher {
		return func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
			m := mocks.NewMocklcFetcher(ctrl)
			m.EXPECT().FetchDaily().DoAndReturn(func() (*leetcode.Problem, error) { return testDaily("Hard"), nil }).AnyTimes()
			m.EXPECT().FetchRandom(gomock.Eq(difficulties)).DoAndReturn(func([]string) (*leetcode.Problem, error) {
				time.Sleep(50 * time.Millisecond)
				return random(), nil
			}).Times(1)
			return m
		}
	}
	// sendsToEach expects every caller to send text.
	sendsToEach := func(text string) func(*gomock.Controller) *mocks.MocktelegramSender {
		return func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
			m := mocks.NewMocktelegramSender(ctrl)
			m.EXPECT().Send(gomock.Eq(problemMsg(text))).Return(tgbotapi.Message{MessageID: 1}, nil).Times(callers)
			return m
		}
	}

	tests := []struct {
		name       string
		stored     storage.ChatConfig
		lcMock     func(*gomock.Controller) *mocks.MocklcFetcher
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		wantPick   *storage.DailyPick
	}{
		{
			name:       "no pick yet",
			stored:     subscribedCfg("Easy"),
			lcMock:     slowRandom([]string{"Easy"}, testRandom),
			senderMock: sendsToEach(randomText()),
			wantPick:   testPick(testDailyDate),
		},
		{
			name:       "pick from another day",
			stored:     withPick(subscribedCfg("Easy"), testPick("2026-09-26")),
			lcMock:     slowRandom([]string{"Easy"}, testRandom),
			senderMock: sendsToEach(randomText()),
			wantPick:   testPick(testDailyDate),
		},
		{
			name:       "pick of a difficulty no longer subscribed",
			stored:     withPick(subscribedCfg("Medium"), testPick(testDailyDate)),
			lcMock:     slowRandom([]string{"Medium"}, testMediumRandom),
			senderMock: sendsToEach(leetcode.FormatRandomProblem(mediumRandom, "Hard")),
			wantPick: &storage.DailyPick{
				Date:            testDailyDate,
				DailyDifficulty: "Hard",
				ID:              "2",
				Title:           "Add Two Numbers",
				Link:            "/problems/add-two-numbers/",
				Difficulty:      "Medium",
				Tags:            []string{"Linked List", "Math"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store, err := storage.NewJSONStorage(filepath.Join(t.TempDir(), "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set(tt.stored); err != nil {
				t.Fatal(err)
			}
			b := New(tt.senderMock(ctrl), "TestBot", store, tt.lcMock(ctrl), mocks.NewMocktaskScheduler(ctrl))

			var wg sync.WaitGroup
			for range callers {
				wg.Go(func() { b.SendDailyProblem(testChatID) })
			}
			wg.Wait()

			if got, _ := store.Get(testChatID); !reflect.DeepEqual(got.DailyPick, tt.wantPick) {
				t.Errorf("pick: got %+v, want %+v", got.DailyPick, tt.wantPick)
			}
		})
	}
}

func TestWantsDaily(t *testing.T) {
	tests := []struct {
		name       string
		set        []string
		difficulty string
		want       bool
	}{
		{name: "nil set means any", set: nil, difficulty: "Hard", want: true},
		{name: "empty set means any", set: []string{}, difficulty: "Hard", want: true},
		{name: "subscribed difficulty", set: []string{"Easy", "Hard"}, difficulty: "Hard", want: true},
		{name: "case-insensitive match", set: []string{"hard"}, difficulty: "Hard", want: true},
		{name: "not subscribed", set: []string{"Easy", "Medium"}, difficulty: "Hard", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wantsDaily(tt.set, tt.difficulty); got != tt.want {
				t.Errorf("wantsDaily(%v, %q) = %v, want %v", tt.set, tt.difficulty, got, tt.want)
			}
		})
	}
}
