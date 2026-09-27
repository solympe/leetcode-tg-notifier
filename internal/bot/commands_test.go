package bot

import (
	"errors"
	"net/http"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

func TestHandleSetup(t *testing.T) {
	tests := []struct {
		name        string
		before      func(s *stateStore)
		senderMock  func(*gomock.Controller) *mocks.MocktelegramSender
		wantState   string
		wantMsgID   int
		wantPending pendingSetup
	}{
		{
			name:   "sets awaiting-time state and captures message ID",
			before: func(*stateStore) {},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseTime, setupTimeKeyboard()))).
					Return(tgbotapi.Message{MessageID: 42}, nil)
				return m
			},
			wantState: stateAwaitingTime,
			wantMsgID: 42,
		},
		{
			// The earlier prompt was abandoned; its buttons are stale from now on.
			name: "restarts from the time step",
			before: func(s *stateStore) {
				s.set(100, stateAwaitingTime)
				s.setSetupMsgID(100, 10)
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseTime, setupTimeKeyboard()))).
					Return(tgbotapi.Message{MessageID: 42}, nil)
				return m
			},
			wantState: stateAwaitingTime,
			wantMsgID: 42,
		},
		{
			name: "restarts from the timezone step",
			before: func(s *stateStore) {
				s.set(100, stateAwaitingTimezone)
				s.setPendingTime(100, "09:00")
				s.setSetupMsgID(100, 10)
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseTime, setupTimeKeyboard()))).
					Return(tgbotapi.Message{MessageID: 42}, nil)
				return m
			},
			wantState: stateAwaitingTime,
			wantMsgID: 42,
		},
		{
			name: "restarts from the difficulty step",
			before: func(s *stateStore) {
				s.set(100, stateAwaitingDifficulty)
				s.setPendingTime(100, "09:00")
				s.setPendingTz(100, "UTC")
				s.setPendingDifficulties(100, []string{"Easy"})
				s.setSetupMsgID(100, 10)
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseTime, setupTimeKeyboard()))).
					Return(tgbotapi.Message{MessageID: 42}, nil)
				return m
			},
			wantState: stateAwaitingTime,
			wantMsgID: 42,
		},
		{
			name: "restarts from a /difficulty session",
			before: func(s *stateStore) {
				s.set(100, stateEditingDifficulty)
				s.setPendingDifficulties(100, []string{"Hard"})
				s.setSetupMsgID(100, 10)
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseTime, setupTimeKeyboard()))).
					Return(tgbotapi.Message{MessageID: 42}, nil)
				return m
			},
			wantState: stateAwaitingTime,
			wantMsgID: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			chatID := int64(100)
			b := New(
				tt.senderMock(ctrl),
				"TestBot",
				mocks.NewMockchatStore(ctrl),
				mocks.NewMocklcFetcher(ctrl),
				mocks.NewMocktaskScheduler(ctrl),
			)
			tt.before(b.states)

			b.handleSetup(chatID)

			if got := b.states.get(chatID); got != tt.wantState {
				t.Errorf("state: got %q, want %q", got, tt.wantState)
			}
			if got := b.states.getSetupMsgID(chatID); got != tt.wantMsgID {
				t.Errorf("msgID: got %d, want %d", got, tt.wantMsgID)
			}
			if got := b.states.clearAndGetPending(chatID); !samePending(got, tt.wantPending) {
				t.Errorf("pending: got %+v, want %+v", got, tt.wantPending)
			}
		})
	}
}

