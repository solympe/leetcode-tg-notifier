//go:build integration

package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	probeChat = 1               // reserved for sync()
	waitFor   = 5 * time.Second // the longest any expectation waits
)

var alice = tgbotapi.User{ID: 7, FirstName: "Alice"}

// button is an expected inline button; an empty text matches any label.
type button struct{ text, data string }

// keyboard is an expected inline keyboard; nil means no keyboard.
type keyboard [][]button

func (k keyboard) matches(got *tgbotapi.InlineKeyboardMarkup) bool {
	if k == nil || got == nil {
		return k == nil && got == nil
	}
	if len(got.InlineKeyboard) != len(k) {
		return false
	}
	for i, row := range k {
		if len(got.InlineKeyboard[i]) != len(row) {
			return false
		}
		for j, want := range row {
			b := got.InlineKeyboard[i][j]
			if b.CallbackData == nil || *b.CallbackData != want.data || (want.text != "" && b.Text != want.text) {
				return false
			}
		}
	}
	return true
}

func (k keyboard) String() string {
	if k == nil {
		return "<none>"
	}
	rows := make([]string, 0, len(k))
	for _, row := range k {
		buttons := make([]string, 0, len(row))
		for _, b := range row {
			buttons = append(buttons, b.text+"|"+b.data)
		}
		rows = append(rows, "["+strings.Join(buttons, ", ")+"]")
	}
	return strings.Join(rows, " ")
}

// env is one hermetic bot: a temp dir, the two fakes and the application
// that main would build, started with a fresh ctx.
type env struct {
	t    *testing.T
	dir  string
	tg   *fakeTelegram
	lc   *fakeLeetCode
	a    *application // the last started app; kept after stop()
	halt func()       // stops the last started app, once
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, dir: t.TempDir(), tg: newFakeTelegram(t), lc: newFakeLeetCode(t)}
	// Registered after the servers' Close and before any start(), so cleanup
	// stops the app, then verifies, then closes the servers.
	t.Cleanup(func() {
		if !t.Failed() {
			e.tg.verify()
		}
	})
	return e
}

// start builds the app exactly as main does and runs it in the background.
// Restore runs inside New, so fire and scheduledAt are valid on return.
func (e *env) start() {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a, err := New(ctx, Config{
		Token:            testToken,
		StoragePath:      filepath.Join(e.dir, "config.json"),
		TelegramEndpoint: e.tg.srv.URL + "/bot%s/%s",
		LeetCodeEndpoint: e.lc.srv.URL + "/graphql",
	})
	if err != nil {
		cancel()
		e.t.Fatalf("New: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(ctx)
	}()
	var once sync.Once
	e.a = a
	e.halt = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(waitFor):
				e.t.Errorf("Run did not return within %v of cancel", waitFor)
			}
		})
	}
	e.t.Cleanup(e.halt)
}

// say sends a text message from user to the bot.
func (e *env) say(chatID int64, from tgbotapi.User, text string) {
	e.tg.say(chatID, from, text)
}

// expectMessage consumes the chat's next send or edit, in order.
func (e *env) expectMessage(chatID int64) tgCall {
	e.t.Helper()
	c, ok := e.tg.next(chatID, waitFor)
	if !ok {
		e.t.Fatalf("chat %d: no further message within %v\n%s", chatID, waitFor, e.tg.transcript(chatID))
	}
	return c
}

// sent expects a delivered sendMessage and returns its message_id.
func (e *env) sent(chatID int64, text string, kb keyboard) int {
	e.t.Helper()
	c := e.expectMessage(chatID)
	if c.method != "sendMessage" || c.blocked || c.text != text || !kb.matches(c.kb) {
		e.t.Fatalf("chat %d:\n got %s\nwant sendMessage text=%q kb=%s\n%s", chatID, c, text, kb, e.tg.transcript(chatID))
	}
	return c.msgID
}

// sync returns once every update sent so far is handled: the loop is
// sequential, so the reply to a later /about comes after all of them.
func (e *env) sync() {
	e.t.Helper()
	e.say(probeChat, alice, "/about")
	e.sent(probeChat, aboutText, nil)
}

// expectQuiet asserts that nothing beyond what was already asserted reached the chat.
func (e *env) expectQuiet(chatID int64) {
	e.t.Helper()
	e.sync()
	for _, c := range e.tg.pending(chatID) {
		e.t.Errorf("chat %d: unexpected %s", chatID, c)
	}
}
