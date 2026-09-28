package telegram

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// Commands, the menu buttons' callback data among them, and the other
// callback data. Buttons on messages sent by earlier versions carry these
// strings, so they must never change.
const (
	cmdSetup       = "/setup"
	cmdToday       = "/today"
	cmdDaily       = "/daily"
	cmdStatus      = "/status"
	cmdUnsubscribe = "/unsubscribe"
	cmdRating      = "/rating"
	cmdDifficulty  = "/difficulty"

	cbDone       = "done"
	cbDiffSave   = "diffsave" // must not start with cbPrefixDiff
	cbPrefixTime = "time:"
	cbPrefixTz   = "tz:"
	cbPrefixDiff = "diff:"

	parseMode = "HTML"
)

// User-visible texts, byte for byte as in the old internal/bot/const.go and
// commands.go.
const (
	msgFetchFailed = "⚠️ Failed to fetch the problem from LeetCode. Try /today later."
	msgRatingEmpty = "No solves yet. Be the first to press ✅ Done!"
)

var timeRegexp = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

func btn(label, data string) tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardButtonData(label, data)
}

func keyboard(rows ...[]tgbotapi.InlineKeyboardButton) *tgbotapi.InlineKeyboardMarkup {
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return &kb
}

func startKeyboard() *tgbotapi.InlineKeyboardMarkup {
	return keyboard(
		tgbotapi.NewInlineKeyboardRow(
			btn("📅 Today's problem", cmdToday),
			btn("🗓 LeetCode daily", cmdDaily),
			btn("⚙️ Setup", cmdSetup),
		),
		tgbotapi.NewInlineKeyboardRow(
			btn("ℹ️ Status", cmdStatus),
			btn("🏆 Rating", cmdRating),
		),
		tgbotapi.NewInlineKeyboardRow(
			btn("🎚 Difficulty", cmdDifficulty),
			btn("🛑 Unsubscribe", cmdUnsubscribe),
		),
	)
}

func doneKeyboard() *tgbotapi.InlineKeyboardMarkup {
	return keyboard(tgbotapi.NewInlineKeyboardRow(btn("✅ Done", cbDone)))
}

func timeKeyboard() *tgbotapi.InlineKeyboardMarkup {
	return keyboard(
		tgbotapi.NewInlineKeyboardRow(
			btn("7:00", cbPrefixTime+"07:00"),
			btn("8:00", cbPrefixTime+"08:00"),
			btn("9:00", cbPrefixTime+"09:00"),
			btn("10:00", cbPrefixTime+"10:00"),
		),
		tgbotapi.NewInlineKeyboardRow(
			btn("18:00", cbPrefixTime+"18:00"),
			btn("19:00", cbPrefixTime+"19:00"),
			btn("20:00", cbPrefixTime+"20:00"),
			btn("21:00", cbPrefixTime+"21:00"),
		),
	)
}

// tzKeyboard labels six zones with their current UTC offset, so the labels
// change with DST; the callback data does not.
func tzKeyboard() *tgbotapi.InlineKeyboardMarkup {
	zone := func(city, name string) tgbotapi.InlineKeyboardButton {
		return btn(tzLabel(city, name), cbPrefixTz+name)
	}
	return keyboard(
		tgbotapi.NewInlineKeyboardRow(zone("New York", "America/New_York"), zone("London", "Europe/London")),
		tgbotapi.NewInlineKeyboardRow(zone("Lisbon", "Europe/Lisbon"), btn("UTC+0", cbPrefixTz+"UTC")),
		tgbotapi.NewInlineKeyboardRow(zone("Moscow", "Europe/Moscow"), zone("Dubai", "Asia/Dubai")),
		tgbotapi.NewInlineKeyboardRow(zone("Bangkok", "Asia/Bangkok")),
	)
}

// tzLabel renders "UTC+3 Moscow" or "UTC+5:30 Kolkata" for zone's current
// offset, or just city when zone does not load.
func tzLabel(city, zone string) string {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return city
	}
	_, offset := time.Now().In(loc).Zone()
	h, m := offset/3600, (offset%3600)/60
	sign := "+"
	if h < 0 {
		sign = ""
	}
	if m == 0 {
		return fmt.Sprintf("UTC%s%d %s", sign, h, city)
	}
	return fmt.Sprintf("UTC%s%d:%02d %s", sign, h, m, city)
}