// TestHandleSetupFailedSend checks that a /setup whose prompt is not sent
// leaves no session behind and that the next /setup starts normally.
func TestHandleSetupFailedSend(t *testing.T) {
	// failThenPrompt fails the first /setup prompt and sends the second as
	// message 42.
	failThenPrompt := func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
		m := mocks.NewMocktelegramSender(ctrl)
		gomock.InOrder(
			m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseTime, setupTimeKeyboard()))).
				Return(tgbotapi.Message{}, errors.New("network")),
			m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseTime, setupTimeKeyboard()))).
				Return(tgbotapi.Message{MessageID: 42}, nil),
		)
		return m
	}

	tests := []struct {
		name       string
		before     func(s *stateStore)
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
	}{
		{
			name:       "without a session",
			before:     func(*stateStore) {},
			senderMock: failThenPrompt,
		},
		{
			name: "from the timezone step",
			before: func(s *stateStore) {
				s.set(100, stateAwaitingTimezone)
				s.setPendingTime(100, "09:00")
				s.setSetupMsgID(100, 10)
			},
			senderMock: failThenPrompt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			chatID := int64(100)
			b := New(
				tt.senderMock(ctrl),
				"TestBot",
				mocks.NewMockchatStore(ctrl),
				mocks.NewMocklcFetcher(ctrl),
				mocks.NewMocktaskScheduler(ctrl),
			)
			tt.before(b.states)

			b.handleSetup(chatID)

			if got := b.states.get(chatID); got != "" {
				t.Errorf("state after failed send: got %q, want none", got)
			}
			if got := b.states.getSetupMsgID(chatID); got != 0 {
				t.Errorf("msgID after failed send: got %d, want 0", got)
			}
			if got := b.states.getPendingTime(chatID); got != "" {
				t.Errorf("pending time after failed send: got %q, want none", got)
			}

			b.handleSetup(chatID)

			if got := b.states.get(chatID); got != stateAwaitingTime {
				t.Errorf("state after retry: got %q, want %q", got, stateAwaitingTime)
			}
			if got := b.states.getSetupMsgID(chatID); got != 42 {
				t.Errorf("msgID after retry: got %d, want 42", got)
			}
		})
	}
}

func TestHandleStatus(t *testing.T) {
	tests := []struct {
		name       string
		storeMock  func(*gomock.Controller) *mocks.MockchatStore
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
	}{
		{
			name: "no subscription",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(100, msgStatusInactive))).Return(tgbotapi.Message{}, nil)
				return m
			},
		},
		{
			name: "shows the subscribed difficulties",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{
					ChatID:       100,
					NotifyTime:   "09:00",
					Timezone:     "UTC",
					Difficulties: []string{"Easy", "Medium"},
				}, true)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(100,
					"✅ Subscription active\nTime: <b>09:00</b> (UTC)\nDifficulty: <b>Easy, Medium</b>"))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
		},
		{
			name: "no difficulties means any",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{ChatID: 100, NotifyTime: "21:00", Timezone: "Asia/Dubai"}, true)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(100,
					"✅ Subscription active\nTime: <b>21:00</b> (Asia/Dubai)\nDifficulty: <b>Any</b>"))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			b := New(
				tt.senderMock(ctrl),
				"TestBot",
				tt.storeMock(ctrl),
				mocks.NewMocklcFetcher(ctrl),
				mocks.NewMocktaskScheduler(ctrl),
			)

			b.handleStatus(100)
		})
	}
}

func TestHandleUnsubscribe(t *testing.T) {
	tests := []struct {
		name       string
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock  func(*gomock.Controller) *mocks.MockchatStore
		schedMock  func(*gomock.Controller) *mocks.MocktaskScheduler
	}{
		{
			name: "deletes config and removes schedule",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Delete(int64(100)).Return(nil)
				return m
			},
			schedMock: func(ctrl *gomock.Controller) *mocks.MocktaskScheduler {
				m := mocks.NewMocktaskScheduler(ctrl)
				m.EXPECT().Remove(int64(100))
				return m
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			b := New(
				tt.senderMock(ctrl),
				"TestBot",
				tt.storeMock(ctrl),
				mocks.NewMocklcFetcher(ctrl),
				tt.schedMock(ctrl),
			)

			b.handleUnsubscribe(100)
		})
	}
}

// fetchesDaily expects a single FetchDaily returning today's daily of
// difficulty; a random fetch fails the test.
func fetchesDaily(difficulty string) func(*gomock.Controller) *mocks.MocklcFetcher {
	return func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
		m := mocks.NewMocklcFetcher(ctrl)
		m.EXPECT().FetchDaily().Return(testDaily(difficulty), nil)
		return m
	}
}

