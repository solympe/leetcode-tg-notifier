//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	testToken = "TEST:TOKEN"
	// pollCap is the longest a getUpdates with a timeout waits, so stopping
	// the app never waits for a real 60s long poll.
	pollCap = 100 * time.Millisecond
)

var botUser = tgbotapi.User{ID: 1, IsBot: true, FirstName: "Test", UserName: "TestBot"}

// tgCall is one recorded sendMessage, editMessageText or editMessageReplyMarkup.
type tgCall struct {
	method    string
	chatID    int64
	msgID     int // assigned by sendMessage, the target of an edit
	text      string
	parseMode string
	kb        *tgbotapi.InlineKeyboardMarkup // nil: the call had no reply_markup
	blocked   bool                           // a sendMessage rejected with 403
}

func (c tgCall) String() string {
	s := fmt.Sprintf("%s msg=%d parse_mode=%q text=%q", c.method, c.msgID, c.parseMode, c.text)
	if c.kb != nil {
		s += " kb=" + markupString(c.kb)
	}
	if c.blocked {
		s += " (403)"
	}
	return s
}

// shown is a bot message as the chat currently sees it.
type shown struct {
	text string
	kb   *tgbotapi.InlineKeyboardMarkup
}

// fakeTelegram is an in-process Bot API: POST /bot{token}/{method} with
// form-encoded parameters, as tgbotapi v5.5.1 sends them.
type fakeTelegram struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	changed  chan struct{} // closed and replaced on every state change
	queue    []tgbotapi.Update
	lastUpd  int
	lastMsg  map[int64]int // per-chat message_id counter
	shown    map[int64]map[int]shown
	calls    map[int64][]tgCall
	consumed map[int64]int
	blocked  map[int64]bool
	answers  map[string][]string // every issued cbID has an entry
	unknown  []string
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	f := &fakeTelegram{
		t:        t,
		changed:  make(chan struct{}),
		lastMsg:  make(map[int64]int),
		shown:    make(map[int64]map[int]shown),
		calls:    make(map[int64][]tgCall),
		consumed: make(map[int64]int),
		blocked:  make(map[int64]bool),
		answers:  make(map[string][]string),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	token, method, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/bot"), "/")
	if !ok || token != testToken {
		writeJSON(w, http.StatusUnauthorized, `{"ok":false,"error_code":401}`)
		return
	}
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("fake telegram: %s: parse form: %v", method, err)
	}
	switch method {
	case "getMe":
		writeJSON(w, http.StatusOK, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test","username":"TestBot"}}`)
	case "getUpdates":
		f.getUpdates(w, r)
	case "sendMessage":
		f.sendMessage(w, r)
	case "editMessageText", "editMessageReplyMarkup":
		f.edit(w, r, method)
	case "answerCallbackQuery":
		f.answerCallback(w, r)
	default:
		f.mu.Lock()
		f.unknown = append(f.unknown, method)
		f.mu.Unlock()
		f.t.Errorf("fake telegram: unexpected method %s %v", method, r.Form)
		writeJSON(w, http.StatusNotFound, `{"ok":false,"error_code":404,"description":"Not Found"}`)
	}
}

