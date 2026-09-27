package bot

import (
	"fmt"
	"log"
	"slices"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

// formatDifficulties renders a subscribed difficulty set; empty means any.
func formatDifficulties(ds []string) string {
	if len(ds) == 0 {
		return "Any"
	}
	return strings.Join(ds, ", ")
}

// initialDifficulties is what a difficulty keyboard starts with: the saved
// selection, or every difficulty when there is none.
func initialDifficulties(saved []string) []string {
	if len(saved) == 0 {
		return leetcode.AllDifficulties()
	}
	return saved
}

// handleDifficulty starts a /difficulty session for a subscribed chat,
// discarding any other in-progress session.
func (b *tgBot) handleDifficulty(chatID int64) {
	cfg, ok := b.store.Get(chatID)
	if !ok {
		b.sendMsg(chatID, msgNotSubscribed)
		return
	}
	selected := initialDifficulties(cfg.Difficulties)

	b.states.clearAndGetPending(chatID)
	b.states.set(chatID, stateEditingDifficulty)
	b.states.setPendingDifficulties(chatID, selected)
	sent, err := b.sendWithKB(chatID, msgChooseDifficulty, difficultyKeyboard(selected))
	if err != nil {
		log.Printf("handleDifficulty to %d: %v", chatID, err)
		b.states.clearAndGetPending(chatID)
		return
	}
	b.states.setSetupMsgID(chatID, sent.MessageID)
}

// inDifficultySession reports whether msgID is the difficulty keyboard of the
// chat's active /setup or /difficulty session.
func (b *tgBot) inDifficultySession(chatID int64, msgID int) bool {
	s := b.states.get(chatID)
	return (s == stateAwaitingDifficulty || s == stateEditingDifficulty) && b.states.getSetupMsgID(chatID) == msgID
}

func (b *tgBot) handleDifficultyToggle(cb *tgbotapi.CallbackQuery, chatID int64, msgID int) {
	if !b.inDifficultySession(chatID, msgID) {
		b.answerCB(cb.ID, msgMenuExpired)
		return
	}
	d := strings.TrimPrefix(cb.Data, cbPrefixDiff)
	if !slices.Contains(leetcode.AllDifficulties(), d) {
		b.answerCB(cb.ID, "")
		return
	}
	selected := b.states.toggleDifficulty(chatID, d)
	b.answerCB(cb.ID, "")
	b.editKB(chatID, msgID, difficultyKeyboard(selected))
}

func (b *tgBot) handleDifficultySave(cb *tgbotapi.CallbackQuery, chatID int64, msgID int) {
	if !b.inDifficultySession(chatID, msgID) {
		b.answerCB(cb.ID, msgMenuExpired)
		return
	}
	if len(b.states.getPendingDifficulties(chatID)) == 0 {
		b.answerCB(cb.ID, msgPickAtLeastOne)
		return
	}
	b.answerCB(cb.ID, "")
	if b.states.get(chatID) == stateAwaitingDifficulty {
		b.saveSetup(chatID, msgID)
		return
	}
	b.saveDifficulties(chatID, msgID)
}

// saveDifficulties stores the /difficulty selection in one atomic update, so
// a change saved concurrently (such as a cron send's pick) survives and a
// removed chat is not re-created. The pick of the day is kept: sends reuse it
// only while its difficulty stays subscribed.
func (b *tgBot) saveDifficulties(chatID int64, msgID int) {
	difficulties := b.states.clearAndGetPending(chatID).difficulties
	found, err := b.store.Update(chatID, func(cfg *storage.ChatConfig) bool {
		cfg.Difficulties = difficulties
		return true
	})
	if err != nil {
		log.Printf("store.Update: %v", err)
	}
	if !found {
		b.editMsg(chatID, msgID, msgNotSubscribed)
		return
	}
	b.editMsg(chatID, msgID, fmt.Sprintf(msgDifficultyUpdated, formatDifficulties(difficulties)))
}
