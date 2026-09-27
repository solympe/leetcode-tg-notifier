package bot

const (
	stateAwaitingTime       = "awaiting_time"
	stateAwaitingTimezone   = "awaiting_timezone"
	stateAwaitingDifficulty = "awaiting_difficulty" // last /setup step
	stateEditingDifficulty  = "editing_difficulty"  // /difficulty

	cbPrefixTime    = "time:"
	cbPrefixTz      = "tz:"
	cbPrefixDiff    = "diff:"
	cbCmdSetup      = "/setup"
	cbCmdToday      = "/today"
	cbCmdDaily      = "/daily"
	cbCmdUnsub      = "/unsubscribe"
	cbCmdStatus     = "/status"
	cbCmdDone       = "done"
	cbCmdRating     = "/rating"
	cbCmdDifficulty = "/difficulty"
	cbCmdDiffSave   = "diffsave" // must not start with cbPrefixDiff

	cmdStart       = "/start"
	cmdAbout       = "/about"
	cmdSetup       = "/setup"
	cmdToday       = "/today"
	cmdDaily       = "/daily"
	cmdStatus      = "/status"
	cmdUnsubscribe = "/unsubscribe"
	cmdRating      = "/rating"
	cmdDifficulty  = "/difficulty"

	parseMode = "HTML"

	msgWelcome           = "Hi! I'm <b>%s</b>. I help you subscribe to a daily LeetCode challenge newsletter and get the problem of the day anytime.\n\nCommands:\n• /setup — create your daily subscription\n• /difficulty — choose problem difficulty\n• /today — get today's problem (your difficulty)\n• /daily — get the official LeetCode daily (any difficulty)\n• /rating — show solve leaderboard\n• /status — check your subscription status\n• /about — learn more about this bot"
	msgAbout             = "For questions, suggestions, and bug reports — DM @solympe"
	msgChooseTime        = "Choose notification time or type your own (<b>HH:MM</b>, 24h):"
	msgChooseTz          = "Time: <b>%s</b>\n\nChoose your timezone or type it manually (e.g. <code>Europe/Moscow</code>)\nhttps://en.wikipedia.org/wiki/List_of_tz_database_time_zones"
	msgChooseDifficulty  = "Choose difficulty (tap to toggle, then Save).\nIf today's daily doesn't match, I'll send a random problem of your level instead."
	msgPickAtLeastOne    = "Pick at least one difficulty"
	msgAllSet            = "✅ All set! I'll send you the daily problem at <b>%s</b> (%s)\nDifficulty: <b>%s</b>"
	msgDifficultyUpdated = "✅ Difficulty updated: <b>%s</b>"
	msgSessionExpired    = "⚠️ Session expired. Please use /setup to start over."
	msgMenuExpired       = "⚠️ This menu is no longer active. Use /setup or /difficulty to start again."
	msgMessageExpired    = "⚠️ This message is too old. Use /today to get a fresh one."
	msgNotSubscribed     = "⚠️ Use /setup to subscribe first."
	msgDisabled          = "🛑 Notifications disabled."
	msgFetchFailed       = "⚠️ Failed to fetch the problem from LeetCode. Try /today later."
	msgInvalidTime       = "⚠️ Invalid format. Please enter time as <b>HH:MM</b> (e.g. <code>09:00</code>):"
	msgInvalidTz         = "⚠️ Unknown timezone. Try again (e.g. <code>Europe/Moscow</code>):"
	msgStatusActive      = "✅ Subscription active\nTime: <b>%s</b> (%s)\nDifficulty: <b>%s</b>"
	msgStatusInactive    = "❌ No active subscription. Use /setup to configure."
	msgRatingEmpty       = "No solves yet. Be the first to press ✅ Done!"
)
