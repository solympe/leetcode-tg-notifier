package telegram

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/telegram/mocks"
)

var alice = &tgbotapi.User{ID: 7, FirstName: "Alice"}

// message is alice's text message in testChat.
func message(text string) tgbotapi.Update {
	return tgbotapi.Update{UpdateID: 1, Message: &tgbotapi.Message{
		MessageID: 1,
		From:      alice,
		Chat:      &tgbotapi.Chat{ID: testChat},
		Text:      text,
	}}
}

// press is alice pressing the button with data on message msgID in testChat,
// as callback "cb".
func press(msgID int, data string) tgbotapi.Update {
	return tgbotapi.Update{UpdateID: 1, CallbackQuery: &tgbotapi.CallbackQuery{
		ID:      "cb",
		From:    alice,
		Message: &tgbotapi.Message{MessageID: msgID, Chat: &tgbotapi.Chat{ID: testChat}},
		Data:    data,
	}}
}

// apiCalls builds MockbotAPI factories. A factory that answers callback "cb"
// stores that expectation in answered, so the svc factory of the same row,
// built after it, can chain .After(answered).
type apiCalls struct {
	answered *gomock.Call
}

// answer expects only the answer to "cb" with toast.
func (a *apiCalls) answer(toast string) func(*gomock.Controller) *mocks.MockbotAPI {
	return func(ctrl *gomock.Controller) *mocks.MockbotAPI {
		m := mocks.NewMockbotAPI(ctrl)
		a.answered = m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb", toast))).Return(okResp, nil)
		return m
	}
}

// answerThen expects the "" answer to "cb", then want.
func (a *apiCalls) answerThen(want tgbotapi.Chattable) func(*gomock.Controller) *mocks.MockbotAPI {
	return func(ctrl *gomock.Controller) *mocks.MockbotAPI {
		m := mocks.NewMockbotAPI(ctrl)
		a.answered = m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb", ""))).Return(okResp, nil)
		gomock.InOrder(a.answered, m.EXPECT().Send(gomock.Eq(want)).Return(tgbotapi.Message{MessageID: 50}, nil))
		return m
	}
}

// sends expects only want, which is sent as message 50.
func (a *apiCalls) sends(want tgbotapi.Chattable) func(*gomock.Controller) *mocks.MockbotAPI {
	return func(ctrl *gomock.Controller) *mocks.MockbotAPI {
		m := mocks.NewMockbotAPI(ctrl)
		m.EXPECT().Send(gomock.Eq(want)).Return(tgbotapi.Message{MessageID: 50}, nil)
		return m
	}
}

