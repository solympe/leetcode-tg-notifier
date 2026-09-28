//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

var (
	alice = tgbotapi.User{ID: 7, FirstName: "Alice"}
	bob   = tgbotapi.User{ID: 8, UserName: "bob"} // no first name: shown as @bob
)

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

// legacyFile, legacyChat, legacyStat and legacyPick copy today's
// storage.ChatConfig, UserStat and DailyPick tags verbatim, so stored() reads
// config.json exactly as the pre-refactor binary does.
type legacyFile struct {
	Chats map[string]legacyChat `json:"chats"`
}

type legacyStat struct {
	Name           string `json:"name"`
	Count          int    `json:"count"`
	LastSolvedDate string `json:"last_solved_date"`
}

type legacyPick struct {
	Date            string   `json:"date"`
	DailyDifficulty string   `json:"daily_difficulty"`
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Link            string   `json:"link"`
	Difficulty      string   `json:"difficulty"`
	Tags            []string `json:"tags"`
}

type legacyChat struct {
	ChatID       int64                 `json:"chat_id"`
	NotifyTime   string                `json:"notify_time"`
	Timezone     string                `json:"timezone"`
	Members      map[string]legacyStat `json:"members"`
	Difficulties []string              `json:"difficulties,omitempty"`
	DailyPick    *legacyPick           `json:"daily_pick,omitempty"`
}

// stop cancels the app's ctx and waits for Run to return.
func (e *env) stop() {
	e.t.Helper()
	e.halt()
}

// restart stops the app after every update so far is handled and starts a
// new one on the same dir and fakes.
func (e *env) restart() {
	e.t.Helper()
	e.sync()
	e.stop()
	e.start()
}

// seed writes config.json before start.
func (e *env) seed(raw string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.dir, "config.json"), []byte(raw), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// stored decodes the chat from config.json on disk with the legacy structs.
func (e *env) stored(chatID int64) (legacyChat, bool) {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.dir, "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return legacyChat{}, false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var f legacyFile
	if err := json.Unmarshal(raw, &f); err != nil {
		e.t.Fatalf("config.json: %v\n%s", err, raw)
	}
	c, ok := f.Chats[strconv.FormatInt(chatID, 10)]
	return c, ok
}

// storedEq asserts that config.json holds exactly want for the chat.
func (e *env) storedEq(chatID int64, want legacyChat) {
	e.t.Helper()
	got, ok := e.stored(chatID)
	if !ok {
		e.t.Errorf("chat %d: not in config.json, want %s", chatID, asJSON(want))
		return
	}
	if !reflect.DeepEqual(got, want) {
		e.t.Errorf("chat %d in config.json:\n got %s\nwant %s", chatID, asJSON(got), asJSON(want))
	}
}

// notStored asserts that config.json has no entry for the chat.
func (e *env) notStored(chatID int64) {
	e.t.Helper()
	if got, ok := e.stored(chatID); ok {
		e.t.Errorf("chat %d: still in config.json: %s", chatID, asJSON(got))
	}
}

// fire runs the chat's cron entry now, synchronously, exactly as cron would
// (Recover included); false means nothing is scheduled.
func (e *env) fire(chatID int64) bool {
	return e.a.sched.RunNow(chatID)
}

// scheduledAt asserts that the chat's next firing is at hhmm in zone, within 24h.
func (e *env) scheduledAt(chatID int64, hhmm, zone string) {
	e.t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		e.t.Fatal(err)
	}
	next, ok := e.a.sched.Next(chatID)
	if !ok {
		e.t.Errorf("chat %d: not scheduled, want %s %s", chatID, hhmm, zone)
		return
	}
	if got := next.In(loc).Format("15:04"); got != hhmm {
		e.t.Errorf("chat %d: next firing at %s %s, want %s", chatID, got, zone, hhmm)
	}
	if d := time.Until(next); d <= 0 || d > 24*time.Hour {
		e.t.Errorf("chat %d: next firing %v away, want within 24h", chatID, d)
	}
}

func (e *env) notScheduled(chatID int64) {
	e.t.Helper()
	if next, ok := e.a.sched.Next(chatID); ok {
		e.t.Errorf("chat %d: still scheduled at %v", chatID, next)
	}
}

// press presses the button with data on the newest bot message showing it.
func (e *env) press(chatID int64, from tgbotapi.User, data string) string {
	e.t.Helper()
	msgID, ok := e.tg.newest(chatID, data)
	if !ok {
		e.t.Fatalf("chat %d: no message shows a %q button\n%s", chatID, data, e.tg.transcript(chatID))
	}
	return e.tg.callback(chatID, from, msgID, false, data)
}

// pressOn presses data on message msgID whatever its keyboard shows: stale
// and forged buttons.
func (e *env) pressOn(chatID int64, from tgbotapi.User, msgID int, data string) string {
	return e.tg.callback(chatID, from, msgID, false, data)
}

// pressInline presses a button whose callback has no Message.
func (e *env) pressInline(from tgbotapi.User, data string) string {
	return e.tg.callback(0, from, 0, true, data)
}

// block makes later sendMessage calls to the chat fail with 403.
func (e *env) block(chatID int64) {
	e.tg.block(chatID)
}

// edited expects an editMessageText of msgID; a nil kb means the keyboard is removed.
func (e *env) edited(chatID int64, msgID int, text string, kb keyboard) {
	e.t.Helper()
	c := e.expectMessage(chatID)
	if c.method != "editMessageText" || c.msgID != msgID || c.text != text || !kb.matches(c.kb) {
		e.t.Fatalf("chat %d:\n got %s\nwant editMessageText msg=%d text=%q kb=%s\n%s", chatID, c, msgID, text, kb, e.tg.transcript(chatID))
	}
}

// rekeyed expects an editMessageReplyMarkup of msgID.
func (e *env) rekeyed(chatID int64, msgID int, kb keyboard) {
	e.t.Helper()
	c := e.expectMessage(chatID)
	if c.method != "editMessageReplyMarkup" || c.msgID != msgID || !kb.matches(c.kb) {
		e.t.Fatalf("chat %d:\n got %s\nwant editMessageReplyMarkup msg=%d kb=%s\n%s", chatID, c, msgID, kb, e.tg.transcript(chatID))
	}
}

// blockedAttempt expects a sendMessage that got 403 and returns its text.
func (e *env) blockedAttempt(chatID int64) string {
	e.t.Helper()
	c := e.expectMessage(chatID)
	if c.method != "sendMessage" || !c.blocked {
		e.t.Fatalf("chat %d:\n got %s\nwant a sendMessage rejected with 403\n%s", chatID, c, e.tg.transcript(chatID))
	}
	return c.text
}

// expectAnswer returns the answer to callback cbID.
func (e *env) expectAnswer(cbID string) string {
	e.t.Helper()
	text, ok := e.tg.answer(cbID, waitFor)
	if !ok {
		e.t.Fatalf("callback %s: not answered within %v", cbID, waitFor)
	}
	return text
}

// answered asserts the answer to callback cbID.
func (e *env) answered(cbID, want string) {
	e.t.Helper()
	if got := e.expectAnswer(cbID); got != want {
		e.t.Errorf("callback %s answered %q, want %q", cbID, got, want)
	}
}

func asJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(raw)
}
