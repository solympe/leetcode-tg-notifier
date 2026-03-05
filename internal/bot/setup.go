package bot

import (
	"fmt"
	"log"
	"regexp"
	"time"

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

func (b *tgBot) handleAwaitingTimezone(chatID int64, text string) {
	msgID := b.states.getSetupMsgID(chatID)
	if _, err := time.LoadLocation(text); err != nil {
		b.editMsgWithKB(chatID, msgID, msgInvalidTz, setupTzKeyboard())
		return
	}
	notifyTime := b.states.clearAndGetPending(chatID)
	if notifyTime == "" {
		b.sendMsg(chatID, msgSessionExpired)
		return
	}
	b.finishSetup(chatID, notifyTime, text)
	b.editMsg(chatID, msgID, fmt.Sprintf(msgAllSet, notifyTime, text))
}

func (b *tgBot) finishSetup(chatID int64, notifyTime, timezone string) {
	cfg := storage.ChatConfig{
		ChatID:     chatID,
		NotifyTime: notifyTime,
		Timezone:   timezone,
	}
	if err := b.store.Set(cfg); err != nil {
		log.Printf("store.Set: %v", err)
	}
	if err := b.sched.Schedule(chatID, cfg); err != nil {
		log.Printf("sched.Schedule: %v", err)
	}
}
