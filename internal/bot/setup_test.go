package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

func TestSetupFlow(t *testing.T) {
	tests := []struct {
		name        string
		senderMock  func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock   func(*gomock.Controller) *mocks.MockchatStore
		schedMock   func(*gomock.Controller) *mocks.MocktaskScheduler
		inputTime   string
		inputTz     string
		wantCleared bool
	}{
		{
			name:        "valid time and UTC timezone",
			inputTime:   "09:00",
			inputTz:     "UTC",
			wantCleared: true,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{MessageID: 42}, nil) // handleSetup
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, nil)              // handleAwaitingTime
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, nil)              // handleAwaitingTimezone
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Set(storage.ChatConfig{
					ChatID:     100,
					NotifyTime: "09:00",
					Timezone:   "UTC",
				}).Return(nil)
				return m
			},
			schedMock: func(ctrl *gomock.Controller) *mocks.MocktaskScheduler {
				m := mocks.NewMocktaskScheduler(ctrl)
				m.EXPECT().Schedule(int64(100), storage.ChatConfig{
					ChatID:     100,
					NotifyTime: "09:00",
					Timezone:   "UTC",
				}).Return(nil)
				return m
			},
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

			b.handleSetup(chatID)
			b.handleAwaitingTime(chatID, tt.inputTime)
			b.handleAwaitingTimezone(chatID, tt.inputTz)

			if tt.wantCleared && b.states.get(chatID) != "" {
				t.Errorf("state should be cleared after setup, got %q", b.states.get(chatID))
			}
		})
	}
}

func TestHandleAwaitingTime(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		wantState  string
	}{
		{
			name:  "invalid format stays in awaiting-time and shows error",
			input: "9am",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).DoAndReturn(func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
					edit, ok := c.(tgbotapi.EditMessageTextConfig)
					if !ok {
						t.Errorf("expected EditMessageTextConfig, got %T", c)
					}
					if edit.Text != msgInvalidTime {
						t.Errorf("text: got %q, want %q", edit.Text, msgInvalidTime)
					}
					if edit.ReplyMarkup == nil {
						t.Error("time keyboard should be re-attached on invalid input")
					}
					return tgbotapi.Message{}, nil
				})
				return m
			},
			wantState: stateAwaitingTime,
		},
		{
			name:  "valid format transitions to awaiting-timezone",
			input: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, nil)
				return m
			},
			wantState: stateAwaitingTimezone,
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

			b.states.set(chatID, stateAwaitingTime)
			b.states.setSetupMsgID(chatID, 10)

			b.handleAwaitingTime(chatID, tt.input)

			if got := b.states.get(chatID); got != tt.wantState {
				t.Errorf("state: got %q, want %q", got, tt.wantState)
			}
		})
	}
}

func TestHandleAwaitingTimezone(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock  func(*gomock.Controller) *mocks.MockchatStore
		schedMock  func(*gomock.Controller) *mocks.MocktaskScheduler
		wantState  string
	}{
		{
			name:  "invalid timezone stays in awaiting-timezone and shows error",
			input: "Not/ATimezone",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).DoAndReturn(func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
					edit, ok := c.(tgbotapi.EditMessageTextConfig)
					if !ok {
						t.Errorf("expected EditMessageTextConfig, got %T", c)
					}
					if edit.Text != msgInvalidTz {
						t.Errorf("text: got %q, want %q", edit.Text, msgInvalidTz)
					}
					if edit.ReplyMarkup == nil {
						t.Error("timezone keyboard should be re-attached on invalid input")
					}
					return tgbotapi.Message{}, nil
				})
				return m
			},
			storeMock: mocks.NewMockchatStore,
			schedMock: mocks.NewMocktaskScheduler,
			wantState: stateAwaitingTimezone,
		},
		{
			name:  "valid timezone finishes setup and clears state",
			input: "UTC",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Set(gomock.Any()).Return(nil)
				return m
			},
			schedMock: func(ctrl *gomock.Controller) *mocks.MocktaskScheduler {
				m := mocks.NewMocktaskScheduler(ctrl)
				m.EXPECT().Schedule(gomock.Any(), gomock.Any()).Return(nil)
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
				tt.schedMock(ctrl),
			)

			b.states.set(chatID, stateAwaitingTimezone)
			b.states.setPendingTime(chatID, "09:00")
			b.states.setSetupMsgID(chatID, 10)

			b.handleAwaitingTimezone(chatID, tt.input)

			if got := b.states.get(chatID); got != tt.wantState {
				t.Errorf("state: got %q, want %q", got, tt.wantState)
			}
		})
	}
}