// difficultyKeyboard shows a toggle per difficulty, ticked when in selected,
// and a Save button.
func difficultyKeyboard(selected []string) *tgbotapi.InlineKeyboardMarkup {
	var toggles []tgbotapi.InlineKeyboardButton
	for _, d := range domain.Difficulties() {
		label := "⬜ " + d
		if slices.Contains(selected, d) {
			label = "✅ " + d
		}
		toggles = append(toggles, btn(label, cbPrefixDiff+d))
	}
	return keyboard(toggles, tgbotapi.NewInlineKeyboardRow(btn("💾 Save", cbDiffSave)))
}

// formatPick renders the problem a chat is sent: the official daily, or a
// random problem replacing a daily of p.DailyDifficulty.
func formatPick(p domain.Pick) string {
	if p.DailyDifficulty == "" {
		return fmt.Sprintf("📅 LeetCode Daily — %s\n\n%s", formatDate(p.Date), formatBody(p.Problem))
	}
	return fmt.Sprintf(
		"🎲 LeetCode Random — %s\nToday's daily is %s, so here's a random %s problem for you.\n\n%s",
		formatDate(p.Date), p.DailyDifficulty, p.Difficulty, formatBody(p.Problem),
	)
}

// formatDate renders a "2006-01-02" date as "January 2, 2006", falling back
// to the raw string when it does not parse.
func formatDate(date string) string {
	if t, err := time.Parse(time.DateOnly, date); err == nil {
		return t.Format("January 2, 2006")
	}
	return date
}

func formatBody(p domain.Problem) string {
	return fmt.Sprintf(
		"🔢 %s. %s\n💪 Difficulty: %s\n🏷 %s\n\n🔗 https://leetcode.com%s",
		p.ID, p.Title, p.Difficulty, strings.Join(p.Tags, ", "), p.Link,
	)
}

// formatRating renders standings, already ordered by domain.Chat.Standings.
func formatRating(standings []domain.Member) string {
	switch len(standings) {
	case 0:
		return msgRatingEmpty
	case 1:
		return fmt.Sprintf("🏆 Solved: <b>%d</b>", standings[0].Count)
	}
	medals := []string{"🥇", "🥈", "🥉"}
	var sb strings.Builder
	sb.WriteString("🏆 <b>Rating</b>\n\n")
	for i, m := range standings {
		if i < len(medals) {
			fmt.Fprintf(&sb, "%s %s — %d\n", medals[i], m.Name, m.Count)
		} else {
			fmt.Fprintf(&sb, "%d. %s — %d\n", i+1, m.Name, m.Count)
		}
	}
	return sb.String()
}

// formatDifficulties renders a subscribed difficulty set; empty means any.
func formatDifficulties(ds []string) string {
	if len(ds) == 0 {
		return "Any"
	}
	return strings.Join(ds, ", ")
}

func validTime(s string) bool { return timeRegexp.MatchString(s) }

// validTimezone reports whether s names an IANA timezone. time.LoadLocation
// also accepts "" (UTC) and "Local" (the server's zone, which cron would take
// as CRON_TZ=Local), which are not zones a user picks.
func validTimezone(s string) bool {
	if s == "" || s == "Local" {
		return false
	}
	_, err := time.LoadLocation(s)
	return err == nil
}

// canonical returns the known difficulties in ds, in canonical order, as a
// new slice.
func canonical(ds []string) []string {
	return slices.DeleteFunc(domain.Difficulties(), func(d string) bool { return !slices.Contains(ds, d) })
}

// initialDifficulties is what a difficulty keyboard starts with: the saved
// set in canonical order, or every difficulty when there is none.
func initialDifficulties(saved []string) []string {
	if len(saved) == 0 {
		return domain.Difficulties()
	}
	return canonical(saved)
}

// toggle adds d to selected or removes it, returning a new canonical slice.
func toggle(selected []string, d string) []string {
	next := slices.DeleteFunc(slices.Clone(selected), func(s string) bool { return s == d })
	if len(next) == len(selected) {
		next = append(next, d)
	}
	return canonical(next)
}
