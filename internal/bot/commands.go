package bot

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

func (b *Bot) handleStart(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf(msgWelcome, b.botName))
	msg.ParseMode = parseMode
	msg.ReplyMarkup = startKeyboard()
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("handleStart to %d: %v", chatID, err)
	}
}

func (b *Bot) handleAbout(chatID int64) {
	b.sendMsg(chatID, msgAbout)
}

func (b *Bot) handleSetup(chatID int64) {
	if s := b.states.get(chatID); s == stateAwaitingTime || s == stateAwaitingTimezone {
		return
	}
	b.states.set(chatID, stateAwaitingTime)
	sent, err := b.sendWithKB(chatID, msgChooseTime, setupTimeKeyboard())
	if err != nil {
		log.Printf("handleSetup to %d: %v", chatID, err)
		return
	}
	b.states.setSetupMsgID(chatID, sent.MessageID)
}

func (b *Bot) handleToday(chatID int64) {
	b.SendDailyProblem(chatID)
}

func (b *Bot) handleStatus(chatID int64) {
	cfg, ok := b.store.Get(chatID)
	if !ok {
		b.sendMsg(chatID, msgStatusInactive)
		return
	}
	b.sendMsg(chatID, fmt.Sprintf(msgStatusActive, cfg.NotifyTime, cfg.Timezone))
}

func (b *Bot) handleUnsubscribe(chatID int64) {
	if err := b.store.Delete(chatID); err != nil {
		log.Printf("store.Delete: %v", err)
	}
	b.sched.Remove(chatID)
	b.sendMsg(chatID, msgDisabled)
}

func (b *Bot) handleRating(chatID int64) {
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

func (b *Bot) handleDone(cb *tgbotapi.CallbackQuery, chatID int64, _ int) {
	today := time.Now().UTC().Format("2006-01-02")
	userID := cb.From.ID

	cfg, ok := b.store.Get(chatID)
	if !ok {
		b.answerCB(cb.ID, "")
		return
	}
	if cfg.Members == nil {
		cfg.Members = make(map[string]storage.UserStat)
	}

	key := fmt.Sprintf("%d", userID)
	stat := cfg.Members[key]
	if stat.LastSolvedDate == today {
		b.answerCB(cb.ID, "Already counted today!")
		return
	}

	name := cb.From.FirstName
	if name == "" {
		name = "@" + cb.From.UserName
	}

	stat.Name = name
	stat.Count++
	stat.LastSolvedDate = today
	cfg.Members[key] = stat
	if err := b.store.Set(cfg); err != nil {
		log.Printf("store.Set: %v", err)
	}

	b.answerCB(cb.ID, fmt.Sprintf("✅ Counted! Your total: %d", stat.Count))
}
