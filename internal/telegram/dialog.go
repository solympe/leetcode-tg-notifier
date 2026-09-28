package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// step is where a chat's /setup or /difficulty dialog is; 0 means none.
type step int

const (
	stepTime step = iota + 1
	stepTimezone
	stepSetupDifficulty
	stepEditDifficulty
)

type session struct {
	step                 step
	msgID                int // the one message whose buttons are live
	notifyTime, timezone string
	selected             []string // canonical order; replaced, never modified in place
}

func (s session) onDifficulty(msgID int) bool {
	return (s.step == stepSetupDifficulty || s.step == stepEditDifficulty) && s.msgID == msgID
}

// sessions holds the dialogs in progress, in memory: a restart loses them.
type sessions struct {
	mu sync.Mutex
	m  map[int64]session
}

// get returns chatID's session; the zero step means none.
func (ss *sessions) get(chatID int64) session {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.m[chatID]
}

func (ss *sessions) set(chatID int64, s session) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.m[chatID] = s
}

func (ss *sessions) drop(chatID int64) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.m, chatID)
}

// startSetup starts /setup over with a fresh prompt, discarding any other
// session, so an abandoned one never blocks it.
func (h *handler) startSetup(ctx context.Context, chatID int64) {
	h.sessions.drop(chatID)
	msgID, err := h.send(ctx, chatID, msgChooseTime, timeKeyboard())
	if err != nil {
		log.Printf("setup prompt to %d: %v", chatID, err)
		return
	}
	h.sessions.set(chatID, session{step: stepTime, msgID: msgID})
}

// startDifficulty starts a /difficulty session for a subscribed chat,
// discarding any other session. An unsubscribed chat keeps its session.
func (h *handler) startDifficulty(ctx context.Context, chatID int64) {
	c, ok, err := h.svc.Subscription(ctx, chatID)
	if err != nil {
		log.Printf("subscription of %d: %v", chatID, err)
		return
	}
	if !ok {
		h.text(ctx, chatID, msgNotSubscribed)
		return
	}
	h.sessions.drop(chatID)
	selected := initialDifficulties(c.Difficulties)
	msgID, err := h.send(ctx, chatID, msgChooseDifficulty, difficultyKeyboard(selected))
	if err != nil {
		log.Printf("difficulty prompt to %d: %v", chatID, err)
		return
	}
	h.sessions.set(chatID, session{step: stepEditDifficulty, msgID: msgID, selected: selected})
}

func (h *handler) onText(ctx context.Context, chatID int64, text string) {
	s := h.sessions.get(chatID)
	switch {
	case s.step == stepTime && validTime(text):
		h.chooseTime(ctx, chatID, s, text)
	case s.step == stepTime:
		h.edit(ctx, chatID, s.msgID, msgInvalidTime, timeKeyboard())
	case s.step == stepTimezone && validTimezone(text):
		h.chooseTimezone(ctx, chatID, s, text)
	case s.step == stepTimezone:
		h.edit(ctx, chatID, s.msgID, msgInvalidTz, tzKeyboard())
	}
}

// stepButton accepts a valid choice pressed on the prompt of step want.
func (h *handler) stepButton(chatID int64, msgID int, want step, valid bool, next func(session)) (string, func()) {
	s := h.sessions.get(chatID)
	if s.step != want || s.msgID != msgID || !valid {
		return msgMenuExpired, nil
	}
	return "", func() { next(s) }
}

func (h *handler) diffButton(ctx context.Context, chatID int64, msgID int, d string) (string, func()) {
	s := h.sessions.get(chatID)
	if !s.onDifficulty(msgID) {
		return msgMenuExpired, nil
	}
	if !slices.Contains(domain.Difficulties(), d) {
		return "", nil
	}
	return "", func() {
		s.selected = toggle(s.selected, d)
		h.sessions.set(chatID, s)
		h.editKeyboard(ctx, chatID, msgID, difficultyKeyboard(s.selected))
	}
}

func (h *handler) saveButton(ctx context.Context, chatID int64, msgID int) (string, func()) {
	s := h.sessions.get(chatID)
	if !s.onDifficulty(msgID) {
		return msgMenuExpired, nil
	}
	if len(s.selected) == 0 {
		return msgPickAtLeastOne, nil
	}
	return "", func() {
		h.sessions.drop(chatID)
		if s.step == stepSetupDifficulty {
			h.finishSetup(ctx, chatID, s)
			return
		}
		h.saveDifficulties(ctx, chatID, s)
	}
}

func (h *handler) chooseTime(ctx context.Context, chatID int64, s session, t string) {
	s.step, s.notifyTime = stepTimezone, t
	h.sessions.set(chatID, s)
	h.edit(ctx, chatID, s.msgID, fmt.Sprintf(msgChooseTz, t), tzKeyboard())
}

// chooseTimezone records the zone and turns the prompt into the difficulty
// step, with the chat's saved difficulties ticked. When the subscription
// cannot be read, the session stays at the timezone step.
func (h *handler) chooseTimezone(ctx context.Context, chatID int64, s session, zone string) {
	c, _, err := h.svc.Subscription(ctx, chatID)
	if err != nil {
		log.Printf("subscription of %d: %v", chatID, err)
		return
	}
	s.step, s.timezone, s.selected = stepSetupDifficulty, zone, initialDifficulties(c.Difficulties)
	h.sessions.set(chatID, s)
	h.edit(ctx, chatID, s.msgID, msgChooseDifficulty, difficultyKeyboard(s.selected))
}

// finishSetup saves and schedules the subscription. A failed save is logged
// and the user still sees All set.
func (h *handler) finishSetup(ctx context.Context, chatID int64, s session) {
	if err := h.svc.Subscribe(ctx, chatID, s.notifyTime, s.timezone, s.selected); err != nil {
		log.Printf("subscribe %d: %v", chatID, err)
	}
	h.edit(ctx, chatID, s.msgID, fmt.Sprintf(msgAllSet, s.notifyTime, s.timezone, formatDifficulties(s.selected)), nil)
}

func (h *handler) saveDifficulties(ctx context.Context, chatID int64, s session) {
	text := fmt.Sprintf(msgDifficultyUpdated, formatDifficulties(s.selected))
	err := h.svc.SetDifficulties(ctx, chatID, s.selected)
	switch {
	case errors.Is(err, domain.ErrNotSubscribed):
		text = msgNotSubscribed
	case err != nil:
		log.Printf("set difficulties of %d: %v", chatID, err)
	}
	h.edit(ctx, chatID, s.msgID, text, nil)
}
