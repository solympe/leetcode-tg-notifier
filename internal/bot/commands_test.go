package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
)

func TestHandleSetup(t *testing.T) {
	tests := []struct {
		name       string
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		wantState  string
		wantMsgID  int
	}{
		{
			name: "sets awaiting-time state and captures message ID",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).DoAndReturn(func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
					msg, ok := c.(tgbotapi.MessageConfig)
					if !ok {
						t.Errorf("expected MessageConfig, got %T", c)
					}
					if msg.Text != msgChooseTime {
						t.Errorf("text: got %q, want %q", msg.Text, msgChooseTime)
					}
					return tgbotapi.Message{MessageID: 42}, nil
				})
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

			b.handleSetup(chatID)

			if got := b.states.get(chatID); got != tt.wantState {
				t.Errorf("state: got %q, want %q", got, tt.wantState)
			}
			if got := b.states.getSetupMsgID(chatID); got != tt.wantMsgID {
				t.Errorf("msgID: got %d, want %d", got, tt.wantMsgID)
			}
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
