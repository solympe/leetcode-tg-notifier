package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/telegram/mocks"
)

const testChat = int64(100)

// okResp is a successful answerCallbackQuery response.
var okResp = &tgbotapi.APIResponse{Ok: true}

// msgCfg is the sendMessage request for testChat: HTML, with kb when not nil.
func msgCfg(text string, kb *tgbotapi.InlineKeyboardMarkup) tgbotapi.MessageConfig {
	msg := tgbotapi.NewMessage(testChat, text)
	msg.ParseMode = "HTML"
	if kb != nil {
		msg.ReplyMarkup = *kb
	}
	return msg
}

// editCfg is the editMessageText request for testChat: HTML, and a nil kb
// removes the keyboard.
func editCfg(msgID int, text string, kb *tgbotapi.InlineKeyboardMarkup) tgbotapi.EditMessageTextConfig {
	edit := tgbotapi.NewEditMessageText(testChat, msgID, text)
	edit.ParseMode = "HTML"
	edit.ReplyMarkup = kb
	return edit
}

func TestSender(t *testing.T) {
	ctx := t.Context()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	daily := domain.Pick{Problem: domain.Problem{
		Date:       "2026-09-28",
		ID:         "4",
		Title:      "Median of Two Sorted Arrays",
		Link:       "/problems/median-of-two-sorted-arrays/",
		Difficulty: "Hard",
		Tags:       []string{"Array", "Binary Search", "Divide and Conquer"},
	}}
	dailyText := "📅 LeetCode Daily — September 28, 2026\n\n" +
		"🔢 4. Median of Two Sorted Arrays\n💪 Difficulty: Hard\n🏷 Array, Binary Search, Divide and Conquer\n\n" +
		"🔗 https://leetcode.com/problems/median-of-two-sorted-arrays/"
	forbidden := &tgbotapi.Error{Code: http.StatusForbidden, Message: "Forbidden: bot was blocked by the user"}
	serverErr := &tgbotapi.Error{Code: http.StatusInternalServerError, Message: "Internal Server Error"}

	sendProblem := func(ctx context.Context, s *sender) error { return s.SendProblem(ctx, testChat, daily) }
	sendFetchFailed := func(ctx context.Context, s *sender) error { return s.SendFetchFailed(ctx, testChat) }

	tests := []struct {
		name     string
		ctx      context.Context
		call     func(context.Context, *sender) error
		apiMock  func(*gomock.Controller) *mocks.MockbotAPI
		wantErr  error // matched with errors.Is; nil means success
		wantCode int   // when not 0: errors.As finds a *tgbotapi.Error with this code
		blocked  bool  // errors.Is(err, domain.ErrBlocked)
	}{
		{
			name: "problem: HTML message with the Done button",
			ctx:  ctx,
			call: sendProblem,
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(msgCfg(dailyText, doneKeyboard()))).Return(tgbotapi.Message{MessageID: 10}, nil)
				return m
			},
		},
		{
			name: "problem: 403 wraps ErrBlocked and keeps the API error",
			ctx:  ctx,
			call: sendProblem,
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, forbidden)
				return m
			},
			wantErr:  domain.ErrBlocked,
			wantCode: http.StatusForbidden,
			blocked:  true,
		},
		{
			name: "problem: another API error passes through",
			ctx:  ctx,
			call: sendProblem,
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, serverErr)
				return m
			},
			wantErr:  serverErr,
			wantCode: http.StatusInternalServerError,
		},
		{
			name:    "problem: a done ctx makes no call",
			ctx:     cancelled,
			call:    sendProblem,
			apiMock: mocks.NewMockbotAPI,
			wantErr: context.Canceled,
		},
		{
			name: "fetch failed: HTML message without a keyboard",
			ctx:  ctx,
			call: sendFetchFailed,
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(msgCfg("⚠️ Failed to fetch the problem from LeetCode. Try /today later.", nil))).
					Return(tgbotapi.Message{MessageID: 11}, nil)
				return m
			},
		},
		{
			name: "fetch failed: a 403 is not mapped",
			ctx:  ctx,
			call: sendFetchFailed,
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, forbidden)
				return m
			},
			wantErr:  forbidden,
			wantCode: http.StatusForbidden,
		},
		{
			name:    "fetch failed: a done ctx makes no call",
			ctx:     cancelled,
			call:    sendFetchFailed,
			apiMock: mocks.NewMockbotAPI,
			wantErr: context.Canceled,
		},
		{
			name: "text: HTML message without a keyboard",
			ctx:  ctx,
			call: func(ctx context.Context, s *sender) error { s.text(ctx, testChat, "hi"); return nil },
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(msgCfg("hi", nil))).Return(tgbotapi.Message{MessageID: 12}, nil)
				return m
			},
		},
		{
			name: "edit: HTML text with a keyboard",
			ctx:  ctx,
			call: func(ctx context.Context, s *sender) error { s.edit(ctx, testChat, 5, "hi", doneKeyboard()); return nil },
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(editCfg(5, "hi", doneKeyboard()))).Return(tgbotapi.Message{MessageID: 5}, nil)
				return m
			},
		},
		{
			name: "edit: a nil keyboard removes it",
			ctx:  ctx,
			call: func(ctx context.Context, s *sender) error { s.edit(ctx, testChat, 5, "hi", nil); return nil },
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(editCfg(5, "hi", nil))).Return(tgbotapi.Message{MessageID: 5}, nil)
				return m
			},
		},
		{
			name: "editKeyboard: replaces the keyboard only",
			ctx:  ctx,
			call: func(ctx context.Context, s *sender) error {
				s.editKeyboard(ctx, testChat, 5, doneKeyboard())
				return nil
			},
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(tgbotapi.NewEditMessageReplyMarkup(testChat, 5, *doneKeyboard()))).
					Return(tgbotapi.Message{MessageID: 5}, nil)
				return m
			},
		},
		{
			name: "answer: callback with a toast",
			ctx:  ctx,
			call: func(ctx context.Context, s *sender) error { s.answer(ctx, "cb", "hi"); return nil },
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb", "hi"))).Return(okResp, nil)
				return m
			},
		},
		{
			name: "helpers: a done ctx makes no call",
			ctx:  cancelled,
			call: func(ctx context.Context, s *sender) error {
				s.text(ctx, testChat, "hi")
				s.edit(ctx, testChat, 5, "hi", nil)
				s.editKeyboard(ctx, testChat, 5, doneKeyboard())
				s.answer(ctx, "cb", "hi")
				return nil
			},
			apiMock: mocks.NewMockbotAPI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			err := tt.call(tt.ctx, NewSender(tt.apiMock(ctrl)))
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if got := errors.Is(err, domain.ErrBlocked); got != tt.blocked {
				t.Errorf("errors.Is(err, domain.ErrBlocked) = %v, want %v", got, tt.blocked)
			}
			if tt.wantCode != 0 {
				var tgErr *tgbotapi.Error
				if !errors.As(err, &tgErr) || tgErr.Code != tt.wantCode {
					t.Errorf("errors.As(err, *tgbotapi.Error) = %v, want code %d", tgErr, tt.wantCode)
				}
			}
		})
	}
}

func TestIsBotBlocked(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "generic error", err: errors.New("something"), want: false},
		{name: "tg error 400", err: &tgbotapi.Error{Code: http.StatusBadRequest}, want: false},
		{name: "tg error 403", err: &tgbotapi.Error{Code: http.StatusForbidden}, want: true},
		{name: "wrapped tg error 403", err: fmt.Errorf("wrap: %w", &tgbotapi.Error{Code: http.StatusForbidden}), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBotBlocked(tt.err); got != tt.want {
				t.Errorf("isBotBlocked(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
