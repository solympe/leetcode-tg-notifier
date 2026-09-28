package telegram

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/telegram/mocks"
)

func TestDialog(t *testing.T) {
	ctx := t.Context()
	var api apiCalls
	const prompt = 42 // the dialog message whose buttons are live
	all := []string{"Easy", "Medium", "Hard"}
	diskFull := errors.New("disk full")

	timeStep := session{step: stepTime, msgID: prompt}
	tzStep := session{step: stepTimezone, msgID: prompt, notifyTime: "09:00"}
	setupDiff := session{step: stepSetupDifficulty, msgID: prompt, notifyTime: "09:00", timezone: "Europe/Moscow", selected: []string{"Easy", "Medium"}}
	editDiff := session{step: stepEditDifficulty, msgID: prompt, selected: []string{"Easy"}}
	tzPrompt := editCfg(prompt, fmt.Sprintf(msgChooseTz, "09:00"), tzKeyboard())
	allSet := editCfg(prompt, fmt.Sprintf(msgAllSet, "09:00", "Europe/Moscow", "Easy, Medium"), nil)
	updated := editCfg(prompt, fmt.Sprintf(msgDifficultyUpdated, "Easy"), nil)

	subscription := func(c domain.Chat, ok bool, err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).Return(c, ok, err)
			return m
		}
	}
	subscribe := func(err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().Subscribe(gomock.Eq(ctx), gomock.Eq(testChat), gomock.Eq("09:00"), gomock.Eq("Europe/Moscow"),
				gomock.Eq([]string{"Easy", "Medium"})).After(api.answered).Return(err)
			return m
		}
	}
	setDifficulties := func(err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().SetDifficulties(gomock.Eq(ctx), gomock.Eq(testChat), gomock.Eq([]string{"Easy"})).After(api.answered).Return(err)
			return m
		}
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
		// the time step
		{
			name:    "typed time: the timezone step on the same message",
			before:  timeStep,
			update:  message("09:00"),
			apiMock: api.sends(tzPrompt),
			svcMock: mocks.NewMockservice,
			after:   tzStep,
		},
		{
			name:    "typed invalid time: asked again",
			before:  timeStep,
			update:  message("9:00"),
			apiMock: api.sends(editCfg(prompt, msgInvalidTime, timeKeyboard())),
			svcMock: mocks.NewMockservice,
			after:   timeStep,
		},
		{
			name:    "time button: answered, then the timezone step",
			before:  timeStep,
			update:  press(prompt, "time:09:00"),
			apiMock: api.answerThen(tzPrompt),
			svcMock: mocks.NewMockservice,
			after:   tzStep,
		},
		{name: "time button on another message has expired", before: timeStep, update: press(prompt-1, "time:09:00"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: timeStep},
		{name: "time button in another step has expired", before: tzStep, update: press(prompt, "time:07:00"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: tzStep},
		{name: "forged time:25:00 has expired", before: timeStep, update: press(prompt, "time:25:00"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: timeStep},
		{name: "time button without a session has expired", update: press(prompt, "time:09:00"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice},

		// the timezone step
		{
			name:    "typed timezone: the saved difficulties are ticked",
			before:  tzStep,
			update:  message("Europe/Moscow"),
			apiMock: api.sends(editCfg(prompt, msgChooseDifficulty, difficultyKeyboard([]string{"Easy", "Hard"}))),
			svcMock: subscription(domain.Chat{ChatID: testChat, Difficulties: []string{"Hard", "Easy"}}, true, nil),
			after:   session{step: stepSetupDifficulty, msgID: prompt, notifyTime: "09:00", timezone: "Europe/Moscow", selected: []string{"Easy", "Hard"}},
		},
		{name: "typed unknown timezone: asked again", before: tzStep, update: message("Mars/Base"), apiMock: api.sends(editCfg(prompt, msgInvalidTz, tzKeyboard())), svcMock: mocks.NewMockservice, after: tzStep},
		{name: "typed Local is not a timezone", before: tzStep, update: message("Local"), apiMock: api.sends(editCfg(prompt, msgInvalidTz, tzKeyboard())), svcMock: mocks.NewMockservice, after: tzStep},
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
		{name: "tz:Local has expired", before: tzStep, update: press(prompt, "tz:Local"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: tzStep},
		{name: "timezone button on another message has expired", before: tzStep, update: press(prompt-1, "tz:UTC"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: tzStep},
		{
			name:    "a Subscription error keeps the timezone step",
			before:  tzStep,
			update:  message("Europe/Moscow"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: subscription(domain.Chat{}, false, diskFull),
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
		{name: "diff:Insane is answered and changes nothing", before: editDiff, update: press(prompt, "diff:Insane"), apiMock: api.answer(""), svcMock: mocks.NewMockservice, after: editDiff},
		{name: "difficulty button on another message has expired", before: editDiff, update: press(prompt-1, "diff:Hard"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: editDiff},
		{name: "difficulty button in the time step has expired", before: timeStep, update: press(prompt, "diff:Hard"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: timeStep},
		{
			name:    "an empty Save asks for one and keeps the session",
			before:  session{step: stepEditDifficulty, msgID: prompt},
			update:  press(prompt, cbDiffSave),
			apiMock: api.answer(msgPickAtLeastOne),
			svcMock: mocks.NewMockservice,
			after:   session{step: stepEditDifficulty, msgID: prompt},
		},
		{name: "Save on another message has expired", before: editDiff, update: press(prompt-1, cbDiffSave), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: editDiff},
		{name: "Save without a session has expired", update: press(prompt, cbDiffSave), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice},

		// saves
		{name: "setup Save: answered, subscribed, All set", before: setupDiff, update: press(prompt, cbDiffSave), apiMock: api.answerThen(allSet), svcMock: subscribe(nil)},
		{name: "setup Save: a Subscribe error still shows All set", before: setupDiff, update: press(prompt, cbDiffSave), apiMock: api.answerThen(allSet), svcMock: subscribe(diskFull)},
		{name: "difficulty Save: answered, saved, updated", before: editDiff, update: press(prompt, cbDiffSave), apiMock: api.answerThen(updated), svcMock: setDifficulties(nil)},
		{
			name:    "difficulty Save: no longer subscribed",
			before:  editDiff,
			update:  press(prompt, cbDiffSave),
			apiMock: api.answerThen(editCfg(prompt, msgNotSubscribed, nil)),
			svcMock: setDifficulties(domain.ErrNotSubscribed),
		},
		{name: "difficulty Save: another error still shows updated", before: editDiff, update: press(prompt, cbDiffSave), apiMock: api.answerThen(updated), svcMock: setDifficulties(diskFull)},

		// sessions
		{
			name:    "/setup: the prompt starts a session",
			update:  message("/setup"),
			apiMock: api.sends(msgCfg(msgChooseTime, timeKeyboard())),
			svcMock: mocks.NewMockservice,
			after:   session{step: stepTime, msgID: 50},
		},
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
			name:    "/difficulty: the saved set, in a new session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: api.sends(msgCfg(msgChooseDifficulty, difficultyKeyboard([]string{"Hard"}))),
			svcMock: subscription(domain.Chat{ChatID: testChat, Difficulties: []string{"Hard"}}, true, nil),
			after:   session{step: stepEditDifficulty, msgID: 50, selected: []string{"Hard"}},
		},
		{
			name:    "/difficulty: nothing saved ticks all three",
			update:  message("/difficulty"),
			apiMock: api.sends(msgCfg(msgChooseDifficulty, difficultyKeyboard(all))),
			svcMock: subscription(domain.Chat{ChatID: testChat}, true, nil),
			after:   session{step: stepEditDifficulty, msgID: 50, selected: all},
		},
		{
			name:    "/difficulty: unsubscribed keeps the existing session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: api.sends(msgCfg(msgNotSubscribed, nil)),
			svcMock: subscription(domain.Chat{}, false, nil),
			after:   timeStep,
		},
		{
			name:    "/difficulty: a failed send leaves no session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: sendFails,
			svcMock: subscription(domain.Chat{ChatID: testChat}, true, nil),
		},
		{
			name:    "/difficulty: a Subscription error sends nothing and keeps the session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: subscription(domain.Chat{}, false, diskFull),
			after:   timeStep,
		},
		{name: "text in a difficulty step is ignored", before: editDiff, update: message("hello"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice, after: editDiff},
		{
			name:    "a command in the time step is a command",
			before:  timeStep,
			update:  message("/today"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().SendToday(gomock.Eq(ctx), gomock.Eq(testChat))
				return m
			},
			after: timeStep,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
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
