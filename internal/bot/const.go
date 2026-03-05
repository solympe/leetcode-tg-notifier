package bot

const (
	stateAwaitingTime     = "awaiting_time"
	stateAwaitingTimezone = "awaiting_timezone"

	cbPrefixTime = "time:"
	cbPrefixTz   = "tz:"
	cbCmdSetup   = "/setup"
	cbCmdToday   = "/today"
	cbCmdUnsub   = "/unsubscribe"
	cbCmdStatus  = "/status"
	cbCmdDone    = "done"
	cbCmdRating  = "/rating"

	cmdStart       = "/start"
	cmdAbout       = "/about"
	cmdSetup       = "/setup"
	cmdToday       = "/today"
	cmdStatus      = "/status"
	cmdUnsubscribe = "/unsubscribe"
	cmdRating      = "/rating"

	parseMode = "HTML"

	msgWelcome        = "Hi! I'm <b>%s</b>. I help you subscribe to a daily LeetCode challenge newsletter and get the problem of the day anytime.\n\nCommands:\n• /setup — create your daily subscription\n• /today — get today's LeetCode problem now\n• /rating — show solve leaderboard\n• /status — check your subscription status\n• /about — learn more about this bot"
	msgAbout          = "For questions, suggestions, and bug reports — DM @solympe"
	msgChooseTime     = "Choose notification time or type your own (<b>HH:MM</b>, 24h):"
	msgChooseTz       = "Time: <b>%s</b>\n\nChoose your timezone or type it manually (e.g. <code>Europe/Moscow</code>)\nhttps://en.wikipedia.org/wiki/List_of_tz_database_time_zones"
	msgAllSet         = "✅ All set! I'll send you the daily problem at <b>%s</b> (%s)"
	msgSessionExpired = "⚠️ Session expired. Please use /setup to start over."
	msgDisabled       = "🛑 Notifications disabled."
	msgFetchFailed    = "⚠️ Failed to fetch the problem from LeetCode. Try /today later."
	msgInvalidTime    = "⚠️ Invalid format. Please enter time as <b>HH:MM</b> (e.g. <code>09:00</code>):"
	msgInvalidTz      = "⚠️ Unknown timezone. Try again (e.g. <code>Europe/Moscow</code>):"
	msgStatusActive   = "✅ Subscription active\nTime: <b>%s</b> (%s)"
	msgStatusInactive = "❌ No active subscription. Use /setup to configure."
	msgRatingEmpty    = "No solves yet. Be the first to press ✅ Done!"
)
