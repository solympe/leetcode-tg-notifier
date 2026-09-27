package bot

import (
	"errors"
	"fmt"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

// editSession puts chat 100 into a /difficulty session on message 55.
func editSession(selected ...string) func(*stateStore) {
	return func(s *stateStore) {
		s.set(100, stateEditingDifficulty)
		s.setPendingDifficulties(100, selected)
		s.setSetupMsgID(100, 55)
	}
}

// setupSession puts chat 100 into the difficulty step of /setup on message 55.
func setupSession(notifyTime, tz string, selected ...string) func(*stateStore) {
	return func(s *stateStore) {
		s.set(100, stateAwaitingDifficulty)
		if notifyTime != "" {
			s.setPendingTime(100, notifyTime)
		}
		if tz != "" {
			s.setPendingTz(100, tz)
		}
		s.setPendingDifficulties(100, selected)
		s.setSetupMsgID(100, 55)
	}
}

func TestFormatDifficulties(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{name: "nil means any", in: nil, want: "Any"},
		{name: "empty means any", in: []string{}, want: "Any"},
		{name: "single", in: []string{"Hard"}, want: "Hard"},
		{name: "several", in: []string{"Easy", "Medium"}, want: "Easy, Medium"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDifficulties(tt.in); got != tt.want {
				t.Errorf("formatDifficulties(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHandleDifficulty(t *testing.T) {
	tests := []struct {
		name        string
		before      func(s *stateStore)
		storeMock   func(*gomock.Controller) *mocks.MockchatStore
		senderMock  func(*gomock.Controller) *mocks.MocktelegramSender
		wantState   string
		wantMsgID   int
		wantPending pendingSetup
	}{
		{
			name:   "not subscribed",
			before: func(*stateStore) {},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(100, msgNotSubscribed))).Return(tgbotapi.Message{}, nil)
				return m
			},
			wantState: "",
		},
		{
			name:   "keyboard reflects the saved selection",
			before: func(*stateStore) {},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{ChatID: 100, Difficulties: []string{"Medium", "Hard"}}, true)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseDifficulty, difficultyKeyboard([]string{"Medium", "Hard"})))).
					Return(tgbotapi.Message{MessageID: 55}, nil)
				return m
			},
			wantState:   stateEditingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{difficulties: []string{"Medium", "Hard"}},
		},
		{
			name:   "no saved selection starts with everything ticked",
			before: func(*stateStore) {},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{ChatID: 100}, true)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseDifficulty, difficultyKeyboard(leetcode.AllDifficulties())))).
					Return(tgbotapi.Message{MessageID: 55}, nil)
				return m
			},
			wantState:   stateEditingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{difficulties: leetcode.AllDifficulties()},
		},
		{
			name: "discards an in-progress setup",
			before: func(s *stateStore) {
				s.set(100, stateAwaitingTimezone)
				s.setPendingTime(100, "09:00")
				s.setSetupMsgID(100, 10)
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{ChatID: 100, Difficulties: []string{"Easy"}}, true)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseDifficulty, difficultyKeyboard([]string{"Easy"})))).
					Return(tgbotapi.Message{MessageID: 55}, nil)
				return m
			},
			wantState:   stateEditingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{difficulties: []string{"Easy"}},
		},
		{
			name:   "send error clears the session",
			before: func(*stateStore) {},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{ChatID: 100, Difficulties: []string{"Easy"}}, true)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, errors.New("network"))
				return m
			},
			wantState: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			chatID := int64(100)
			b := New(
				tt.senderMock(ctrl),
				"TestBot",
				tt.storeMock(ctrl),
				mocks.NewMocklcFetcher(ctrl),
				mocks.NewMocktaskScheduler(ctrl),
			)
			tt.before(b.states)

			b.handleDifficulty(chatID)

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

