package bot

import (
	"fmt"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

const setupMsgID = 42

// expectSetupPrompts expects the /setup prompts for chat 100 up to the
// difficulty keyboard with ticked selected, all shown on message setupMsgID.
func expectSetupPrompts(m *mocks.MocktelegramSender, notifyTime string, ticked []string) {
	m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseTime, setupTimeKeyboard()))).
		Return(tgbotapi.Message{MessageID: setupMsgID}, nil)
	m.EXPECT().Send(gomock.Eq(editTextKB(100, setupMsgID, fmt.Sprintf(msgChooseTz, notifyTime), setupTzKeyboard()))).
		Return(tgbotapi.Message{}, nil)
	m.EXPECT().Send(gomock.Eq(editTextKB(100, setupMsgID, msgChooseDifficulty, difficultyKeyboard(ticked)))).
		Return(tgbotapi.Message{}, nil)
}

// expectToggle expects the answer to and keyboard update for pressing the
// d toggle on message setupMsgID, leaving selected ticked.
func expectToggle(m *mocks.MocktelegramSender, d string, selected ...string) {
	m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback(cbPrefixDiff+d, ""))).Return(answered, nil)
	m.EXPECT().Send(gomock.Eq(tgbotapi.NewEditMessageReplyMarkup(100, setupMsgID, difficultyKeyboard(selected)))).
		Return(tgbotapi.Message{}, nil)
}

