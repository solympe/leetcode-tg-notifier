package bot

import (
	"fmt"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func startKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📅 Today's problem", cbCmdToday),
			tgbotapi.NewInlineKeyboardButtonData("⚙️ Setup", cbCmdSetup),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("ℹ️ Status", cbCmdStatus),
			tgbotapi.NewInlineKeyboardButtonData("🏆 Rating", cbCmdRating),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛑 Unsubscribe", cbCmdUnsub),
		),
	)
}

func doneKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Done", cbCmdDone),
		),
	)
}

func setupTimeKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("7:00", cbPrefixTime+"07:00"),
			tgbotapi.NewInlineKeyboardButtonData("8:00", cbPrefixTime+"08:00"),
			tgbotapi.NewInlineKeyboardButtonData("9:00", cbPrefixTime+"09:00"),
			tgbotapi.NewInlineKeyboardButtonData("10:00", cbPrefixTime+"10:00"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("18:00", cbPrefixTime+"18:00"),
			tgbotapi.NewInlineKeyboardButtonData("19:00", cbPrefixTime+"19:00"),
			tgbotapi.NewInlineKeyboardButtonData("20:00", cbPrefixTime+"20:00"),
			tgbotapi.NewInlineKeyboardButtonData("21:00", cbPrefixTime+"21:00"),
		),
	)
}

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

func setupTzKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tzLabel("New York", "America/New_York"), cbPrefixTz+"America/New_York"),
			tgbotapi.NewInlineKeyboardButtonData(tzLabel("London", "Europe/London"), cbPrefixTz+"Europe/London"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tzLabel("Lisbon", "Europe/Lisbon"), cbPrefixTz+"Europe/Lisbon"),
			tgbotapi.NewInlineKeyboardButtonData("UTC+0", cbPrefixTz+"UTC"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tzLabel("Moscow", "Europe/Moscow"), cbPrefixTz+"Europe/Moscow"),
			tgbotapi.NewInlineKeyboardButtonData(tzLabel("Dubai", "Asia/Dubai"), cbPrefixTz+"Asia/Dubai"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tzLabel("Bangkok", "Asia/Bangkok"), cbPrefixTz+"Asia/Bangkok"),
		),
	)
}