// TestHandleDaily checks that /daily sends the official daily whatever the
// chat's difficulty. The store mocks expect nothing but the removal of a chat
// that blocked the bot, so /daily neither reads nor saves the pick of the day.
func TestHandleDaily(t *testing.T) {
	tests := []struct {
		name       string
		lcMock     func(*gomock.Controller) *mocks.MocklcFetcher
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock  func(*gomock.Controller) *mocks.MockchatStore
		schedMock  func(*gomock.Controller) *mocks.MocktaskScheduler
	}{
		{
			// The chat is subscribed to Easy, so /today would send a random
			// Easy problem instead of this Hard daily.
			name:   "daily of an unsubscribed difficulty is sent as is",
			lcMock: fetchesDaily("Hard"),
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(leetcode.FormatProblem(testDaily("Hard"))))).
					Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: mocks.NewMockchatStore,
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name:   "chat without a subscription gets the daily",
			lcMock: fetchesDaily("Medium"),
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(leetcode.FormatProblem(testDaily("Medium"))))).
					Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
			storeMock: mocks.NewMockchatStore,
			schedMock: mocks.NewMocktaskScheduler,
		},
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
			name:   "bot blocked by user removes subscription",
			lcMock: fetchesDaily("Hard"),
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(leetcode.FormatProblem(testDaily("Hard"))))).
					Return(tgbotapi.Message{}, &tgbotapi.Error{
						Code:    http.StatusForbidden,
						Message: "Forbidden: bot was blocked by the user",
					})
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Delete(testChatID).Return(nil)
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
			name:   "non-403 send error keeps subscription intact",
			lcMock: fetchesDaily("Hard"),
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(problemMsg(leetcode.FormatProblem(testDaily("Hard"))))).
					Return(tgbotapi.Message{}, &tgbotapi.Error{Code: http.StatusTooManyRequests})
				return m
			},
			storeMock: mocks.NewMockchatStore,
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
			// A pick of the day is in flight for the chat; /daily must not wait for it.
			unlock := b.pickLocks.lock(testChatID)

			done := make(chan struct{})
			go func() {
				defer close(done)
				b.handleDaily(testChatID)
			}()
			select {
			case <-done:
				unlock()
			case <-time.After(time.Second):
				t.Fatal("handleDaily waited for the chat's pick lock")
			}
		})
	}
}

func TestDailyEntryPoints(t *testing.T) {
	chat := &tgbotapi.Chat{ID: testChatID}
	daily := problemMsg(leetcode.FormatProblem(testDaily("Hard")))

	sendsDaily := func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
		m := mocks.NewMocktelegramSender(ctrl)
		m.EXPECT().Send(gomock.Eq(daily)).Return(tgbotapi.Message{MessageID: 1}, nil)
		return m
	}

	tests := []struct {
		name       string
		update     tgbotapi.Update
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		lcMock     func(*gomock.Controller) *mocks.MocklcFetcher
	}{
		{
			name:       "/daily command",
			update:     tgbotapi.Update{Message: &tgbotapi.Message{Chat: chat, Text: "/daily"}},
			senderMock: sendsDaily,
			lcMock:     fetchesDaily("Hard"),
		},
		{
			name:       "/daily command addressed to the bot",
			update:     tgbotapi.Update{Message: &tgbotapi.Message{Chat: chat, Text: "/daily@TestBot"}},
			senderMock: sendsDaily,
			lcMock:     fetchesDaily("Hard"),
		},
		{
			name:   "LeetCode daily button on the start keyboard",
			update: tgbotapi.Update{CallbackQuery: pressButton("cb1", testChatID, 3, cbCmdDaily)},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				gomock.InOrder(
					m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", ""))).Return(answered, nil),
					m.EXPECT().Send(gomock.Eq(daily)).Return(tgbotapi.Message{MessageID: 1}, nil),
				)
				return m
			},
			lcMock: fetchesDaily("Hard"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			b := New(
				tt.senderMock(ctrl),
				"TestBot",
				mocks.NewMockchatStore(ctrl),
				tt.lcMock(ctrl),
				mocks.NewMocktaskScheduler(ctrl),
			)

			b.handleMessage(tt.update)
		})
	}
}
