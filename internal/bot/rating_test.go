package bot

import (
	"fmt"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

func makeCallbackQuery(cbID string, userID int64, firstName string, chatID int64) *tgbotapi.CallbackQuery {
	return &tgbotapi.CallbackQuery{
		ID:   cbID,
		From: &tgbotapi.User{ID: userID, FirstName: firstName},
		Message: &tgbotapi.Message{
			MessageID: 1,
			Chat:      &tgbotapi.Chat{ID: chatID},
		},
		Data: cbCmdDone,
	}
}

func TestHandleDone(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")

	tests := []struct {
		name       string
		storeMock  func(*gomock.Controller) *mocks.MockchatStore
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		cb         *tgbotapi.CallbackQuery
		chatID     int64
	}{
		{
			name:   "first click counts",
			chatID: 100,
			cb:     makeCallbackQuery("cb1", 1, "Alice", 100),
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(
					storage.ChatConfig{ChatID: 100, NotifyTime: "09:00", Timezone: "UTC"}, true,
				)
				m.EXPECT().Set(storage.ChatConfig{
					ChatID:     100,
					NotifyTime: "09:00",
					Timezone:   "UTC",
					Members: map[string]storage.UserStat{
						"1": {Name: "Alice", Count: 1, LastSolvedDate: today},
					},
				}).Return(nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(tgbotapi.NewCallback("cb1", fmt.Sprintf("✅ Counted! Your total: %d", 1))).
					Return(&tgbotapi.APIResponse{Ok: true}, nil)
				return m
			},
		},
		{
			name:   "second click same day is rejected",
			chatID: 100,
			cb:     makeCallbackQuery("cb2", 1, "Alice", 100),
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{
					ChatID:     100,
					NotifyTime: "09:00",
					Timezone:   "UTC",
					Members: map[string]storage.UserStat{
						"1": {Name: "Alice", Count: 3, LastSolvedDate: today},
					},
				}, true)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(tgbotapi.NewCallback("cb2", "Already counted today!")).
					Return(&tgbotapi.APIResponse{Ok: true}, nil)
				return m
			},
		},
		{
			name:   "new day allows count",
			chatID: 100,
			cb:     makeCallbackQuery("cb3", 1, "Alice", 100),
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{
					ChatID:     100,
					NotifyTime: "09:00",
					Timezone:   "UTC",
					Members: map[string]storage.UserStat{
						"1": {Name: "Alice", Count: 5, LastSolvedDate: yesterday},
					},
				}, true)
				m.EXPECT().Set(storage.ChatConfig{
					ChatID:     100,
					NotifyTime: "09:00",
					Timezone:   "UTC",
					Members: map[string]storage.UserStat{
						"1": {Name: "Alice", Count: 6, LastSolvedDate: today},
					},
				}).Return(nil)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(tgbotapi.NewCallback("cb3", fmt.Sprintf("✅ Counted! Your total: %d", 6))).
					Return(&tgbotapi.APIResponse{Ok: true}, nil)
				return m
			},
		},
		{
			name:   "no config answers not subscribed",
			chatID: 999,
			cb:     makeCallbackQuery("cb4", 1, "Alice", 999),
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(999)).Return(storage.ChatConfig{}, false)
				return m
			},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(tgbotapi.NewCallback("cb4", msgNotSubscribed)).
					Return(&tgbotapi.APIResponse{Ok: true}, nil)
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

			b.handleDone(tt.cb, tt.chatID, 1)
		})
	}
}

func TestHandleRating(t *testing.T) {
	tests := []struct {
		name         string
		storeMock    func(*gomock.Controller) *mocks.MockchatStore
		wantText     string
		wantOrdering []string
	}{
		{
			name: "empty - no members",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(
					storage.ChatConfig{ChatID: 100, NotifyTime: "09:00", Timezone: "UTC"}, true,
				)
				return m
			},
			wantText: msgRatingEmpty,
		},
		{
			name: "single user - name not shown",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{
					ChatID:     100,
					NotifyTime: "09:00",
					Timezone:   "UTC",
					Members:    map[string]storage.UserStat{"1": {Name: "Alice", Count: 7}},
				}, true)
				return m
			},
		},
		{
			name: "multiple users sorted by count descending",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{
					ChatID:     100,
					NotifyTime: "09:00",
					Timezone:   "UTC",
					Members: map[string]storage.UserStat{
						"1": {Name: "Alice", Count: 5},
						"2": {Name: "Bob", Count: 12},
						"3": {Name: "Charlie", Count: 3},
					},
				}, true)
				return m
			},
			wantOrdering: []string{"Bob", "Alice", "Charlie"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			var sentText string
			sender := mocks.NewMocktelegramSender(ctrl)
			sender.EXPECT().Send(gomock.Any()).DoAndReturn(func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
				if msg, ok := c.(tgbotapi.MessageConfig); ok {
					sentText = msg.Text
				}
				return tgbotapi.Message{}, nil
			})

			b := New(
				sender,
				"TestBot",
				tt.storeMock(ctrl),
				mocks.NewMocklcFetcher(ctrl),
				mocks.NewMocktaskScheduler(ctrl),
			)

			b.handleRating(100)

			if tt.wantText != "" && sentText != tt.wantText {
				t.Errorf("text: got %q, want %q", sentText, tt.wantText)
			}
			for i := 1; i < len(tt.wantOrdering); i++ {
				prev, curr := tt.wantOrdering[i-1], tt.wantOrdering[i]
				if findPos(sentText, prev) > findPos(sentText, curr) {
					t.Errorf("expected %q before %q in rating, got: %q", prev, curr, sentText)
				}
			}
			if tt.name == "single user - name not shown" {
				if findPos(sentText, "Alice") != -1 {
					t.Errorf("single-user rating should not contain name, got %q", sentText)
				}
				if findPos(sentText, "7") == -1 {
					t.Errorf("single-user rating should contain count, got %q", sentText)
				}
			}
		})
	}
}

func findPos(s, substr string) int {
	for i := range s {
		if i+len(substr) <= len(s) && s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
