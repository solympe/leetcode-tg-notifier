package bot

import (
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

func (b *tgBot) SendDailyProblem(chatID int64) {
	daily, err := b.lc.FetchDaily()
	if err != nil {
		log.Printf("FetchDaily: %v", err)
		b.sendMsg(chatID, msgFetchFailed)
		return
	}
	text, err := b.problemText(chatID, daily)
	if err != nil {
		log.Printf("FetchRandom for %d: %v", chatID, err)
		b.sendMsg(chatID, msgFetchFailed)
		return
	}
	b.sendProblem(chatID, text)
}

// sendProblem sends a problem's text with the Done button and removes the
// subscription of a chat that blocked the bot.
func (b *tgBot) sendProblem(chatID int64, text string) {
	if _, err := b.sendWithKB(chatID, text, doneKeyboard()); err != nil {
		log.Printf("sendProblem to %d: %v", chatID, err)
		if isBotBlocked(err) {
			b.removeSubscription(chatID)
		}
	}
}

// problemText formats the problem chatID gets today: the daily when its
// difficulty is subscribed, otherwise a random problem of a subscribed
// difficulty, picked once per daily date so every send that day repeats it
// while its difficulty stays subscribed. It runs under the chat's pick lock,
// so a send concurrent with another one to the chat (a cron send and /today)
// waits for its pick and repeats it instead of fetching a second problem.
func (b *tgBot) problemText(chatID int64, daily *leetcode.Problem) (string, error) {
	unlock := b.pickLocks.lock(chatID)
	defer unlock()

	cfg, ok := b.store.Get(chatID)
	if !ok || wantsDaily(cfg.Difficulties, daily.Difficulty) {
		return leetcode.FormatProblem(daily), nil
	}
	if validPick(cfg, daily) {
		return leetcode.FormatRandomProblem(pickedProblem(cfg.DailyPick), cfg.DailyPick.DailyDifficulty), nil
	}

	p, err := b.lc.FetchRandom(cfg.Difficulties)
	if err != nil {
		return "", err
	}
	p.Date = daily.Date
	b.savePick(chatID, newDailyPick(p, daily.Difficulty))
	return leetcode.FormatRandomProblem(p, daily.Difficulty), nil
}

// wantsDaily reports whether a chat subscribed to set gets a daily of
// difficulty d as is; an empty set means any difficulty.
func wantsDaily(set []string, d string) bool {
	return len(set) == 0 || slices.ContainsFunc(set, func(s string) bool { return strings.EqualFold(s, d) })
}

// validPick reports whether the stored pick can be resent in place of daily:
// it replaces that daily's date and its difficulty is still subscribed, which
// also rejects a pick fetched for a selection changed during the fetch.
func validPick(cfg storage.ChatConfig, daily *leetcode.Problem) bool {
	pick := cfg.DailyPick
	return pick != nil && pick.Date == daily.Date && wantsDaily(cfg.Difficulties, pick.Difficulty)
}

// savePick stores the pick of the day on the chat's current config in one
// atomic update, so a change saved while the pick was being fetched or saved
// (such as a Done press) is kept, and a removed chat is not re-created. A
// failed save only means a new pick on the next send.
func (b *tgBot) savePick(chatID int64, pick *storage.DailyPick) {
	_, err := b.store.Update(chatID, func(cfg *storage.ChatConfig) bool {
		cfg.DailyPick = pick
		return true
	})
	if err != nil {
		log.Printf("store.Update pick for %d: %v", chatID, err)
	}
}

func newDailyPick(p *leetcode.Problem, dailyDifficulty string) *storage.DailyPick {
	return &storage.DailyPick{
		Date:            p.Date,
		DailyDifficulty: dailyDifficulty,
		ID:              p.ID,
		Title:           p.Title,
		Link:            p.Link,
		Difficulty:      p.Difficulty,
		Tags:            p.Tags,
	}
}

func pickedProblem(pick *storage.DailyPick) *leetcode.Problem {
	return &leetcode.Problem{
		Date:       pick.Date,
		Link:       pick.Link,
		ID:         pick.ID,
		Title:      pick.Title,
		Difficulty: pick.Difficulty,
		Tags:       pick.Tags,
	}
}

// isBotBlocked reports whether the Telegram API error indicates
// the bot was blocked by the user (HTTP 403 Forbidden).
func isBotBlocked(err error) bool {
	var tgErr *tgbotapi.Error
	return errors.As(err, &tgErr) && tgErr.Code == http.StatusForbidden
}

// removeSubscription removes the cron job and persistent config for chatID.
func (b *tgBot) removeSubscription(chatID int64) {
	log.Printf("bot blocked by %d: removing subscription", chatID)
	b.sched.Remove(chatID)
	if err := b.store.Delete(chatID); err != nil {
		log.Printf("store.Delete %d: %v", chatID, err)
	}
}

func (b *tgBot) sendMsg(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("sendMsg to %d: %v", chatID, err)
	}
}

// sendWithKB sends a message with an inline keyboard and returns the sent message.
// The caller is responsible for handling the returned error.
func (b *tgBot) sendWithKB(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) (tgbotapi.Message, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode
	msg.ReplyMarkup = kb
	return b.api.Send(msg)
}

func (b *tgBot) editMsg(chatID int64, msgID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = parseMode
	if _, err := b.api.Send(edit); err != nil {
		log.Printf("editMsg to %d: %v", chatID, err)
	}
}

func (b *tgBot) editMsgWithKB(chatID int64, msgID int, text string, kb tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = parseMode
	edit.ReplyMarkup = &kb
	if _, err := b.api.Send(edit); err != nil {
		log.Printf("editMsgWithKB to %d: %v", chatID, err)
	}
}

func (b *tgBot) editKB(chatID int64, msgID int, kb tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageReplyMarkup(chatID, msgID, kb)
	if _, err := b.api.Send(edit); err != nil {
		log.Printf("editKB to %d: %v", chatID, err)
	}
}
