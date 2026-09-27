package bot

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

func (b *tgBot) handleStart(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(msgWelcome, b.botName))
	msg.ParseMode = parseMode
	msg.ReplyMarkup = startKeyboard()
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("handleStart to %d: %v", chatID, err)
	}
}

func (b *tgBot) handleAbout(chatID int64) {
	b.sendMsg(chatID, msgAbout)
}

// handleSetup starts /setup over with a fresh prompt, discarding any other
// in-progress session, so an abandoned one never blocks it.
func (b *tgBot) handleSetup(chatID int64) {
	b.states.clearAndGetPending(chatID)
	b.states.set(chatID, stateAwaitingTime)
	sent, err := b.sendWithKB(chatID, msgChooseTime, setupTimeKeyboard())
	if err != nil {
		log.Printf("handleSetup to %d: %v", chatID, err)
		b.states.clearAndGetPending(chatID)
		return
	}
	b.states.setSetupMsgID(chatID, sent.MessageID)
}

func (b *tgBot) handleToday(chatID int64) {
	b.SendDailyProblem(chatID)
}

// handleDaily sends the official daily whatever the chat's difficulty, so it
// neither reads nor saves the pick of the day and works without a subscription.
func (b *tgBot) handleDaily(chatID int64) {
	daily, err := b.lc.FetchDaily()
	if err != nil {
		log.Printf("FetchDaily: %v", err)
		b.sendMsg(chatID, msgFetchFailed)
		return
	}
	b.sendProblem(chatID, leetcode.FormatProblem(daily))
}

func (b *tgBot) handleStatus(chatID int64) {
	cfg, ok := b.store.Get(chatID)
	if !ok {
		b.sendMsg(chatID, msgStatusInactive)
		return
	}
	b.sendMsg(chatID, fmt.Sprintf(msgStatusActive, cfg.NotifyTime, cfg.Timezone, formatDifficulties(cfg.Difficulties)))
}

func (b *tgBot) handleUnsubscribe(chatID int64) {
	if err := b.store.Delete(chatID); err != nil {
		log.Printf("store.Delete: %v", err)
	}
	b.sched.Remove(chatID)
	b.sendMsg(chatID, msgDisabled)
}

func (b *tgBot) handleRating(chatID int64) {
	cfg, ok := b.store.Get(chatID)
	if !ok || len(cfg.Members) == 0 {
		b.sendMsg(chatID, msgRatingEmpty)
		return
	}

	type entry struct {
		name  string
		count int
	}
	entries := make([]entry, 0, len(cfg.Members))
	for _, stat := range cfg.Members {
		entries = append(entries, entry{stat.Name, stat.Count})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].count > entries[j].count
	})

	if len(entries) == 1 {
		b.sendMsg(chatID, fmt.Sprintf("🏆 Solved: <b>%d</b>", entries[0].count))
		return
	}

	medals := []string{"🥇", "🥈", "🥉"}
	var sb strings.Builder
	sb.WriteString("🏆 <b>Rating</b>\n\n")
	for i, e := range entries {
		if i < len(medals) {
			fmt.Fprintf(&sb, "%s %s — %d\n", medals[i], e.name, e.count)
		} else {
			fmt.Fprintf(&sb, "%d. %s — %d\n", i+1, e.name, e.count)
		}
	}
	b.sendMsg(chatID, sb.String())
}

// handleDone counts the presser's solve for today in one atomic update, so a
// pick of the day saved concurrently by a cron send is kept.
func (b *tgBot) handleDone(cb *tgbotapi.CallbackQuery, chatID int64, _ int) {
	today := time.Now().UTC().Format("2006-01-02")
	key := fmt.Sprintf("%d", cb.From.ID)
	name := cb.From.FirstName
	if name == "" {
		name = "@" + cb.From.UserName
	}

	var stat storage.UserStat
	counted := false
	found, err := b.store.Update(chatID, func(cfg *storage.ChatConfig) bool {
		stat = cfg.Members[key]
		if stat.LastSolvedDate == today {
			return false
		}
		stat.Name = name
		stat.Count++
		stat.LastSolvedDate = today
		if cfg.Members == nil {
			cfg.Members = make(map[string]storage.UserStat)
		}
		cfg.Members[key] = stat
		counted = true
		return true
	})
	if err != nil {
		log.Printf("store.Update: %v", err)
	}

	switch {
	case !found:
		b.answerCB(cb.ID, msgNotSubscribed)
	case !counted:
		b.answerCB(cb.ID, "Already counted today!")
	default:
		b.answerCB(cb.ID, fmt.Sprintf("✅ Counted! Your total: %d", stat.Count))
	}
}