func TestSetupFlow(t *testing.T) {
	members := map[string]storage.UserStat{"1": {Name: "Alice", Count: 4, LastSolvedDate: "2026-09-26"}}

	tests := []struct {
		name       string
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock  func(*gomock.Controller) *mocks.MockchatStore
		schedMock  func(*gomock.Controller) *mocks.MocktaskScheduler
		inputTime  string
		inputTz    string
		tzByButton bool     // choose the timezone with a keyboard button instead of typing it
		toggles    []string // difficulty buttons pressed before Save
		wantState  string
	}{
		{
			name:      "typed timezone, Hard deselected, new chat",
			inputTime: "09:00",
			inputTz:   "UTC",
			toggles:   []string{"Hard"},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				expectSetupPrompts(m, "09:00", leetcode.AllDifficulties())
				expectToggle(m, "Hard", "Easy", "Medium")
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback(cbCmdDiffSave, ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(editText(100, setupMsgID, fmt.Sprintf(msgAllSet, "09:00", "UTC", "Easy, Medium")))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false),
					m.EXPECT().Update(gomock.Eq(int64(100)), gomock.Any()).Return(false, nil),
					m.EXPECT().Set(gomock.Eq(storage.ChatConfig{
						ChatID:       100,
						NotifyTime:   "09:00",
						Timezone:     "UTC",
						Difficulties: []string{"Easy", "Medium"},
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
					Difficulties: []string{"Easy", "Medium"},
				})).Return(nil)
				return m
			},
			wantState: "",
		},
		{
			name:       "timezone button, re-running setup keeps members, difficulties and the pick",
			inputTime:  "09:00",
			inputTz:    "Europe/Moscow",
			tzByButton: true,
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				expectSetupPrompts(m, "09:00", []string{"Hard"})
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback(cbPrefixTz+"Europe/Moscow", ""))).Return(answered, nil)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback(cbCmdDiffSave, ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(editText(100, setupMsgID,
					fmt.Sprintf(msgAllSet, "09:00", "Europe/Moscow", "Hard")))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				stored := storage.ChatConfig{
					ChatID:       100,
					NotifyTime:   "07:00",
					Timezone:     "UTC",
					Members:      members,
					Difficulties: []string{"Hard"},
					DailyPick:    testPick(testDailyDate),
				}
				gomock.InOrder(
					m.EXPECT().Get(int64(100)).Return(stored, true),
					m.EXPECT().Update(gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(updateVia(m, stored)),
					m.EXPECT().Set(gomock.Eq(storage.ChatConfig{
						ChatID:       100,
						NotifyTime:   "09:00",
						Timezone:     "Europe/Moscow",
						Members:      members,
						Difficulties: []string{"Hard"},
						DailyPick:    testPick(testDailyDate),
					})).Return(nil),
				)
				return m
			},
			schedMock: func(ctrl *gomock.Controller) *mocks.MocktaskScheduler {
				m := mocks.NewMocktaskScheduler(ctrl)
				m.EXPECT().Schedule(int64(100), gomock.Eq(storage.ChatConfig{
					ChatID:       100,
					NotifyTime:   "09:00",
					Timezone:     "Europe/Moscow",
					Members:      members,
					Difficulties: []string{"Hard"},
					DailyPick:    testPick(testDailyDate),
				})).Return(nil)
				return m
			},
			wantState: "",
		},
		{
			// A cron send saved today's pick and a Done press counted a solve
			// after the difficulty step read the config; Save keeps both.
			name:      "changes saved while setup is open are kept",
			inputTime: "09:00",
			inputTz:   "UTC",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				expectSetupPrompts(m, "09:00", []string{"Easy"})
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback(cbCmdDiffSave, ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(editText(100, setupMsgID, fmt.Sprintf(msgAllSet, "09:00", "UTC", "Easy")))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				atDifficultyStep := storage.ChatConfig{ChatID: 100, NotifyTime: "07:00", Timezone: "UTC", Difficulties: []string{"Easy"}}
				atSave := atDifficultyStep
				atSave.Members = members
				atSave.DailyPick = testPick(testDailyDate)
				gomock.InOrder(
					m.EXPECT().Get(int64(100)).Return(atDifficultyStep, true),
					m.EXPECT().Update(gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(updateVia(m, atSave)),
					m.EXPECT().Set(gomock.Eq(storage.ChatConfig{
						ChatID:       100,
						NotifyTime:   "09:00",
						Timezone:     "UTC",
						Members:      members,
						Difficulties: []string{"Easy"},
						DailyPick:    testPick(testDailyDate),
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
					Members:      members,
					Difficulties: []string{"Easy"},
					DailyPick:    testPick(testDailyDate),
				})).Return(nil)
				return m
			},
			wantState: "",
		},
		{
			// No Set or Schedule expected — gomock fails the test if they are called.
			name:      "saving with nothing selected is rejected and keeps the session",
			inputTime: "09:00",
			inputTz:   "UTC",
			toggles:   []string{"Easy", "Medium", "Hard"},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				expectSetupPrompts(m, "09:00", leetcode.AllDifficulties())
				expectToggle(m, "Easy", "Medium", "Hard")
				expectToggle(m, "Medium", "Hard")
				expectToggle(m, "Hard")
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback(cbCmdDiffSave, msgPickAtLeastOne))).Return(answered, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false)
				return m
			},
			schedMock: mocks.NewMocktaskScheduler,
			wantState: stateAwaitingDifficulty,
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
			if tt.tzByButton {
				data := cbPrefixTz + tt.inputTz
				b.handleCallback(pressButton(data, chatID, setupMsgID, data))
			} else {
				b.handleAwaitingTimezone(chatID, tt.inputTz)
			}
			for _, d := range tt.toggles {
				data := cbPrefixDiff + d
				b.handleCallback(pressButton(data, chatID, setupMsgID, data))
			}
			b.handleCallback(pressButton(cbCmdDiffSave, chatID, setupMsgID, cbCmdDiffSave))

			if got := b.states.get(chatID); got != tt.wantState {
				t.Errorf("state: got %q, want %q", got, tt.wantState)
			}
		})
	}
}

// expectSetupThroughRouter expects, in order, what TestSetupFlowThroughRouter
// sends for chat 100 after the calls in before: the time keyboard, the
// timezone step, the retry after a sticker, the difficulty keyboard with
// ticked selected, the toggle leaving selected ticked, and the summary for
// selected.
func expectSetupThroughRouter(m *mocks.MocktelegramSender, ticked, selected []string, before ...any) {
	gomock.InOrder(append(before,
		m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseTime, setupTimeKeyboard()))).
			Return(tgbotapi.Message{MessageID: setupMsgID}, nil),
		m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("time", ""))).Return(answered, nil),
		m.EXPECT().Send(gomock.Eq(editTextKB(100, setupMsgID, fmt.Sprintf(msgChooseTz, "09:00"), setupTzKeyboard()))).
			Return(tgbotapi.Message{}, nil),
		m.EXPECT().Send(gomock.Eq(editTextKB(100, setupMsgID, msgInvalidTz, setupTzKeyboard()))).
			Return(tgbotapi.Message{}, nil),
		m.EXPECT().Send(gomock.Eq(editTextKB(100, setupMsgID, msgChooseDifficulty, difficultyKeyboard(ticked)))).
			Return(tgbotapi.Message{}, nil),
		m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("toggle", ""))).Return(answered, nil),
		m.EXPECT().Send(gomock.Eq(tgbotapi.NewEditMessageReplyMarkup(100, setupMsgID, difficultyKeyboard(selected)))).
			Return(tgbotapi.Message{}, nil),
		m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("save", ""))).Return(answered, nil),
		m.EXPECT().Send(gomock.Eq(editText(100, setupMsgID, fmt.Sprintf(msgAllSet, "09:00", "UTC", formatDifficulties(selected))))).
			Return(tgbotapi.Message{}, nil),
	)...)
}

