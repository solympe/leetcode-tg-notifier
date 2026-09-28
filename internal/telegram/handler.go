package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// command is a slash command; inMenu commands are also menu buttons, whose
// callback data is the command itself.
type command struct {
	run    func(ctx context.Context, chatID int64)
	inMenu bool
}

// handler routes updates. It never creates a ctx: every call uses the one
// Handle receives, and callback actions capture it.
type handler struct {
	*sender
	svc      service
	botName  string
	sessions sessions
	commands map[string]command // a field, not a package var, which avoids an init cycle
}

func NewHandler(api botAPI, svc service, botName string) *handler {
	h := &handler{
		sender:   NewSender(api),
		svc:      svc,
		botName:  botName,
		sessions: sessions{m: make(map[int64]session)},
	}
	h.commands = map[string]command{
		cmdStart:       {run: h.start},
		cmdAbout:       {run: h.about},
		cmdSetup:       {run: h.startSetup, inMenu: true},
		cmdDifficulty:  {run: h.startDifficulty, inMenu: true},
		cmdToday:       {run: svc.SendToday, inMenu: true},
		cmdDaily:       {run: svc.SendDaily, inMenu: true},
		cmdStatus:      {run: h.status, inMenu: true},
		cmdRating:      {run: h.rating, inMenu: true},
		cmdUnsubscribe: {run: h.unsubscribe, inMenu: true},
	}
	return h
}

// Handle handles one update. A panic is logged and the update dropped, so it
// never takes the process down.
func (h *handler) Handle(ctx context.Context, u tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in update %d: %v\n%s", u.UpdateID, r, debug.Stack())
		}
	}()
	switch {
	case u.CallbackQuery != nil:
		h.onCallback(ctx, u.CallbackQuery)
	case u.Message != nil:
		h.onMessage(ctx, u.Message)
	}
}

// onMessage runs a command, "/today" and "/today@AnyBot" alike, or passes
// other text to the dialog. Unknown commands are ignored.
func (h *handler) onMessage(ctx context.Context, m *tgbotapi.Message) {
	text := strings.TrimSpace(m.Text)
	if !strings.HasPrefix(text, "/") {
		h.onText(ctx, m.Chat.ID, text)
		return
	}
	name, _, _ := strings.Cut(text, "@")
	if c, ok := h.commands[name]; ok {
		c.run(ctx, m.Chat.ID)
	}
}

// onCallback answers every callback exactly once, before its action runs.
func (h *handler) onCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	if cb.Message == nil {
		h.answer(ctx, cb.ID, msgMessageExpired)
		return
	}
	toast, act := h.button(ctx, cb.Message.Chat.ID, cb.Message.MessageID, cb.From, cb.Data)
	h.answer(ctx, cb.ID, toast)
	if act != nil {
		act()
	}
}

// button validates a press on message msgID and returns its toast and the
// action to run after the answer, if any. Only done has a side effect here:
// its toast reports the count.
func (h *handler) button(ctx context.Context, chatID int64, msgID int, from *tgbotapi.User, data string) (string, func()) {
	if c, ok := h.commands[data]; ok && c.inMenu {
		return "", func() { c.run(ctx, chatID) }
	}
	if data == cbDone {
		return h.done(ctx, chatID, from), nil
	}
	if data == cbDiffSave {
		return h.saveButton(ctx, chatID, msgID)
	}
	if t, ok := strings.CutPrefix(data, cbPrefixTime); ok {
		return h.timeButton(ctx, chatID, msgID, t)
	}
	if zone, ok := strings.CutPrefix(data, cbPrefixTz); ok {
		return h.tzButton(ctx, chatID, msgID, zone)
	}
	if d, ok := strings.CutPrefix(data, cbPrefixDiff); ok {
		return h.diffButton(ctx, chatID, msgID, d)
	}
	return msgMenuExpired, nil
}

// done counts the presser's solve for today and returns the toast.
func (h *handler) done(ctx context.Context, chatID int64, from *tgbotapi.User) string {
	name := from.FirstName
	if name == "" {
		name = "@" + from.UserName
	}
	total, err := h.svc.Solve(ctx, chatID, from.ID, name)
	switch {
	case errors.Is(err, domain.ErrNotSubscribed):
		return msgNotSubscribed
	case errors.Is(err, domain.ErrAlreadySolved):
		return msgAlreadyCounted
	case err != nil:
		log.Printf("solve in %d: %v", chatID, err)
		if total == 0 {
			return ""
		}
	}
	return fmt.Sprintf(msgCounted, total)
}

func (h *handler) start(ctx context.Context, chatID int64) {
	if _, err := h.send(ctx, chatID, fmt.Sprintf(msgWelcome, h.botName), startKeyboard()); err != nil {
		log.Printf("start to %d: %v", chatID, err)
	}
}

func (h *handler) about(ctx context.Context, chatID int64) {
	h.text(ctx, chatID, msgAbout)
}

func (h *handler) status(ctx context.Context, chatID int64) {
	c, ok, err := h.svc.Subscription(ctx, chatID)
	switch {
	case err != nil:
		log.Printf("subscription of %d: %v", chatID, err)
	case !ok:
		h.text(ctx, chatID, msgStatusInactive)
	default:
		h.text(ctx, chatID, fmt.Sprintf(msgStatusActive, c.NotifyTime, c.Timezone, formatDifficulties(c.Difficulties)))
	}
}

func (h *handler) rating(ctx context.Context, chatID int64) {
	c, ok, err := h.svc.Subscription(ctx, chatID)
	switch {
	case err != nil:
		log.Printf("subscription of %d: %v", chatID, err)
	case !ok:
		h.text(ctx, chatID, msgRatingEmpty)
	default:
		h.text(ctx, chatID, formatRating(c.Standings()))
	}
}

func (h *handler) unsubscribe(ctx context.Context, chatID int64) {
	if err := h.svc.Unsubscribe(ctx, chatID); err != nil {
		log.Printf("unsubscribe %d: %v", chatID, err)
	}
	h.text(ctx, chatID, msgDisabled)
}
