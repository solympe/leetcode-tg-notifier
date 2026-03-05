package bot

import (
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func isCommand(text, cmd string) bool {
	return text == cmd || strings.HasPrefix(text, cmd+"@")
}

func (b *tgBot) answerCB(id, text string) {
	if _, err := b.api.Request(tgbotapi.NewCallback(id, text)); err != nil {
		log.Printf("answer callback: %v", err)
	}
}

func (b *tgBot) handleCallback(cb *tgbotapi.CallbackQuery) {
	if cb.Message == nil {
		b.answerCB(cb.ID, msgMessageExpired)
		return
	}
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID

	switch {
	case cb.Data == cbCmdSetup:
		b.answerCB(cb.ID, "")
		b.handleSetup(chatID)

	case cb.Data == cbCmdToday:
		b.answerCB(cb.ID, "")
		b.SendDailyProblem(chatID)

	case cb.Data == cbCmdStatus:
		b.answerCB(cb.ID, "")
		b.handleStatus(chatID)

	case cb.Data == cbCmdRating:
		b.answerCB(cb.ID, "")
		b.handleRating(chatID)

	case cb.Data == cbCmdUnsub:
		b.answerCB(cb.ID, "")
		b.handleUnsubscribe(chatID)

	case cb.Data == cbCmdDone:
		b.handleDone(cb, chatID, msgID)

	case strings.HasPrefix(cb.Data, cbPrefixTime):
		b.answerCB(cb.ID, "")
		t := strings.TrimPrefix(cb.Data, cbPrefixTime)
		b.states.set(chatID, stateAwaitingTimezone)
		b.states.setPendingTime(chatID, t)
		b.states.setSetupMsgID(chatID, msgID)
		b.editMsgWithKB(chatID, msgID, fmt.Sprintf(msgChooseTz, t), setupTzKeyboard())

	case strings.HasPrefix(cb.Data, cbPrefixTz):
		b.answerCB(cb.ID, "")
		tz := strings.TrimPrefix(cb.Data, cbPrefixTz)
		notifyTime := b.states.clearAndGetPending(chatID)
		if notifyTime == "" {
			b.sendMsg(chatID, msgSessionExpired)
			return
		}
		b.finishSetup(chatID, notifyTime, tz)
		b.editMsg(chatID, msgID, fmt.Sprintf(msgAllSet, notifyTime, tz))
	}
}

func (b *tgBot) handleMessage(update tgbotapi.Update) {
	if update.CallbackQuery != nil {
		b.handleCallback(update.CallbackQuery)
		return
	}
	if update.Message == nil {
		return
	}

	chatID := update.Message.Chat.ID
	text := strings.TrimSpace(update.Message.Text)

	if !strings.HasPrefix(text, "/") {
		switch b.states.get(chatID) {
		case stateAwaitingTime:
			b.handleAwaitingTime(chatID, text)
			return
		case stateAwaitingTimezone:
			b.handleAwaitingTimezone(chatID, text)
			return
		}
	}

	switch {
	case isCommand(text, cmdStart):
		b.handleStart(chatID)
	case isCommand(text, cmdAbout):
		b.handleAbout(chatID)
	case isCommand(text, cmdSetup):
		b.handleSetup(chatID)
	case isCommand(text, cmdToday):
		b.handleToday(chatID)
	case isCommand(text, cmdStatus):
		b.handleStatus(chatID)
	case isCommand(text, cmdRating):
		b.handleRating(chatID)
	case isCommand(text, cmdUnsubscribe):
		b.handleUnsubscribe(chatID)
	}
}