// oldSetupMsgID is the prompt of a /setup abandoned before the current one.
const oldSetupMsgID = 7

// expectAbandonedSetup expects an earlier /setup for chat 100 on message
// oldSetupMsgID, left at the timezone step, and the menu-expired answers to
// its buttons pressed after /setup starts over. It returns the earlier calls
// in order.
func expectAbandonedSetup(m *mocks.MocktelegramSender) []any {
	m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("stale-time", msgMenuExpired))).Return(answered, nil)
	m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("stale-tz", msgMenuExpired))).Return(answered, nil)
	return []any{
		m.EXPECT().Send(gomock.Eq(kbMsg(100, msgChooseTime, setupTimeKeyboard()))).
			Return(tgbotapi.Message{MessageID: oldSetupMsgID}, nil),
		m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("old-time", ""))).Return(answered, nil),
		m.EXPECT().Send(gomock.Eq(editTextKB(100, oldSetupMsgID, fmt.Sprintf(msgChooseTz, "07:00"), setupTzKeyboard()))).
			Return(tgbotapi.Message{}, nil),
	}
}

// TestSetupFlowThroughRouter drives the whole /setup flow through
// handleMessage, as Run does: /setup, a time button, a sticker and then a
// typed timezone, stray text at the difficulty step, a toggle and Save.
func TestSetupFlowThroughRouter(t *testing.T) {
	members := map[string]storage.UserStat{"1": {Name: "Alice", Count: 4, LastSolvedDate: "2026-09-26"}}
	resubscribed := storage.ChatConfig{
		ChatID:       100,
		NotifyTime:   "09:00",
		Timezone:     "UTC",
		Members:      members,
		Difficulties: []string{"Easy", "Hard"},
		DailyPick:    testPick(testDailyDate),
	}

	chat := &tgbotapi.Chat{ID: 100}
	message := func(text string) tgbotapi.Update {
		return tgbotapi.Update{Message: &tgbotapi.Message{Chat: chat, Text: text}}
	}
	press := func(cbID, data string) tgbotapi.Update {
		return tgbotapi.Update{CallbackQuery: pressButton(cbID, 100, setupMsgID, data)}
	}
	pressOld := func(cbID, data string) tgbotapi.Update {
		return tgbotapi.Update{CallbackQuery: pressButton(cbID, 100, oldSetupMsgID, data)}
	}
	sticker := tgbotapi.Update{Message: &tgbotapi.Message{Chat: chat, Sticker: &tgbotapi.Sticker{FileID: "sticker"}}}

	tests := []struct {
		name       string
		toggle     string
		earlier    []tgbotapi.Update // sent before /setup
		atTimeStep []tgbotapi.Update // sent after /setup, before the time button
		atTzStep   []tgbotapi.Update // sent after the time button
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock  func(*gomock.Controller) *mocks.MockchatStore
		schedMock  func(*gomock.Controller) *mocks.MocktaskScheduler
	}{
		{
			name:   "new chat",
			toggle: "Hard",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				expectSetupThroughRouter(m, leetcode.AllDifficulties(), []string{"Easy", "Medium"})
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false),
					m.EXPECT().Update(gomock.Eq(int64(100)), gomock.Any()).Return(false, nil),
					m.EXPECT().Set(gomock.Eq(storage.ChatConfig{
						ChatID:       100,
						NotifyTime:   "09:00",
						Timezone:     "UTC",
						Difficulties: []string{"Easy", "Medium"},
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
					Difficulties: []string{"Easy", "Medium"},
				})).Return(nil)
				return m
			},
		},
		{
			name:   "subscribed chat starts from its saved difficulties and keeps members and the pick",
			toggle: "Easy",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				expectSetupThroughRouter(m, []string{"Hard"}, []string{"Easy", "Hard"})
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				stored := storage.ChatConfig{
					ChatID:       100,
					NotifyTime:   "07:00",
					Timezone:     "Europe/Moscow",
					Members:      members,
					Difficulties: []string{"Hard"},
					DailyPick:    testPick(testDailyDate),
				}
				gomock.InOrder(
					m.EXPECT().Get(int64(100)).Return(stored, true),
					m.EXPECT().Update(gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(updateVia(m, stored)),
					m.EXPECT().Set(gomock.Eq(resubscribed)).Return(nil),
				)
				return m
			},
			schedMock: func(ctrl *gomock.Controller) *mocks.MocktaskScheduler {
				m := mocks.NewMocktaskScheduler(ctrl)
				m.EXPECT().Schedule(int64(100), gomock.Eq(resubscribed)).Return(nil)
				return m
			},
		},
		{
			// The earlier /setup was left at the timezone step; /setup starts
			// over and the earlier prompt's buttons change nothing.
			name:       "abandoned setup is started over",
			toggle:     "Hard",
			earlier:    []tgbotapi.Update{message("/setup"), pressOld("old-time", cbPrefixTime+"07:00")},
			atTimeStep: []tgbotapi.Update{pressOld("stale-time", cbPrefixTime+"08:00")},
			atTzStep:   []tgbotapi.Update{pressOld("stale-tz", cbPrefixTz+"Europe/Moscow")},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				expectSetupThroughRouter(m, leetcode.AllDifficulties(), []string{"Easy", "Medium"}, expectAbandonedSetup(m)...)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false),
					m.EXPECT().Update(gomock.Eq(int64(100)), gomock.Any()).Return(false, nil),
					m.EXPECT().Set(gomock.Eq(storage.ChatConfig{
						ChatID:       100,
						NotifyTime:   "09:00",
						Timezone:     "UTC",
						Difficulties: []string{"Easy", "Medium"},
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
					Difficulties: []string{"Easy", "Medium"},
				})).Return(nil)
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
			handle := func(updates []tgbotapi.Update) {
				for _, u := range updates {
					b.handleMessage(u)
				}
			}

			handle(tt.earlier)
			b.handleMessage(message("/setup"))
			handle(tt.atTimeStep)
			b.handleMessage(press("time", cbPrefixTime+"09:00"))
			handle(tt.atTzStep)
			b.handleMessage(sticker)
			b.handleMessage(message("UTC"))
			b.handleMessage(message("hello"))
			if got := b.states.get(100); got != stateAwaitingDifficulty {
				t.Fatalf("state after stray text: got %q, want %q", got, stateAwaitingDifficulty)
			}
			b.handleMessage(press("toggle", cbPrefixDiff+tt.toggle))
			b.handleMessage(press("save", cbCmdDiffSave))

			if got := b.states.get(100); got != "" {
				t.Errorf("state after save: got %q, want none", got)
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
		name        string
		input       string
		pendingTime string
		senderMock  func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock   func(*gomock.Controller) *mocks.MockchatStore
		wantState   string
		wantMsgID   int
		wantPending pendingSetup
	}{
		{
			name:        "invalid timezone stays in awaiting-timezone and shows error",
			input:       "Not/ATimezone",
			pendingTime: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(editTextKB(100, 10, msgInvalidTz, setupTzKeyboard()))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			wantState:   stateAwaitingTimezone,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00"},
		},
		{
			// A sticker, photo or service message has no text.
			name:        "empty text is not a timezone",
			input:       "",
			pendingTime: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(editTextKB(100, 10, msgInvalidTz, setupTzKeyboard()))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			wantState:   stateAwaitingTimezone,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00"},
		},
		{
			// time.LoadLocation resolves "Local" to the server's timezone.
			name:        "Local is not a timezone",
			input:       "Local",
			pendingTime: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(editTextKB(100, 10, msgInvalidTz, setupTzKeyboard()))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			wantState:   stateAwaitingTimezone,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00"},
		},
		{
			// No Set or Schedule expected — setup only finishes on Save.
			name:        "valid timezone for a new chat moves to the difficulty step with everything ticked",
			input:       "UTC",
			pendingTime: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(editTextKB(100, 10, msgChooseDifficulty, difficultyKeyboard(leetcode.AllDifficulties())))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false)
				return m
			},
			wantState:   stateAwaitingDifficulty,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00", timezone: "UTC", difficulties: leetcode.AllDifficulties()},
		},
		{
			name:        "valid timezone when re-running setup ticks the saved difficulties",
			input:       "UTC",
			pendingTime: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(editTextKB(100, 10, msgChooseDifficulty, difficultyKeyboard([]string{"Hard"})))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{ChatID: 100, Difficulties: []string{"Hard"}}, true)
				return m
			},
			wantState:   stateAwaitingDifficulty,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00", timezone: "UTC", difficulties: []string{"Hard"}},
		},
		{
			name:        "valid timezone when re-running setup without a saved selection ticks everything",
			input:       "UTC",
			pendingTime: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(editTextKB(100, 10, msgChooseDifficulty, difficultyKeyboard(leetcode.AllDifficulties())))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{ChatID: 100}, true)
				return m
			},
			wantState:   stateAwaitingDifficulty,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00", timezone: "UTC", difficulties: leetcode.AllDifficulties()},
		},
		{
			name:  "missing pending time expires the session",
			input: "UTC",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(100, msgSessionExpired))).Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: mocks.NewMockchatStore,
			wantState: "",
			wantMsgID: 0,
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

			b.states.set(chatID, stateAwaitingTimezone)
			if tt.pendingTime != "" {
				b.states.setPendingTime(chatID, tt.pendingTime)
			}
			b.states.setSetupMsgID(chatID, 10)

			b.handleAwaitingTimezone(chatID, tt.input)

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