func TestHandleRouting(t *testing.T) {
	ctx := t.Context()
	var api apiCalls
	subscribed := domain.Chat{
		ChatID:       testChat,
		NotifyTime:   "09:00",
		Timezone:     "Europe/Moscow",
		Difficulties: []string{"Easy", "Hard"},
		Members: map[string]domain.Member{
			"7": {Name: "Alice", Count: 3},
			"8": {Name: "@bob", Count: 1},
		},
	}
	storeDown := errors.New("store down")
	inactive := msgCfg(msgStatusInactive, nil)

	sendToday := func(ctrl *gomock.Controller) *mocks.Mockservice {
		m := mocks.NewMockservice(ctrl)
		m.EXPECT().SendToday(gomock.Eq(ctx), gomock.Eq(testChat))
		return m
	}
	subscription := func(c domain.Chat, ok bool, err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).Return(c, ok, err)
			return m
		}
	}

	tests := []struct {
		name    string
		update  tgbotapi.Update
		apiMock func(*gomock.Controller) *mocks.MockbotAPI
		svcMock func(*gomock.Controller) *mocks.Mockservice
	}{
		{name: "/today", update: message("/today"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "/today@TestBot", update: message("/today@TestBot"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "/today@Other, as before", update: message("/today@Other"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "surrounding spaces are trimmed", update: message("  /today \n"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "/today extra is not a command", update: message("/today extra"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{name: "an unknown command is ignored", update: message("/foo"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{name: "plain text outside a dialog is ignored", update: message("hello"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{name: "an update without message or callback is ignored", update: tgbotapi.Update{UpdateID: 1}, apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{
			name:    "/daily",
			update:  message("/daily"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().SendDaily(gomock.Eq(ctx), gomock.Eq(testChat))
				return m
			},
		},
		{
			name:    "/start: the welcome names the bot, with the menu",
			update:  message("/start"),
			apiMock: api.sends(msgCfg(fmt.Sprintf(msgWelcome, "TestBot"), startKeyboard())),
			svcMock: mocks.NewMockservice,
		},
		{name: "/about", update: message("/about"), apiMock: api.sends(msgCfg(msgAbout, nil)), svcMock: mocks.NewMockservice},
		{
			name:    "/status: subscribed",
			update:  message("/status"),
			apiMock: api.sends(msgCfg(fmt.Sprintf(msgStatusActive, "09:00", "Europe/Moscow", "Easy, Hard"), nil)),
			svcMock: subscription(subscribed, true, nil),
		},
		{
			name:    "/status: no difficulties means Any",
			update:  message("/status"),
			apiMock: api.sends(msgCfg(fmt.Sprintf(msgStatusActive, "09:00", "Europe/Moscow", "Any"), nil)),
			svcMock: subscription(domain.Chat{ChatID: testChat, NotifyTime: "09:00", Timezone: "Europe/Moscow"}, true, nil),
		},
		{name: "/status: unsubscribed", update: message("/status"), apiMock: api.sends(inactive), svcMock: subscription(domain.Chat{}, false, nil)},
		{name: "/status: a Subscription error sends nothing", update: message("/status"), apiMock: mocks.NewMockbotAPI, svcMock: subscription(domain.Chat{}, false, storeDown)},
		{
			name:    "/rating: standings",
			update:  message("/rating"),
			apiMock: api.sends(msgCfg(formatRating(subscribed.Standings()), nil)),
			svcMock: subscription(subscribed, true, nil),
		},
		{name: "/rating: unsubscribed", update: message("/rating"), apiMock: api.sends(msgCfg(msgRatingEmpty, nil)), svcMock: subscription(domain.Chat{}, false, nil)},
		{name: "/rating: no members", update: message("/rating"), apiMock: api.sends(msgCfg(msgRatingEmpty, nil)), svcMock: subscription(domain.Chat{ChatID: testChat}, true, nil)},
		{name: "/rating: a Subscription error sends nothing", update: message("/rating"), apiMock: mocks.NewMockbotAPI, svcMock: subscription(domain.Chat{}, false, storeDown)},
		{
			name:    "/unsubscribe",
			update:  message("/unsubscribe"),
			apiMock: api.sends(msgCfg(msgDisabled, nil)),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Unsubscribe(gomock.Eq(ctx), gomock.Eq(testChat)).Return(nil)
				return m
			},
		},
		{
			name:    "/unsubscribe: an error is logged and still confirmed",
			update:  message("/unsubscribe"),
			apiMock: api.sends(msgCfg(msgDisabled, nil)),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Unsubscribe(gomock.Eq(ctx), gomock.Eq(testChat)).Return(storeDown)
				return m
			},
		},
		{
			name:    "menu /setup: answered, then the time prompt",
			update:  press(10, "/setup"),
			apiMock: api.answerThen(msgCfg(msgChooseTime, timeKeyboard())),
			svcMock: mocks.NewMockservice,
		},
		{
			name:    "menu /difficulty: answered, then Subscription",
			update:  press(10, "/difficulty"),
			apiMock: api.answerThen(msgCfg(msgNotSubscribed, nil)),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(domain.Chat{}, false, nil)
				return m
			},
		},
		{
			name:    "menu /today: answered, then SendToday",
			update:  press(10, "/today"),
			apiMock: api.answer(""),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().SendToday(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered)
				return m
			},
		},
		{
			name:    "menu /daily: answered, then SendDaily",
			update:  press(10, "/daily"),
			apiMock: api.answer(""),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().SendDaily(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered)
				return m
			},
		},
		{
			name:    "menu /status: answered, then the status",
			update:  press(10, "/status"),
			apiMock: api.answerThen(inactive),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(domain.Chat{}, false, nil)
				return m
			},
		},
		{
			name:    "menu /rating: answered, then the rating",
			update:  press(10, "/rating"),
			apiMock: api.answerThen(msgCfg(msgRatingEmpty, nil)),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(domain.Chat{}, false, nil)
				return m
			},
		},
		{
			name:    "menu /unsubscribe: answered, then Unsubscribe",
			update:  press(10, "/unsubscribe"),
			apiMock: api.answerThen(msgCfg(msgDisabled, nil)),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Unsubscribe(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(nil)
				return m
			},
		},
		{
			name: "a callback without its message is too old",
			update: tgbotapi.Update{UpdateID: 1, CallbackQuery: &tgbotapi.CallbackQuery{
				ID: "cb", From: alice, Data: "/today",
			}},
			apiMock: api.answer(msgMessageExpired),
			svcMock: mocks.NewMockservice,
		},
		{name: "unknown data has expired", update: press(10, "bogus"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice},
		{name: "/start is not a menu button", update: press(10, "/start"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice},
		{name: "done:x is not done", update: press(10, "done:x"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice},
		{
			name:    "a panic is recovered",
			update:  message("/today"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().SendToday(gomock.Eq(ctx), gomock.Eq(testChat)).Do(func(context.Context, int64) { panic("boom") })
				return m
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			apiMock := tt.apiMock(ctrl) // before svcMock, which may chain .After(api.answered)
			h := NewHandler(apiMock, tt.svcMock(ctrl), "TestBot")
			h.Handle(ctx, tt.update)
		})
	}
}

func TestDone(t *testing.T) {
	ctx := t.Context()
	var api apiCalls
	bob := &tgbotapi.User{ID: 8, UserName: "bob"}

	solve := func(userID int64, name string, total int, err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().Solve(gomock.Eq(ctx), gomock.Eq(testChat), gomock.Eq(userID), gomock.Eq(name)).Return(total, err)
			return m
		}
	}

	tests := []struct {
		name    string
		from    *tgbotapi.User
		svcMock func(*gomock.Controller) *mocks.Mockservice
		apiMock func(*gomock.Controller) *mocks.MockbotAPI
	}{
		{
			name:    "counted",
			from:    alice,
			svcMock: solve(7, "Alice", 3, nil),
			apiMock: api.answer(fmt.Sprintf(msgCounted, 3)),
		},
		{
			name:    "no first name: @username",
			from:    bob,
			svcMock: solve(8, "@bob", 1, nil),
			apiMock: api.answer(fmt.Sprintf(msgCounted, 1)),
		},
		{
			name:    "already counted today",
			from:    alice,
			svcMock: solve(7, "Alice", 3, domain.ErrAlreadySolved),
			apiMock: api.answer(msgAlreadyCounted),
		},
		{
			name:    "not subscribed",
			from:    alice,
			svcMock: solve(7, "Alice", 0, domain.ErrNotSubscribed),
			apiMock: api.answer(msgNotSubscribed),
		},
		{
			name:    "a failed write after counting still reports the total",
			from:    alice,
			svcMock: solve(7, "Alice", 1, errors.New("disk full")),
			apiMock: api.answer(fmt.Sprintf(msgCounted, 1)),
		},
		{
			name:    "a refused call only stops the spinner",
			from:    alice,
			svcMock: solve(7, "Alice", 0, context.Canceled),
			apiMock: api.answer(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			u := press(10, cbDone)
			u.CallbackQuery.From = tt.from
			h := NewHandler(tt.apiMock(ctrl), tt.svcMock(ctrl), "TestBot")
			h.Handle(ctx, u)
		})
	}
}
