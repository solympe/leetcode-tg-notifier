package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
)

func TestHandle(t *testing.T) {
	ctx := t.Context()
	chat := &tgbotapi.Chat{ID: 100}

	tests := []struct {
		name       string
		update     tgbotapi.Update
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
	}{
		{
			name:   "message goes to its command",
			update: tgbotapi.Update{UpdateID: 1, Message: &tgbotapi.Message{Chat: chat, Text: "/about"}},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(100, msgAbout))).Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
		},
		{
			name:   "callback goes to the callback router",
			update: tgbotapi.Update{UpdateID: 2, CallbackQuery: &tgbotapi.CallbackQuery{ID: "cb1", Data: cbCmdToday}},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMessageExpired))).Return(answered, nil)
				return m
			},
		},
		{
			name:       "update without a message or callback is ignored",
			update:     tgbotapi.Update{UpdateID: 3},
			senderMock: mocks.NewMocktelegramSender,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			b := New(
				tt.senderMock(ctrl),
				"TestBot",
				mocks.NewMockchatStore(ctrl),
				mocks.NewMocklcFetcher(ctrl),
				mocks.NewMocktaskScheduler(ctrl),
			)

			b.Handle(ctx, tt.update)
		})
	}
}