func TestTimeCallback(t *testing.T) {
	// timeStep is chat 100 at the time step of /setup on message 10.
	timeStep := func(s *stateStore) {
		s.set(100, stateAwaitingTime)
		s.setSetupMsgID(100, 10)
	}

	tests := []struct {
		name        string
		before      func(s *stateStore)
		pressedMsg  int
		notifyTime  string
		senderMock  func(*gomock.Controller) *mocks.MocktelegramSender
		wantState   string
		wantMsgID   int
		wantPending pendingSetup
	}{
		{
			name:       "moves to the timezone step",
			before:     timeStep,
			pressedMsg: 10,
			notifyTime: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(editTextKB(100, 10, fmt.Sprintf(msgChooseTz, "09:00"), setupTzKeyboard()))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			wantState:   stateAwaitingTimezone,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00"},
		},
		{
			// Telegram clients can send any callback data; only valid times are saved.
			name:       "forged out-of-range time is rejected",
			before:     timeStep,
			pressedMsg: 10,
			notifyTime: "25:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			wantState: stateAwaitingTime,
			wantMsgID: 10,
		},
		{
			name:       "forged empty time is rejected",
			before:     timeStep,
			pressedMsg: 10,
			notifyTime: "",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			wantState: stateAwaitingTime,
			wantMsgID: 10,
		},
		{
			// No Send expected — a stale menu changes nothing.
			name:       "button on an older /setup prompt keeps the time step",
			before:     timeStep,
			pressedMsg: 8,
			notifyTime: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			wantState: stateAwaitingTime,
			wantMsgID: 10,
		},
		{
			name:       "button without a session is stale",
			before:     func(*stateStore) {},
			pressedMsg: 10,
			notifyTime: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			wantState: "",
			wantMsgID: 0,
		},
		{
			name: "button pressed again at the timezone step keeps the chosen time",
			before: func(s *stateStore) {
				s.set(100, stateAwaitingTimezone)
				s.setPendingTime(100, "09:00")
				s.setSetupMsgID(100, 10)
			},
			pressedMsg: 10,
			notifyTime: "10:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			wantState:   stateAwaitingTimezone,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00"},
		},
		{
			name:       "button at the difficulty step keeps the pending setup",
			before:     setupSession("09:00", "UTC", "Medium"),
			pressedMsg: 55,
			notifyTime: "10:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			wantState:   stateAwaitingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{notifyTime: "09:00", timezone: "UTC", difficulties: []string{"Medium"}},
		},
		{
			name:       "button on an old setup message leaves an active /difficulty session intact",
			before:     editSession("Easy"),
			pressedMsg: 10,
			notifyTime: "09:00",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			wantState:   stateEditingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{difficulties: []string{"Easy"}},
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

			b.handleCallback(pressButton("cb1", chatID, tt.pressedMsg, cbPrefixTime+tt.notifyTime))

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

func TestTimezoneCallback(t *testing.T) {
	// timezoneStep is chat 100 at the timezone step of /setup on message 10.
	timezoneStep := func(s *stateStore) {
		s.set(100, stateAwaitingTimezone)
		s.setPendingTime(100, "09:00")
		s.setSetupMsgID(100, 10)
	}

	tests := []struct {
		name        string
		before      func(s *stateStore)
		pressedMsg  int
		tz          string
		senderMock  func(*gomock.Controller) *mocks.MocktelegramSender
		storeMock   func(*gomock.Controller) *mocks.MockchatStore
		wantState   string
		wantMsgID   int
		wantPending pendingSetup
	}{
		{
			name:       "moves to the difficulty step on the setup message",
			before:     timezoneStep,
			pressedMsg: 10,
			tz:         "Europe/Moscow",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", ""))).Return(answered, nil)
				m.EXPECT().Send(gomock.Eq(editTextKB(100, 10, msgChooseDifficulty, difficultyKeyboard(leetcode.AllDifficulties())))).
					Return(tgbotapi.Message{}, nil)
				return m
			},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(int64(100)).Return(storage.ChatConfig{}, false)
				return m
			},
			wantState:   stateAwaitingDifficulty,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00", timezone: "Europe/Moscow", difficulties: leetcode.AllDifficulties()},
		},
		{
			// No Send expected — a stale menu changes nothing.
			name:       "button on another message keeps the timezone step",
			before:     timezoneStep,
			pressedMsg: 8,
			tz:         "Europe/Moscow",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			wantState:   stateAwaitingTimezone,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00"},
		},
		{
			// Telegram clients can send any callback data; only real zones are saved.
			name:       "forged Local payload is rejected",
			before:     timezoneStep,
			pressedMsg: 10,
			tz:         "Local",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			wantState:   stateAwaitingTimezone,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00"},
		},
		{
			// Telegram clients can send any callback data; only real zones are saved.
			name:       "forged unknown zone payload is rejected",
			before:     timezoneStep,
			pressedMsg: 10,
			tz:         "Not/AZone",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			wantState:   stateAwaitingTimezone,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00"},
		},
		{
			// Telegram clients can send any callback data; only real zones are saved.
			name:       "forged empty payload is rejected",
			before:     timezoneStep,
			pressedMsg: 10,
			tz:         "",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			wantState:   stateAwaitingTimezone,
			wantMsgID:   10,
			wantPending: pendingSetup{notifyTime: "09:00"},
		},
		{
			name:       "button without a session is stale",
			before:     func(*stateStore) {},
			pressedMsg: 10,
			tz:         "Europe/Moscow",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock: mocks.NewMockchatStore,
			wantState: "",
			wantMsgID: 0,
		},
		{
			name:       "button on an old setup message leaves an active /difficulty session intact",
			before:     editSession("Easy"),
			pressedMsg: 10,
			tz:         "Europe/Moscow",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			wantState:   stateEditingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{difficulties: []string{"Easy"}},
		},
		{
			name:       "button pressed again at the difficulty step keeps the chosen timezone",
			before:     setupSession("09:00", "Europe/London", "Medium", "Hard"),
			pressedMsg: 55,
			tz:         "Europe/Moscow",
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMenuExpired))).Return(answered, nil)
				return m
			},
			storeMock:   mocks.NewMockchatStore,
			wantState:   stateAwaitingDifficulty,
			wantMsgID:   55,
			wantPending: pendingSetup{notifyTime: "09:00", timezone: "Europe/London", difficulties: []string{"Medium", "Hard"}},
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

			b.handleCallback(pressButton("cb1", chatID, tt.pressedMsg, cbPrefixTz+tt.tz))

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
