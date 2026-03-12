package bot

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
)

const testChatID = int64(794812315)

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
				m.EXPECT().Send(gomock.Any()).DoAndReturn(func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
					msg, ok := c.(tgbotapi.MessageConfig)
					if !ok {
						t.Errorf("expected MessageConfig, got %T", c)
					}
					if msg.Text != msgFetchFailed {
						t.Errorf("text: got %q, want %q", msg.Text, msgFetchFailed)
					}
					return tgbotapi.Message{}, nil
				})
				return m
			},
			storeMock: mocks.NewMockchatStore,
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "bot blocked by user removes subscription",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(&leetcode.Problem{Title: "Two Sum"}, nil)
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
			name: "non-403 send error keeps subscription intact",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(&leetcode.Problem{Title: "Two Sum"}, nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, &tgbotapi.Error{Code: http.StatusTooManyRequests})
				return m
			},
			storeMock: mocks.NewMockchatStore,
			schedMock: mocks.NewMocktaskScheduler,
		},
		{
			name: "success sends the problem message",
			lcMock: func(ctrl *gomock.Controller) *mocks.MocklcFetcher {
				m := mocks.NewMocklcFetcher(ctrl)
				m.EXPECT().FetchDaily().Return(&leetcode.Problem{Title: "Two Sum", Difficulty: "Easy"}, nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{MessageID: 1}, nil)
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

			b.SendDailyProblem(testChatID)
		})
	}
}
