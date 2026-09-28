package telegram

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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

// subscription expects one Subscription of testChat with ctx.
func subscription(ctx context.Context, c domain.Chat, ok bool, err error) func(*gomock.Controller) *mocks.Mockservice {
	return func(ctrl *gomock.Controller) *mocks.Mockservice {
		m := mocks.NewMockservice(ctrl)
		m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).Return(c, ok, err)
		return m
	}
}

// TestHandle covers routing and the dialogs, one update per row. Flows the
// integration suite drives end to end are left to it.
func TestHandle(t *testing.T) {
	ctx := t.Context()
	var api apiCalls
	const prompt = 42 // the dialog message whose buttons are live
	all := []string{"Easy", "Medium", "Hard"}
	diskFull := errors.New("disk full")
	storeDown := errors.New("store down")

	timeStep := session{step: stepTime, msgID: prompt}
	tzStep := session{step: stepTimezone, msgID: prompt, notifyTime: "09:00"}
	setupDiff := session{step: stepSetupDifficulty, msgID: prompt, notifyTime: "09:00", timezone: "Europe/Moscow", selected: []string{"Easy", "Medium"}}
	editDiff := session{step: stepEditDifficulty, msgID: prompt, selected: []string{"Easy"}}
	tzPrompt := editCfg(prompt, fmt.Sprintf(msgChooseTz, "09:00"), tzKeyboard())
	allSet := editCfg(prompt, fmt.Sprintf(msgAllSet, "09:00", "Europe/Moscow", "Easy, Medium"), nil)
	updated := editCfg(prompt, fmt.Sprintf(msgDifficultyUpdated, "Easy"), nil)

	sendToday := func(ctrl *gomock.Controller) *mocks.Mockservice {
		m := mocks.NewMockservice(ctrl)
		m.EXPECT().SendToday(gomock.Eq(ctx), gomock.Eq(testChat))
		return m
	}
	// unsubscribedAfterAnswer reads an unsubscribed chat after the "" answer.
	unsubscribedAfterAnswer := func(ctrl *gomock.Controller) *mocks.Mockservice {
		m := mocks.NewMockservice(ctrl)
		m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(domain.Chat{}, false, nil)
		return m
	}
	solve := func(total int, err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().Solve(gomock.Eq(ctx), gomock.Eq(testChat), gomock.Eq(int64(7)), gomock.Eq("Alice")).Return(total, err)
			return m
		}
	}

	subscribeFails := func(ctrl *gomock.Controller) *mocks.Mockservice {
		m := mocks.NewMockservice(ctrl)
		m.EXPECT().Subscribe(gomock.Eq(ctx), gomock.Eq(testChat), gomock.Eq("09:00"), gomock.Eq("Europe/Moscow"),
			gomock.Eq([]string{"Easy", "Medium"})).After(api.answered).Return(diskFull)
		return m
	}
	setDifficultiesFails := func(ctrl *gomock.Controller) *mocks.Mockservice {
		m := mocks.NewMockservice(ctrl)
		m.EXPECT().SetDifficulties(gomock.Eq(ctx), gomock.Eq(testChat), gomock.Eq([]string{"Easy"})).After(api.answered).Return(diskFull)
		return m
	}
	sendFails := func(ctrl *gomock.Controller) *mocks.MockbotAPI {
		m := mocks.NewMockbotAPI(ctrl)
		m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, diskFull)
		return m
	}

	tests := []struct {
		name    string
		before  session // stored for testChat when its step is set
		update  tgbotapi.Update
		apiMock func(*gomock.Controller) *mocks.MockbotAPI
		svcMock func(*gomock.Controller) *mocks.Mockservice
		after   session // testChat's session afterwards; the zero value means none
	}{
		// routing
		{name: "/today@TestBot", update: message("/today@TestBot"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "/today@Other, as before", update: message("/today@Other"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "surrounding spaces are trimmed", update: message("  /today \n"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "/today extra is not a command", update: message("/today extra"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{name: "an unknown command is ignored", update: message("/foo"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{name: "plain text outside a dialog is ignored", update: message("hello"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{name: "an update without message or callback is ignored", update: tgbotapi.Update{UpdateID: 1}, apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{name: "/status: a Subscription error sends nothing", update: message("/status"), apiMock: mocks.NewMockbotAPI, svcMock: subscription(ctx, domain.Chat{}, false, storeDown)},
		{name: "/rating: unsubscribed", update: message("/rating"), apiMock: api.sends(msgCfg(msgRatingEmpty, nil)), svcMock: subscription(ctx, domain.Chat{}, false, nil)},
		{name: "/rating: a Subscription error sends nothing", update: message("/rating"), apiMock: mocks.NewMockbotAPI, svcMock: subscription(ctx, domain.Chat{}, false, storeDown)},
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
		{name: "menu /difficulty: answered, then Subscription", update: press(10, "/difficulty"), apiMock: api.answerThen(msgCfg(msgNotSubscribed, nil)), svcMock: unsubscribedAfterAnswer},
		{name: "menu /status: answered, then the status", update: press(10, "/status"), apiMock: api.answerThen(msgCfg(msgStatusInactive, nil)), svcMock: unsubscribedAfterAnswer},
		{name: "menu /rating: answered, then the rating", update: press(10, "/rating"), apiMock: api.answerThen(msgCfg(msgRatingEmpty, nil)), svcMock: unsubscribedAfterAnswer},
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
		{name: "done: a failed write after counting still reports the total", update: press(10, cbDone), apiMock: api.answer(fmt.Sprintf(msgCounted, 1)), svcMock: solve(1, errors.New("disk full"))},
		{name: "done: a refused call only stops the spinner", update: press(10, cbDone), apiMock: api.answer(""), svcMock: solve(0, context.Canceled)},
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

		// the time step
		{
			name:    "time button: answered, then the timezone step",
			before:  timeStep,
			update:  press(prompt, "time:09:00"),
			apiMock: api.answerThen(tzPrompt),
			svcMock: mocks.NewMockservice,
			after:   tzStep,
		},
		{name: "time button in another step has expired", before: tzStep, update: press(prompt, "time:07:00"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: tzStep},
		{name: "forged time:25:00 has expired", before: timeStep, update: press(prompt, "time:25:00"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: timeStep},

		// the timezone step
		{
			name:    "timezone button: answered, then a new chat gets all three",
			before:  tzStep,
			update:  press(prompt, "tz:Europe/Moscow"),
			apiMock: api.answerThen(editCfg(prompt, msgChooseDifficulty, difficultyKeyboard(all))),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(domain.Chat{}, false, nil)
				return m
			},
			after: session{step: stepSetupDifficulty, msgID: prompt, notifyTime: "09:00", timezone: "Europe/Moscow", selected: all},
		},
		{name: "timezone button on another message has expired", before: tzStep, update: press(prompt-1, "tz:UTC"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: tzStep},
		{
			name:    "a Subscription error keeps the timezone step",
			before:  tzStep,
			update:  message("Europe/Moscow"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: subscription(ctx, domain.Chat{}, false, diskFull),
			after:   tzStep,
		},

		// difficulty buttons
		{
			name:    "difficulty button: answered, toggled, rekeyed",
			before:  editDiff,
			update:  press(prompt, "diff:Hard"),
			apiMock: api.answerThen(tgbotapi.NewEditMessageReplyMarkup(testChat, prompt, *difficultyKeyboard([]string{"Easy", "Hard"}))),
			svcMock: mocks.NewMockservice,
			after:   session{step: stepEditDifficulty, msgID: prompt, selected: []string{"Easy", "Hard"}},
		},
		{name: "difficulty button on another message has expired", before: editDiff, update: press(prompt-1, "diff:Hard"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: editDiff},
		{name: "difficulty button in the time step has expired", before: timeStep, update: press(prompt, "diff:Hard"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: timeStep},
		{name: "Save on another message has expired", before: editDiff, update: press(prompt-1, cbDiffSave), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: editDiff},

		// saves
		{name: "setup Save: a Subscribe error still shows All set", before: setupDiff, update: press(prompt, cbDiffSave), apiMock: api.answerThen(allSet), svcMock: subscribeFails},
		{name: "difficulty Save: another error still shows updated", before: editDiff, update: press(prompt, cbDiffSave), apiMock: api.answerThen(updated), svcMock: setDifficultiesFails},

		// sessions
		{
			name:    "/setup discards another session",
			before:  editDiff,
			update:  message("/setup"),
			apiMock: api.sends(msgCfg(msgChooseTime, timeKeyboard())),
			svcMock: mocks.NewMockservice,
			after:   session{step: stepTime, msgID: 50},
		},
		{name: "/setup: a failed send leaves no session", before: editDiff, update: message("/setup"), apiMock: sendFails, svcMock: mocks.NewMockservice},
		{
			name:    "/difficulty: nothing saved ticks all three, in a new session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: api.sends(msgCfg(msgChooseDifficulty, difficultyKeyboard(all))),
			svcMock: subscription(ctx, domain.Chat{ChatID: testChat}, true, nil),
			after:   session{step: stepEditDifficulty, msgID: 50, selected: all},
		},
		{
			name:    "/difficulty: unsubscribed keeps the existing session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: api.sends(msgCfg(msgNotSubscribed, nil)),
			svcMock: subscription(ctx, domain.Chat{}, false, nil),
			after:   timeStep,
		},
		{
			name:    "/difficulty: a failed send leaves no session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: sendFails,
			svcMock: subscription(ctx, domain.Chat{ChatID: testChat}, true, nil),
		},
		{
			name:    "/difficulty: a Subscription error sends nothing and keeps the session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: subscription(ctx, domain.Chat{}, false, diskFull),
			after:   timeStep,
		},
		{name: "text in a difficulty step is ignored", before: editDiff, update: message("hello"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice, after: editDiff},
		{
			name:    "a command in the time step is a command",
			before:  timeStep,
			update:  message("/today"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: sendToday,
			after:   timeStep,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			api.answered = nil          // a row that never answers cannot order after another row's answer
			apiMock := tt.apiMock(ctrl) // before svcMock, which may chain .After(api.answered)
			h := NewHandler(apiMock, tt.svcMock(ctrl), "TestBot")
			if tt.before.step != 0 {
				h.sessions.set(testChat, tt.before)
			}
			h.Handle(ctx, tt.update)
			if got := h.sessions.get(testChat); !reflect.DeepEqual(got, tt.after) {
				t.Errorf("session = %+v, want %+v", got, tt.after)
			}
		})
	}
}