func TestDifficultyCallbacks(t *testing.T) {
	members := map[string]storage.UserStat{"1": {Name: "Alice", Count: 4, LastSolvedDate: "2026-09-26"}}

	tests := []struct {
		name        string
		before      func(s *stateStore)
		data        string
		pressedMsg  int
		senderMock  func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock   func(*gomock.Controller) *mocks.MockchatStore
		schedMock   func(*gomock.Controller) *mocks.MocktaskScheduler
		wantState   string
		wantMsgID   int
		wantPending pendingSetup
	}{
		{
			name:       "toggle ticks the difficulty and updates the keyboard",
			before:     editSession("Easy"),
			data:       cbPrefixDiff + "Hard",
			pressedMsg: 55,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(tgbotapi.NewEditMessageReplyMarkup(100, 55, difficultyKeyboard([]string{"Easy", "Hard"})))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			schedMock:   mocks.NewMocktaskScheduler,
			wantState:   stateEditingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{difficulties: []string{"Easy", "Hard"}},
		},
		{
			name:       "toggle on a stale message is answered as stale without changes",
			before:     editSession("Easy"),
			data:       cbPrefixDiff + "Hard",
			pressedMsg: 7,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			schedMock:   mocks.NewMocktaskScheduler,
			wantState:   stateEditingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{difficulties: []string{"Easy"}},
		},
		{
			name:       "toggle without a session is answered as stale",
			before:     func(*stateStore) {},
			data:       cbPrefixDiff + "Hard",
			pressedMsg: 55,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock: mocks.NewMockchatStore,
			schedMock: mocks.NewMocktaskScheduler,
			wantState: "",
		},
		{
			name: "toggle in another setup step is answered as stale",
			before: func(s *stateStore) {
				s.set(100, stateAwaitingTimezone)
				s.setPendingTime(100, "09:00")
				s.setSetupMsgID(100, 55)
			},
			data:       cbPrefixDiff + "Hard",
			pressedMsg: 55,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			schedMock:   mocks.NewMocktaskScheduler,
			wantState:   stateAwaitingTimezone,
			wantMsgID:   55,
			wantPending: pendingSetup{notifyTime: "09:00"},
		},
		{
			name:       "unknown difficulty is ignored",
			before:     editSession("Easy"),
			data:       cbPrefixDiff + "Extreme",
			pressedMsg: 55,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", ""))).Return(answered, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			schedMock:   mocks.NewMocktaskScheduler,
			wantState:   stateEditingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{difficulties: []string{"Easy"}},
		},
		{
			name:       "save on a stale message is answered as stale without saving",
			before:     setupSession("09:00", "UTC", "Easy"),
			data:       cbCmdDiffSave,
			pressedMsg: 7,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			schedMock:   mocks.NewMocktaskScheduler,
			wantState:   stateAwaitingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{notifyTime: "09:00", timezone: "UTC", difficulties: []string{"Easy"}},
		},
		{
			// The first tap saved and ended the session.
			name:       "double-tapped save is answered as stale",
			before:     func(*stateStore) {},
			data:       cbCmdDiffSave,
			pressedMsg: 55,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock: mocks.NewMockchatStore,
			schedMock: mocks.NewMocktaskScheduler,
			wantState: "",
		},
		{
			name:       "save with nothing selected keeps the session",
			before:     editSession(),
			data:       cbCmdDiffSave,
			pressedMsg: 55,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgPickAtLeastOne))).Return(answered, nil)
				return m
			},
			storeMock: mocks.NewMockchatStore,
			schedMock: mocks.NewMocktaskScheduler,
			wantState: stateEditingDifficulty,
			wantMsgID: 55,
		},
		{
			name:       "save in setup finishes the subscription",
			before:     setupSession("09:00", "UTC", "Easy"),
			data:       cbCmdDiffSave,
			pressedMsg: 55,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(editText(100, 55, fmt.Sprintf(msgAllSet, "09:00", "UTC", "Easy")))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Update(gomock.Eq(int64(100)), gomock.Any()).Return(false, nil),
					m.EXPECT().Set(gomock.Eq(storage.ChatConfig{
						ChatID:       100,
						NotifyTime:   "09:00",
						Timezone:     "UTC",
						Difficulties: []string{"Easy"},
					})).Return(nil),
				)
				return m
			},
			schedMock: func(ctrl *gomock.Controller) *mocks.MocktaskScheduler {
				m := mocks.NewMocktaskScheduler(ctrl)
				m.EXPECT().Schedule(int64(100), gomock.Eq(storage.ChatConfig{
					ChatID:       100,
					NotifyTime:   "09:00",
					Timezone:     "UTC",
					Difficulties: []string{"Easy"},
				})).Return(nil)
				return m
			},
			wantState: "",
		},
		{
			name:       "save in setup without a timezone expires",
			before:     setupSession("09:00", "", "Easy"),
			data:       cbCmdDiffSave,
			pressedMsg: 55,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(textMsg(100, msgSessionExpired))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: mocks.NewMockchatStore,
			schedMock: mocks.NewMocktaskScheduler,
			wantState: "",
		},
		{
			// No Schedule expected — the notification time does not change.
			name:       "save in /difficulty updates only the difficulties and keeps the pick",
			before:     editSession("Easy", "Hard"),
			data:       cbCmdDiffSave,
			pressedMsg: 55,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(editText(100, 55, fmt.Sprintf(msgDifficultyUpdated, "Easy, Hard")))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(updateVia(m, storage.ChatConfig{
					ChatID:       100,
					NotifyTime:   "07:30",
					Timezone:     "Europe/Moscow",
					Members:      members,
					Difficulties: []string{"Medium"},
					DailyPick:    testPick(testDailyDate),
				}))
				m.EXPECT().Set(gomock.Eq(storage.ChatConfig{
					ChatID:       100,
					NotifyTime:   "07:30",
					Timezone:     "Europe/Moscow",
					Members:      members,
					Difficulties: []string{"Easy", "Hard"},
					DailyPick:    testPick(testDailyDate),
				})).Return(nil)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
			wantState: "",
		},
		{
			// No Set expected — an unsubscribed chat is not re-created.
			name:       "save in /difficulty after unsubscribing replaces the keyboard with not subscribed",
			before:     editSession("Easy"),
			data:       cbCmdDiffSave,
			pressedMsg: 55,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(editText(100, 55, msgNotSubscribed))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(int64(100)), gomock.Any()).Return(false, nil)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
			wantState: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			chatID := int64(100)
			b := New(
				tt.senderMock(ctrl),
				"TestBot",
				tt.storeMock(ctrl),
				mocks.NewMocklcFetcher(ctrl),
				tt.schedMock(ctrl),
			)
			tt.before(b.states)

			b.handleCallback(pressButton("cb1", chatID, tt.pressedMsg, tt.data))

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

func TestDifficultyEntryPoints(t *testing.T) {
	chat := &tgbotapi.Chat{ID: 100}

	tests := []struct {
		name       string
		update     tgbotapi.Update
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock  func(*gomock.Controller) *mocks.MockchatStore
	}{
		{
			name:   "/difficulty command",
			update: tgbotapi.Update{Message: &tgbotapi.Message{Chat: chat, Text: "/difficulty"}},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(100, msgNotSubscribed))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false)
				return m
			},
		},
		{
			name:   "/difficulty command addressed to the bot",
			update: tgbotapi.Update{Message: &tgbotapi.Message{Chat: chat, Text: "/difficulty@TestBot"}},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(100, msgNotSubscribed))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false)
				return m
			},
		},
		{
			name:   "difficulty button on the start keyboard",
			update: tgbotapi.Update{CallbackQuery: pressButton("cb1", 100, 3, cbCmdDifficulty)},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(textMsg(100, msgNotSubscribed))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false)
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

			b.handleMessage(tt.update)
		})
	}
}