// getUpdates is at-least-once: it deletes the updates below offset, returns
// up to limit of the rest and, when there are none and timeout is set, waits
// up to pollCap for an enqueue. The confirming call has no timeout and
// returns at once.
func (f *fakeTelegram) getUpdates(w http.ResponseWriter, r *http.Request) {
	offset := f.formInt(r, "offset", 0)
	limit := f.formInt(r, "limit", 100)
	_, long := r.Form["timeout"]
	timer := time.NewTimer(pollCap)
	defer timer.Stop()
	for {
		f.mu.Lock()
		f.queue = slices.DeleteFunc(f.queue, func(u tgbotapi.Update) bool { return u.UpdateID < offset })
		if len(f.queue) > 0 || !long {
			batch := append([]tgbotapi.Update{}, f.queue[:min(limit, len(f.queue))]...)
			f.mu.Unlock()
			writeResult(w, batch)
			return
		}
		ch := f.changed
		f.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			writeResult(w, []tgbotapi.Update{})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (f *fakeTelegram) sendMessage(w http.ResponseWriter, r *http.Request) {
	c := f.parseCall(r, "sendMessage")
	f.mu.Lock()
	if f.blocked[c.chatID] {
		c.blocked = true
		f.recordLocked(c)
		f.mu.Unlock()
		writeJSON(w, http.StatusForbidden, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
		return
	}
	f.lastMsg[c.chatID]++
	c.msgID = f.lastMsg[c.chatID]
	f.messagesLocked(c.chatID)[c.msgID] = shown{text: c.text, kb: c.kb}
	f.recordLocked(c)
	f.mu.Unlock()
	writeResult(w, message(c.chatID, c.msgID, c.text, c.kb))
}

// edit applies editMessageText (a missing reply_markup removes the keyboard)
// or editMessageReplyMarkup and returns the edited Message, which tgbotapi's
// Send unmarshals.
func (f *fakeTelegram) edit(w http.ResponseWriter, r *http.Request, method string) {
	c := f.parseCall(r, method)
	f.mu.Lock()
	msgs := f.messagesLocked(c.chatID)
	cur, ok := msgs[c.msgID]
	if !ok {
		f.mu.Unlock()
		f.t.Errorf("fake telegram: %s of unknown message %d in chat %d", method, c.msgID, c.chatID)
		writeJSON(w, http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`)
		return
	}
	if method == "editMessageText" {
		cur.text = c.text
	}
	cur.kb = c.kb
	msgs[c.msgID] = cur
	f.recordLocked(c)
	f.mu.Unlock()
	writeResult(w, message(c.chatID, c.msgID, cur.text, cur.kb))
}

func (f *fakeTelegram) answerCallback(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("callback_query_id")
	f.mu.Lock()
	prev, issued := f.answers[id]
	if issued {
		f.answers[id] = append(prev, r.Form.Get("text"))
		f.broadcastLocked()
	}
	f.mu.Unlock()
	if !issued {
		f.t.Errorf("fake telegram: answer to unknown callback %q", id)
	}
	writeJSON(w, http.StatusOK, `{"ok":true,"result":true}`)
}

func (f *fakeTelegram) parseCall(r *http.Request, method string) tgCall {
	chatID, err := strconv.ParseInt(r.Form.Get("chat_id"), 10, 64)
	if err != nil {
		f.t.Errorf("fake telegram: %s: chat_id: %v", method, err)
	}
	c := tgCall{
		method:    method,
		chatID:    chatID,
		msgID:     f.formInt(r, "message_id", 0),
		text:      r.Form.Get("text"),
		parseMode: r.Form.Get("parse_mode"),
	}
	if raw := r.Form.Get("reply_markup"); raw != "" {
		var kb tgbotapi.InlineKeyboardMarkup
		if err := json.Unmarshal([]byte(raw), &kb); err != nil {
			f.t.Errorf("fake telegram: %s: reply_markup %q: %v", method, raw, err)
		}
		c.kb = &kb
	}
	return c
}

func (f *fakeTelegram) formInt(r *http.Request, key string, def int) int {
	s := r.Form.Get(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		f.t.Errorf("fake telegram: %s=%q: %v", key, s, err)
	}
	return n
}

func (f *fakeTelegram) messagesLocked(chatID int64) map[int]shown {
	m, ok := f.shown[chatID]
	if !ok {
		m = make(map[int]shown)
		f.shown[chatID] = m
	}
	return m
}

func (f *fakeTelegram) recordLocked(c tgCall) {
	f.calls[c.chatID] = append(f.calls[c.chatID], c)
	f.broadcastLocked()
}

func (f *fakeTelegram) broadcastLocked() {
	close(f.changed)
	f.changed = make(chan struct{})
}

func (f *fakeTelegram) enqueueLocked(u tgbotapi.Update) {
	f.lastUpd++
	u.UpdateID = f.lastUpd
	f.queue = append(f.queue, u)
	f.broadcastLocked()
}

// say enqueues a text message from user; a negative chatID is a supergroup.
func (f *fakeTelegram) say(chatID int64, from tgbotapi.User, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastMsg[chatID]++
	f.enqueueLocked(tgbotapi.Update{Message: &tgbotapi.Message{
		MessageID: f.lastMsg[chatID],
		From:      &from,
		Date:      int(time.Now().Unix()),
		Chat:      chatOf(chatID),
		Text:      text,
	}})
}

// next consumes the chat's next recorded call, waiting up to wait for it.
func (f *fakeTelegram) next(chatID int64, wait time.Duration) (tgCall, bool) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		f.mu.Lock()
		if i := f.consumed[chatID]; i < len(f.calls[chatID]) {
			f.consumed[chatID]++
			c := f.calls[chatID][i]
			f.mu.Unlock()
			return c, true
		}
		ch := f.changed
		f.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			return tgCall{}, false
		}
	}
}

// pending returns the chat's recorded calls no assertion has consumed.
func (f *fakeTelegram) pending(chatID int64) []tgCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls[chatID][f.consumed[chatID]:])
}

// transcript renders every call recorded for the chat, marking the consumed ones.
func (f *fakeTelegram) transcript(chatID int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sb strings.Builder
	fmt.Fprintf(&sb, "chat %d transcript (%d consumed):\n", chatID, f.consumed[chatID])
	for i, c := range f.calls[chatID] {
		mark := " "
		if i < f.consumed[chatID] {
			mark = "✓"
		}
		fmt.Fprintf(&sb, "  %s %d. %s\n", mark, i+1, c)
	}
	return sb.String()
}

// verify checks the strict invariants once the app has stopped.
func (f *fakeTelegram) verify() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range slices.Sorted(maps.Keys(f.answers)) {
		if got := f.answers[id]; len(got) != 1 {
			f.t.Errorf("invariant: callback %s answered %d times %q, want exactly once", id, len(got), got)
		}
	}
	for _, chatID := range slices.Sorted(maps.Keys(f.calls)) {
		calls := f.calls[chatID]
		for _, c := range calls {
			if (c.method == "sendMessage" || c.method == "editMessageText") && c.parseMode != "HTML" {
				f.t.Errorf("invariant: chat %d: %s without parse_mode=HTML", chatID, c)
			}
		}
		for _, c := range calls[f.consumed[chatID]:] {
			f.t.Errorf("invariant: chat %d: call never asserted: %s", chatID, c)
		}
	}
	if len(f.unknown) > 0 {
		f.t.Errorf("invariant: unknown methods called: %q", f.unknown)
	}
	if len(f.queue) > 0 {
		f.t.Errorf("invariant: %d updates never confirmed, first update_id %d", len(f.queue), f.queue[0].UpdateID)
	}
}

func chatOf(id int64) *tgbotapi.Chat {
	if id < 0 {
		return &tgbotapi.Chat{ID: id, Type: "supergroup", Title: "Test group"}
	}
	return &tgbotapi.Chat{ID: id, Type: "private"}
}

func message(chatID int64, msgID int, text string, kb *tgbotapi.InlineKeyboardMarkup) *tgbotapi.Message {
	from := botUser
	return &tgbotapi.Message{
		MessageID:   msgID,
		From:        &from,
		Date:        int(time.Now().Unix()),
		Chat:        chatOf(chatID),
		Text:        text,
		ReplyMarkup: kb,
	}
}

func markupString(kb *tgbotapi.InlineKeyboardMarkup) string {
	rows := make([]string, 0, len(kb.InlineKeyboard))
	for _, row := range kb.InlineKeyboard {
		buttons := make([]string, 0, len(row))
		for _, b := range row {
			data := "<nil>"
			if b.CallbackData != nil {
				data = *b.CallbackData
			}
			buttons = append(buttons, b.Text+"|"+data)
		}
		rows = append(rows, "["+strings.Join(buttons, ", ")+"]")
	}
	return strings.Join(rows, " ")
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func writeResult(w http.ResponseWriter, result any) {
	raw, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, `{"ok":true,"result":`+string(raw)+`}`)
}
