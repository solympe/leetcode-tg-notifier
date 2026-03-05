# LeetCode Daily Notifier

A Telegram bot that sends you the LeetCode daily challenge at a time you choose, and tracks who solves it.

**[@leetcode_notifier_bot](https://t.me/leetcode_notifier_bot)**

## Features

- Daily LeetCode problem delivered at your chosen time and timezone
- `/today` — get today's problem on demand
- **Done button** on each problem — mark it as solved (once per day per user)
- `/rating` — leaderboard across all chat members
- Works in both private chats and group chats

## Commands

| Command | Description |
|---|---|
| `/setup` | Configure your notification time and timezone |
| `/today` | Get today's LeetCode problem now |
| `/rating` | Show the solve leaderboard |
| `/status` | Check your current subscription |
| `/unsubscribe` | Disable notifications |

## Self-hosting

### Requirements

- Go 1.24+
- A Telegram bot token from [@BotFather](https://t.me/BotFather)

### Run locally

```bash
export BOT_TOKEN=your_token_here
go run main.go
```

### Run with Docker

```bash
docker build -t leetcode-notifier .
docker run -e BOT_TOKEN=your_token_here leetcode-notifier
```

Chat configs are persisted in `config.json` in the working directory.

## Development

```bash
make test   # run tests
make lint   # run linter
make build  # build binary
```
