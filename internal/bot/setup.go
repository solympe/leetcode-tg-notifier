package bot

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

var timeRegexp = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

func (b *tgBot) handleAwaitingTime(chatID int64, text string) {
	msgID := b.states.getSetupMsgID(chatID)
	if !timeRegexp.MatchString(text) {
		b.editMsgWithKB(chatID, msgID, msgInvalidTime, setupTimeKeyboard())
		return
	}
	b.states.set(chatID, stateAwaitingTimezone)
	b.states.setPendingTime(chatID, text)
	b.editMsgWithKB(chatID, msgID, fmt.Sprintf(msgChooseTz, text), setupTzKeyboard())
}

// inTimeStep reports whether msgID is the time keyboard of the chat's active
// /setup session.
func (b *tgBot) inTimeStep(chatID int64, msgID int) bool {
	return b.states.get(chatID) == stateAwaitingTime && b.states.getSetupMsgID(chatID) == msgID
}

// handleTimeButton applies a time button; one on any other message or in
// another step is stale and changes nothing, as does a forged payload that is
// not a time.
func (b *tgBot) handleTimeButton(cb *tgbotapi.CallbackQuery, chatID int64, msgID int) {
	t := strings.TrimPrefix(cb.Data, cbPrefixTime)
	if !b.inTimeStep(chatID, msgID) || !timeRegexp.MatchString(t) {
		b.answerCB(cb.ID, msgMenuExpired)
		return
	}
	b.answerCB(cb.ID, "")
	b.states.set(chatID, stateAwaitingTimezone)
	b.states.setPendingTime(chatID, t)
	b.editMsgWithKB(chatID, msgID, fmt.Sprintf(msgChooseTz, t), setupTzKeyboard())
}

// validTimezone reports whether text names an IANA timezone. time.LoadLocation
// also accepts "" (UTC) and "Local" (the server's timezone), which are not
// timezones a user picks.
func validTimezone(text string) bool {
	if text == "" || text == "Local" {
		return false
	}
	_, err := time.LoadLocation(text)
	return err == nil
}

func (b *tgBot) handleAwaitingTimezone(chatID int64, text string) {
	msgID := b.states.getSetupMsgID(chatID)
	if !validTimezone(text) {
		b.editMsgWithKB(chatID, msgID, msgInvalidTz, setupTzKeyboard())
		return
	}
	b.chooseSetupDifficulty(chatID, msgID, text)
}

// inTimezoneStep reports whether msgID is the timezone keyboard of the chat's
// active /setup session.
func (b *tgBot) inTimezoneStep(chatID int64, msgID int) bool {
	return b.states.get(chatID) == stateAwaitingTimezone && b.states.getSetupMsgID(chatID) == msgID
}

// handleTimezoneButton applies a timezone button; one on any other message or
// in another step is stale and changes nothing, as does a forged payload that
// is not a timezone.
func (b *tgBot) handleTimezoneButton(cb *tgbotapi.CallbackQuery, chatID int64, msgID int) {
	tz := strings.TrimPrefix(cb.Data, cbPrefixTz)
	if !b.inTimezoneStep(chatID, msgID) || !validTimezone(tz) {
		b.answerCB(cb.ID, msgMenuExpired)
		return
	}
	b.answerCB(cb.ID, "")
	b.chooseSetupDifficulty(chatID, msgID, tz)
}

// chooseSetupDifficulty records the chosen timezone and turns setup message
// msgID into the difficulty step, with the chat's saved difficulties ticked.
func (b *tgBot) chooseSetupDifficulty(chatID int64, msgID int, timezone string) {
	if b.states.getPendingTime(chatID) == "" {
		b.states.clearAndGetPending(chatID)
		b.sendMsg(chatID, msgSessionExpired)
		return
	}
	cfg, _ := b.store.Get(chatID)
	selected := initialDifficulties(cfg.Difficulties)
	b.states.setPendingTz(chatID, timezone)
	b.states.setPendingDifficulties(chatID, selected)
	b.states.setSetupMsgID(chatID, msgID)
	b.states.set(chatID, stateAwaitingDifficulty)
	b.editMsgWithKB(chatID, msgID, msgChooseDifficulty, difficultyKeyboard(selected))
}

// saveSetup finishes /setup with the pending time, timezone and difficulties.
func (b *tgBot) saveSetup(chatID int64, msgID int) {
	p := b.states.clearAndGetPending(chatID)
	if p.notifyTime == "" || p.timezone == "" {
		b.sendMsg(chatID, msgSessionExpired)
		return
	}
	b.finishSetup(chatID, p.notifyTime, p.timezone, p.difficulties)
	b.editMsg(chatID, msgID, fmt.Sprintf(msgAllSet, p.notifyTime, p.timezone, formatDifficulties(p.difficulties)))
}

// finishSetup saves and schedules the subscription. An existing one gets only
// the fields setup owns in one atomic update, so its rating and a pick of the
// day saved concurrently by a cron send are kept.
func (b *tgBot) finishSetup(chatID int64, notifyTime, timezone string, difficulties []string) {
	cfg := storage.ChatConfig{ChatID: chatID, NotifyTime: notifyTime, Timezone: timezone, Difficulties: difficulties}
	found, err := b.store.Update(chatID, func(stored *storage.ChatConfig) bool {
		stored.ChatID = cfg.ChatID
		stored.NotifyTime = cfg.NotifyTime
		stored.Timezone = cfg.Timezone
		stored.Difficulties = cfg.Difficulties
		cfg = *stored
		return true
	})
	if err != nil {
		log.Printf("store.Update: %v", err)
	}
	if !found {
		if err := b.store.Set(cfg); err != nil {
			log.Printf("store.Set: %v", err)
		}
	}
	if err := b.sched.Schedule(chatID, cfg); err != nil {
		log.Printf("sched.Schedule: %v", err)
	}
}
