# LeetCode Daily Notifier

A Telegram bot that sends you the LeetCode daily challenge at a time you choose, and tracks who solves it.

**[@leetcode_notifier_bot](https://t.me/leetcode_notifier_bot)**

## Features

- Daily LeetCode problem delivered at your chosen time and timezone
- Choose your difficulty levels (Easy / Medium / Hard) — if the daily doesn't match, you get a random free problem of your level instead, the same one all day
- `/today` — get today's problem on demand, respecting your difficulty
- `/daily` — get the official LeetCode daily on demand, whatever its difficulty
- **Done button** on each problem — mark it as solved (once per day per user)
- `/rating` — leaderboard across all chat members
- Works in both private chats and group chats

## Commands

| Command | Description |
|---|---|
| `/setup` | Configure your notification time, timezone and difficulty |
| `/difficulty` | Choose which difficulty levels you want |
| `/today` | Get today's problem now (respects your difficulty) |
| `/daily` | Get the official LeetCode daily now (any difficulty) |
| `/rating` | Show the solve leaderboard |
| `/status` | Check your current subscription |
| `/unsubscribe` | Disable notifications |

## Self-hosting

### Requirements

- Go 1.26+
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

> **Note:** Storage is intentionally minimal — a plain JSON file with no backup, migration, or durability guarantees. It is not designed to be a reliable long-term store.

## Development

```bash
make test              # run tests
make test-integration  # run tests plus the hermetic integration suite (in-process fake Telegram and LeetCode)
make lint              # run linter
make generate          # regenerate the gomock mocks (needs mockgen v0.6.0)
make build             # build binary
```
