# Architecture refactor: compact hexagonal-lite

| | |
|---|---|
| Date | 2026-09-28 |
| Status | Approved design, ready for an implementation plan |
| Base | `origin/main` @ `8879c84` (branch `refactor/architecture`) |
| Module | `github.com/solympe/leetcode-tg-notifier` (Go 1.26.1) |

## 1. Context and goals

The whole bot lives in `internal/bot`: 10 production files (1096 lines), 9 test files (3541 lines) and 273 lines of mocks. That one package routes updates, runs the `/setup` and `/difficulty` dialogs, renders texts and keyboards, sends messages, picks the problem of the day, counts solves and talks to storage and the scheduler. Measured problems:

| Problem | Where |
|---|---|
| The scheduler depends on persistence (`Schedule(chatID, storage.ChatConfig)`) and holds a `SendFunc` back into the bot. | `internal/scheduler/cron.go`, `scheduler.go` |
| A construction cycle is broken by late binding: `bot.New(..., nil)`, then `NewCronScheduler(b.SendDailyProblem)`, then `b.SetScheduler`. | `main.go:37-39` |
| The same problem is modelled twice (`leetcode.Problem` and `storage.DailyPick`), with the converters `newDailyPick` and `pickedProblem`. | `send.go`, `storage.go` |
| Dialog state is spread over 5 parallel maps with 11 accessors (126 lines), plus an unreachable "session expired" branch. | `state.go`, `setup.go` |
| Pick-of-the-day agreement uses a hand-rolled per-chat lock (42 + 61 lines). | `locks.go` |
| Unknown callback data is never answered, so the client shows a spinner. | `router.go` (`handleCallback` switch has no default) |
| A panic in a cron job or an update kills the process. robfig v3's default chain is empty. | `cron.go` (`cron.New()`) |
| The Telegram HTTP client has no timeout (`tgbotapi.NewBotAPI` uses `&http.Client{}`). There is no graceful shutdown. | `main.go` |
| About 3000 test lines assert exact `tgbotapi.MessageConfig` values, so the tests are coupled to the transport. | `internal/bot/*_test.go` |
| `golangci-lint config verify` (2.13.2) rejects `.golangci.yml`: `issues.exclude-rules` is v1 syntax. `golangci-lint run` still reports 0 issues. | `.golangci.yml` |

**Goals**

1. Clear boundaries. There is a stdlib-only domain and a use-case core behind consumer-side ports. Telegram, LeetCode, JSON-file and cron are thin adapters, with one composition root.
2. More compact code: production shrinks by about 17% and hand-written unit tests by about 53% (section 14).
3. A hermetic integration suite that boots the real object graph against in-process fakes. It is written first, against today's code, so it can gate the switch.
4. User-visible behaviour, texts, keyboards and `config.json` stay compatible. The only exceptions are the approved deviations in section 11.
5. `context.Context` is threaded through every I/O port now, so a later DB adapter is a drop-in.

**Non-goals.** No new features or commands, no DB, no webhook mode, no text changes, no per-chat parallelism, no HTML escaping, no fail-fast on a corrupt `config.json`, and no Docker or deployment changes. The last four are follow-ups (section 13).

## 2. Decisions and approaches considered

### 2.1 User decisions (final)

| # | Decision |
|---|---|
| D1 | Adopt the synthesized **compact hexagonal-lite** design: `internal/domain`, `internal/notifier`, `internal/telegram`, `internal/leetcode`, `internal/storage`, `internal/scheduler`, `internal/app` and `main.go`. It comes with a characterization-first integration suite in `internal/app` under `//go:build integration` (in-process fake Telegram and fake LeetCode), and CI gains an Integration job and a mocks-up-to-date step. |
| D2 | Thread `context.Context` now. Every I/O-performing port method takes `ctx` first: `chatStore` (Get/Upsert/Update/Delete/All), `problemSource` (FetchDaily/FetchRandom), `messenger` (all methods), every exported notifier method, and therefore telegram's `service` interface. leetcode uses `http.NewRequestWithContext`. storage returns `ctx.Err()` early. The telegram sender checks `ctx.Err()` before each call, and the in-flight call is bounded by the 75s client timeout. Context sources are fixed in section 6.4. |
| D3 | All deliberate deviations are approved: (a) unknown or forged callbacks get the "menu no longer active" toast; (b) `/rating` ties are ordered by name; (c) panics in cron jobs and update handling are recovered and logged; (d) the Telegram HTTP client gets a 75s timeout; (e) SIGINT/SIGTERM shut down gracefully, without losing or duplicating updates. Also approved: the key-order change inside `daily_pick`, and "Schedule parses before replacing". |
| D4 | Follow-ups ship as separate PRs after the refactor (section 13): fail fast on a corrupt `config.json`; HTML-escape member names and problem titles; per-chat parallel update handling; Docker stop grace ≥ 70s. The owner makes the Integration job a required check through branch protection, manually. |

### 2.2 Approaches considered

| Approach | Core idea | Judges (two) | Outcome |
|---|---|---|---|
| Pragmatic | Split into a stdlib core and a Telegram adapter. Cron gets a job closure per `Schedule`. Compare-and-set in the store replaces the lock. The domain carries the JSON tags. White-box suite in `internal/app`. | 47 + 49 = **96** (both judges' pick) | **Base.** Its verified defect (notifier mocks import notifier, which makes a test import cycle) is fixed by a separate `internal/domain`. |
| Hexagonal | Classic layering, with ctx on every port, private storage DTOs and a characterization suite in `test/integration`. | 47 + 46 = 93 | Grafted: the domain package, pure domain rules, characterization-first, cron timing checked through `Next`, a legacy-struct `stored()`, `cron.Recover`, the 75s timeout and a concurrency row. Its ctx threading is now adopted (D2). |
| Feature-oriented | Capability packages, a dialog state machine returning view-models, and a parallel per-chat transport. | 39 + 40 = 79 | Grafted: the strict "every call consumed" fake invariant, draining buffered updates on shutdown, and the follow-up list. |

## 3. Architecture

### 3.1 Packages

| Package | Responsibility | Imports (internal) |
|---|---|---|
| `main` (`main.go`) | Reads env, builds a signal-aware ctx, calls `app.New` and `Run`. Stays at the module root, so `go run main.go` and the Dockerfile are unchanged. | app |
| `internal/app` | The only composition root and the process lifecycle: wiring, restore, polling loop, graceful shutdown. It also holds the integration suite. | telegram, notifier, storage, leetcode, scheduler |
| `internal/domain` | Persisted model with today's JSON tags, pure rules and sentinel errors. Stdlib only; no I/O, no interfaces, no constructors. | none |
| `internal/notifier` | Use cases: subscribe, difficulties, unsubscribe, restore, solve, pick of the day (compare-and-set) and delivery policy (fetch-failed notice, 403 leads to unsubscribe). | domain |
| `internal/telegram` | The only package that knows tgbotapi apart from app. Routing, dialogs, the (toast, act) callback protocol, rendering, sending, 403 mapping and panic recovery. | domain |
| `internal/leetcode` | GraphQL client: the daily, and a random free algorithms problem. Injectable endpoint. | domain |
| `internal/storage` | JSON-file `chatStore`, file format unchanged. | domain |
| `internal/scheduler` | One daily cron entry per chat at HH:MM in an IANA zone, with `Recover`, plus the `RunNow` and `Next` seams. | none (robfig/cron) |

### 3.2 Dependency graph (compile time; arrow means "imports"; acyclic)

```
main ──► internal/app ──► tgbotapi
            │
            ├──► internal/telegram ──► tgbotapi
            ├──► internal/notifier
            ├──► internal/storage
            ├──► internal/leetcode
            └──► internal/scheduler ──► robfig/cron/v3

   telegram, notifier, storage, leetcode ──► internal/domain (stdlib only)

   mocks:  notifier/mocks ──► domain          (+ context)
           telegram/mocks ──► domain, tgbotapi (+ context)
           leetcode/mocks ──► nothing internal
```

- Only `app` imports more than one internal package. No adapter imports notifier or another adapter.
- No mock imports the package it was generated for, so there is no test import cycle. This was verified with mockgen v0.6.0, including the ctx-threaded `deps.go`.
- Every cross-package call goes through a private interface that the consumer declares: `telegram.botAPI`, `telegram.service`, `notifier.chatStore`, `problemSource`, `dailyScheduler`, `messenger`, `app.jobRunner` and `updateHandler`.

### 3.3 Runtime flow

```
update: Telegram ─► tgbotapi poller ─► app.Run ─► app.handle (per-update ctx) ─► telegram.handler.Handle
                                                                                      │ (service port)
                                                                                      ▼
                                                                               notifier.service
                                                        ┌───────────────┬─────────────┴───┬──────────────────┐
                                                        ▼               ▼                 ▼                  ▼
                                                   chatStore      problemSource     dailyScheduler      messenger
                                                   (storage)      (leetcode)        (scheduler)         (telegram.sender ─► Bot API)

cron:   cron tick / RunNow ─► job closure (ctx from jobs root) ─► service.SendToday ─► the same ports
```

### 3.4 Composition root (no late binding)

Construction order inside `app.New`: store → api (getMe) → jobs root ctx → sched → `svc(jobs, store, lc, sched, sender(api), time.Now)` → `svc.Restore(ctx)` → `handler(api, svc, name)`. `scheduler.New()` takes no dependencies; the service hands a job closure to every `Schedule` call. The stateless `telegram.sender` is built before the service, and `telegram.handler`, which needs the service, is built after it. `SetScheduler`, `SendFunc` and the nil-scheduler construction are deleted.

### 3.5 CLAUDE.md compliance

| Rule | How it is met |
|---|---|
| Interfaces live at the consumer | `deps.go` in notifier and telegram, `randSource` in leetcode (unchanged), `jobRunner` and `updateHandler` in app. No exported interface anywhere. |
| Constructors return `*privateStruct` | `notifier.New` → `*service`, `telegram.NewSender` → `*sender`, `NewHandler` → `*handler`, `leetcode.NewHTTPClient` → `*httpClient`, `storage.NewJSONStorage` → `*jsonStorage`, `scheduler.New` → `*cronScheduler`, `app.New` → `*application`. domain has no constructors. |
| `//go:generate` only in regular files, `-source` mode, one `mocks/` per consumer | `notifier/deps.go`, `telegram/deps.go` and `leetcode/http.go`. |
| gomock only; table-driven tests; mock factories as struct fields | Every unit-test plan in section 4. Every `Test` function is a table, even single-scenario ones such as `TestSendTodayConcurrent` (one row). Expectations are set inside the factories, never in the loop body. |
| `gomock.Eq` for exact arguments | `ctx := t.Context()` is declared once at the top of each `Test` function, before the table, so the factories' `gomock.Eq(ctx)` and the call under test share one value (subtests are not parallel). Numeric IDs are typed in matchers, e.g. `gomock.Eq(int64(100))`: gomock v0.6.0's `Eq` does not convert an untyped `int` to `int64`, so `Eq(100)` never matches. `DoAndReturn` is used only where `Eq` cannot express the check (applying `fn`, capturing a job, inspecting a derived ctx). |
| `errors.As` plus `http.StatusForbidden` | `telegram.isBotBlocked`. |
| Import grouping stdlib, third-party, local | Enforced by goimports `local-prefixes`. |

### 3.6 File layout after the refactor

```
main.go
internal/app/        app.go
                     integration_test.go harness_test.go faketelegram_test.go fakeleetcode_test.go   (//go:build integration)
internal/domain/     model.go rules.go errors.go                    model_test.go rules_test.go
internal/notifier/   deps.go service.go delivery.go mocks/mock_deps.go   service_test.go delivery_test.go
internal/telegram/   deps.go sender.go handler.go dialog.go view.go mocks/mock_deps.go
                                                                    sender_test.go handler_test.go dialog_test.go view_test.go
internal/leetcode/   http.go mocks/mock_deps.go                     http_test.go
internal/storage/    json.go testdata/legacy_config.json            json_test.go
internal/scheduler/  cron.go                                        cron_test.go
```

Deleted: `internal/bot/` (all of it), `leetcode/client.go`, `leetcode/client_test.go`, `leetcode/format_test.go`, `storage/storage.go` and `scheduler/scheduler.go`.

## 4. Per-package design

### 4.1 `main.go` (about 25 lines)

```go
func main() {
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("BOT_TOKEN environment variable is not set") // same text as today
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.New(ctx, app.Config{Token: token, StoragePath: os.Getenv("STORAGE_PATH")})
	if err != nil {
		log.Fatal(err) // app.New wraps as "storage: …" / "NewBotAPI: …", matching today's log lines
	}
	a.Run(ctx)
}
```

### 4.2 `internal/app`

```go
type Config struct {
	Token            string
	StoragePath      string // "" = "config.json"
	TelegramEndpoint string // tgbotapi format ".../bot%s/%s"; "" = tgbotapi.APIEndpoint
	LeetCodeEndpoint string // "" = https://leetcode.com/graphql
}

const (
	telegramTimeout = 75 * time.Second // long poll is 60s
	pollTimeout     = 60               // seconds, as today
	updateTimeout   = 2 * time.Minute  // per-update budget
)

type jobRunner interface { // a.sched; RunNow and Next are used only by the white-box integration suite
	Start()
	Stop()
	RunNow(chatID int64) bool
	Next(chatID int64) (time.Time, bool)
}

type updateHandler interface { // a.bot
	Handle(ctx context.Context, u tgbotapi.Update)
}

type application struct {
	api        *tgbotapi.BotAPI // concrete third-party type: GetUpdatesChan, StopReceivingUpdates, GetUpdates
	sched      jobRunner
	bot        updateHandler
	cancelJobs context.CancelFunc
}

func New(ctx context.Context, cfg Config) (*application, error)
func (a *application) Run(ctx context.Context) // call once per application
```

`New`:

```go
store, err := storage.NewJSONStorage(cmp.Or(cfg.StoragePath, "config.json"))   // err → fmt.Errorf("storage: %w", err)
api, err := tgbotapi.NewBotAPIWithClient(cfg.Token, cmp.Or(cfg.TelegramEndpoint, tgbotapi.APIEndpoint),
	&http.Client{Timeout: telegramTimeout})                                   // calls getMe; err → "NewBotAPI: %w"
log.Printf("Authorized as @%s", api.Self.UserName)
jobs, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))             // jobs root, see 6.4
sched := scheduler.New()
svc := notifier.New(jobs, store, leetcode.NewHTTPClient(cfg.LeetCodeEndpoint, nil), sched, telegram.NewSender(api), time.Now)
if err := svc.Restore(ctx); err != nil {
	log.Print(err) // never fatal, as today
}
return &application{api: api, sched: sched, bot: telegram.NewHandler(api, svc, api.Self.UserName), cancelJobs: cancelJobs}, nil
```

`Run`:

```go
func (a *application) Run(ctx context.Context) {
	defer a.cancelJobs() // 5. backstop: no job context outlives Run
	a.sched.Start()
	defer a.sched.Stop() // 4. no new firings; waits for running jobs
	updates := a.api.GetUpdatesChan(tgbotapi.UpdateConfig{Timeout: pollTimeout})
	stop := context.AfterFunc(ctx, a.api.StopReceivingUpdates) // 1. exactly once (a second call panics)
	defer stop()
	base := context.WithoutCancel(ctx) // started work must survive SIGTERM
	last := 0
	for u := range updates { // 2. drains buffered updates and the last in-flight poll
		a.handle(base, u)
		last = u.UpdateID
	}
	if last > 0 { // 3. confirm the final batch so the next start does not redeliver it
		if _, err := a.api.GetUpdates(tgbotapi.UpdateConfig{Offset: last + 1, Limit: 1}); err != nil {
			log.Printf("confirm updates: %v", err)
		}
	}
}

func (a *application) handle(base context.Context, u tgbotapi.Update) {
	ctx, cancel := context.WithTimeout(base, updateTimeout)
	defer cancel()
	a.bot.Handle(ctx, u)
}
```

Unit tests: none. The package is covered by the integration suite in the same directory.

### 4.3 `internal/domain`

```go
const Easy, Medium, Hard = "Easy", "Medium", "Hard"

func Difficulties() []string // {Easy, Medium, Hard}, freshly allocated (was leetcode.AllDifficulties)

type Problem struct {
	Date       string   `json:"date"` // "2006-01-02"; empty on a random draw until stamped
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Link       string   `json:"link"` // "/problems/<slug>/"
	Difficulty string   `json:"difficulty"`
	Tags       []string `json:"tags"`
}

// Pick is what a chat is sent. DailyDifficulty == "" means Problem is the official daily.
// Otherwise Problem is a random problem replacing a daily of that difficulty; a stored
// pick of the day always has it set.
type Pick struct {
	Problem
	DailyDifficulty string `json:"daily_difficulty"`
}

type Member struct {
	Name           string `json:"name"`
	Count          int    `json:"count"`
	LastSolvedDate string `json:"last_solved_date"` // "2006-01-02", UTC
}

type Chat struct {
	ChatID       int64             `json:"chat_id"`
	NotifyTime   string            `json:"notify_time"`            // "HH:MM"
	Timezone     string            `json:"timezone"`               // IANA
	Members      map[string]Member `json:"members"`                // key: user ID in decimal; nil is written as null
	Difficulties []string          `json:"difficulties,omitempty"` // nil or empty = any
	DailyPick    *Pick             `json:"daily_pick,omitempty"`
}

func (c Chat) Wants(difficulty string) bool       // empty set = any; strings.EqualFold (was wantsDaily)
func (c Chat) PickFor(daily Problem) (Pick, bool) // DailyPick != nil && Date == daily.Date && Wants(DailyPick.Difficulty) (was validPick)
func (c *Chat) RecordSolve(userID int64, name, day string) (total int, counted bool)
func (c Chat) Standings() []Member                // Count desc, then Name asc

var (
	ErrNotSubscribed = errors.New("chat is not subscribed")
	ErrAlreadySolved = errors.New("already solved today")
	ErrBlocked       = errors.New("chat blocked the bot")
)
```

`RecordSolve`: key `strconv.FormatInt(userID, 10)`. On the same `day` it returns `(current count, false)` and does not refresh the name. Otherwise it sets the name, increments the count, sets the date, allocates `Members` if nil, and returns `(new count, true)`. This is today's `handleDone` logic.

Unit tests: pure tables, no mocks, ported from `TestWantsDaily`, the `validPick` rows of `TestSendDailyProblem` and `TestHandleDone`. `Difficulties` (order, fresh slice); `Wants` (nil, empty, subscribed, case-insensitive, not subscribed); `PickFor` (no pick, other date, unsubscribed difficulty, valid); `RecordSolve` (nil `Members`, same day rejected with the total, next day counted, name refreshed); `Standings` (count desc, ties by name). `model_test.go` checks that a legacy flat `daily_pick` decodes into `Pick{Problem, DailyDifficulty}` and round-trips.

### 4.4 `internal/notifier`

`deps.go`:

```go
//go:generate mockgen -source=deps.go -destination=mocks/mock_deps.go -package=mocks

type chatStore interface {
	Get(ctx context.Context, chatID int64) (domain.Chat, bool, error)
	Upsert(ctx context.Context, chatID int64, fn func(*domain.Chat)) error
	Update(ctx context.Context, chatID int64, fn func(*domain.Chat) bool) (found bool, err error)
	Delete(ctx context.Context, chatID int64) error
	All(ctx context.Context) ([]domain.Chat, error)
}

type problemSource interface {
	FetchDaily(ctx context.Context) (domain.Problem, error)
	FetchRandom(ctx context.Context, difficulties []string) (domain.Problem, error)
}

type dailyScheduler interface { // in-memory, so no ctx
	Schedule(chatID int64, notifyTime, timezone string, job func()) error
	Remove(chatID int64)
}

type messenger interface {
	SendProblem(ctx context.Context, chatID int64, p domain.Pick) error // wraps domain.ErrBlocked on 403
	SendFetchFailed(ctx context.Context, chatID int64) error
}
```

`Get` and `All` gain an `error` result. With the JSON store it can only be `ctx.Err()`; a DB adapter needs it anyway.

`service.go`:

```go
const jobTimeout = 2 * time.Minute

type service struct {
	jobs  context.Context // parent of every scheduled job's ctx; owned and cancelled by app
	store chatStore
	lc    problemSource
	sched dailyScheduler
	out   messenger
	now   func() time.Time
}

func New(jobs context.Context, store chatStore, lc problemSource, sched dailyScheduler, out messenger, now func() time.Time) *service
```

| Method | Semantics |
|---|---|
| `Restore(ctx) error` | `All(ctx)` (its error is returned as is). Each chat `c` is scheduled with `sched.Schedule(c.ChatID, c.NotifyTime, c.Timezone, s.job(c.ChatID))`. Per-chat failures come back as `errors.Join` of `fmt.Errorf("restore schedule for %d: %w", c.ChatID, err)`, and the other chats are still scheduled. `All` is sorted, so the order is deterministic. |
| `Subscribe(ctx, chatID, notifyTime, timezone, difficulties) error` | `Upsert` sets `NotifyTime`, `Timezone` and `Difficulties`, and keeps `Members` and `DailyPick`. Then `sched.Schedule(chatID, notifyTime, timezone, s.job(chatID))` runs **even if the save failed**, as today. Returns `errors.Join(saveErr, schedErr)`. |
| `SetDifficulties(ctx, chatID, ds) error` | One `Update`. An `err` is returned as is. `!found` without an error returns `ErrNotSubscribed` and creates nothing. The pick is kept and re-validated on the next send. |
| `Unsubscribe(ctx, chatID) error` | `sched.Remove`, then `store.Delete(ctx)`. |
| `Subscription(ctx, chatID) (domain.Chat, bool, error)` | `store.Get`. |
| `Solve(ctx, chatID, userID, name) (int, error)` | `day := now().UTC().Format(time.DateOnly)`, then one `Update` applying `RecordSolve`. A storage error returns `(total, err)`: total ≥ 1 when the count was applied before a failed write, 0 when the call was refused. Otherwise it returns `ErrNotSubscribed` (not found), `(total, ErrAlreadySolved)`, or `(total, nil)`. |
| `SendToday(ctx, chatID)` | The cron job, `/today` and the 📅 button: `p, err := s.today(ctx, id); s.deliver(ctx, id, p, err)`. |
| `SendDaily(ctx, chatID)` | `/daily` and the 🗓 button: `FetchDaily`, then `deliver(Pick{Problem: daily})`. It never reads or writes the store, so it works for unsubscribed chats. |

`delivery.go` (private):

```go
func (s *service) job(chatID int64) func() {
	return func() {
		ctx, cancel := context.WithTimeout(s.jobs, jobTimeout)
		defer cancel()
		s.SendToday(ctx, chatID)
	}
}

func (s *service) today(ctx context.Context, chatID int64) (domain.Pick, error) // compare-and-set, section 6.2

func (s *service) deliver(ctx context.Context, chatID int64, p domain.Pick, err error) {
	// err != nil (fetch or store read): log, then out.SendFetchFailed(ctx, id), logging its error.
	// SendProblem error with errors.Is(err, domain.ErrBlocked): log "bot blocked by %d: removing subscription",
	//   then s.Unsubscribe(ctx, id), logging its error.
	// Any other send error: log only.
}
```

Unit tests: in `package notifier`, table-driven, with factories `func(*gomock.Controller) *mocks.MockX`. A mock with no expectations is passed as the bare constructor. Tests call the method under test with the test-level `ctx := t.Context()` (3.5) and assert with `gomock.Eq(ctx)` that the same ctx reaches every port. The only exception is calls made from inside a job closure: they get a ctx derived from `jobs`, so those expectations match ctx with `gomock.Any()`, and `TestJob` checks the derived ctx itself. The clock is fixed.
- `TestSendToday` re-expresses the 16 decision rows of today's `TestSendDailyProblem` on the ports, plus a Get-error row:
  - FetchDaily error: SendFetchFailed.
  - Unsubscribed chat, empty set, or subscribed daily (case-insensitive): `SendProblem(Eq(Pick{Problem: daily}))`, no FetchRandom. A subscribed daily beats a same-day pick.
  - Valid pick: resent, with no FetchRandom and no Update.
  - Stale or now-unsubscribed pick: `FetchRandom(Eq(set))`, then Update saves `Pick{Date: daily.Date, DailyDifficulty: daily.Difficulty}`.
  - Lost race (Update's DoAndReturn applies fn to a chat already holding pick A): A is sent and fn returns false.
  - FetchRandom or Get error: SendFetchFailed, nothing saved. Update `found=false` or a save error: the pick is still sent.
  - `fmt.Errorf("x: %w", domain.ErrBlocked)`: Remove and Delete. Any other send error: nothing more.
- `TestSendTodayConcurrent`: 8 goroutines under `-race` share one mutex-guarded Chat in Get and Update, against a slow FetchRandom that returns a new problem per call. Every SendProblem gets the same Pick, equal to the stored one.
- `TestSendDaily`: in the daily and fetch-failure rows the store factory is the bare `mocks.NewMockchatStore`, which proves the pick is never read or written. The blocked row expects `Remove(Eq(int64(100)))` and `Delete(Eq(ctx), Eq(int64(100)))`.
- `TestSubscribe`: new chat; an existing chat keeps `Members` and `DailyPick`; `Schedule(Eq(int64(100)), Eq("09:00"), Eq("UTC"), Any())`. The captured job (DoAndReturn stores it in a variable declared at the test level), run in the loop body, performs a SendToday. A store error still schedules and is returned.
- `TestJob`: the job's ctx has a deadline ≤ `jobTimeout` (DoAndReturn on FetchDaily). A cancelled `jobs` root reaches the ports as `context.Canceled`.
- `TestRestore`: two Schedules; one failure gives a joined error and the other chat is still scheduled; an `All` error is returned.
- `TestSetDifficulties` (found / missing → `ErrNotSubscribed` / store error), `TestUnsubscribe`, `TestSolve` (counted / already / missing / write error returns the total / refused returns 0).

### 4.5 `internal/telegram`

`deps.go`:

```go
//go:generate mockgen -source=deps.go -destination=mocks/mock_deps.go -package=mocks

type botAPI interface { // *tgbotapi.BotAPI satisfies it; tgbotapi v5.5.1 has no ctx API
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}

type service interface { // satisfied by notifier's *service; domain types only
	Subscribe(ctx context.Context, chatID int64, notifyTime, timezone string, difficulties []string) error
	SetDifficulties(ctx context.Context, chatID int64, difficulties []string) error
	Unsubscribe(ctx context.Context, chatID int64) error
	Subscription(ctx context.Context, chatID int64) (domain.Chat, bool, error)
	Solve(ctx context.Context, chatID, userID int64, name string) (int, error)
	SendToday(ctx context.Context, chatID int64)
	SendDaily(ctx context.Context, chatID int64)
}
```

`sender.go`:

```go
func NewSender(api botAPI) *sender
func (s *sender) SendProblem(ctx context.Context, chatID int64, p domain.Pick) error // formatPick + Done keyboard, HTML
func (s *sender) SendFetchFailed(ctx context.Context, chatID int64) error
```

The private helpers are all ctx-first: `send(ctx, chatID, text, kb) (msgID int, err error)`, `text(ctx, chatID, text)`, `edit(ctx, chatID, msgID, text, kb *tgbotapi.InlineKeyboardMarkup)` (nil removes the keyboard), `editKeyboard(ctx, chatID, msgID, kb)`, `answer(ctx, cbID, text)` and `isBotBlocked(err) bool`. Before each `api.Send` or `api.Request`, the sender returns `ctx.Err()` if the ctx is already done. tgbotapi v5.5.1 builds its requests with `http.NewRequest` and has no ctx parameter, so a call already in flight cannot be interrupted. It is bounded by the 75s `http.Client` timeout set in `app.New`. This is documented on `sender`.

`handler.go`:

```go
func NewHandler(api botAPI, svc service, botName string) *handler // builds its own sender and the commands table (a field, which avoids an init cycle)
func (h *handler) Handle(ctx context.Context, u tgbotapi.Update)   // defer recover + log with debug.Stack()
```

`dialog.go` and `view.go` are specified in section 7. Nothing in telegram creates a ctx: it uses the one `Handle` receives, and the `act` closures capture it.

Unit tests (about 460 lines, with `MockbotAPI` and `Mockservice`; full flows live in the integration suite):
- `TestHandleRouting` (update in, then the expected svc call or sent config):
  - `/today`, `/today@TestBot` and `/today@Other` call `SendToday(Eq(ctx), Eq(int64(100)))`; `"/today extra"` and plain text outside a dialog do nothing.
  - Each of the 7 menu callbacks gets `Request(Eq(NewCallback(id, "")))`, then its action, and the order is asserted. When the action is also a `MockbotAPI` call (the `/status` send), `gomock.InOrder` inside the api factory does this. When it is a `Mockservice` call, the api factory stores the answer's `*gomock.Call` in a test-level variable, and the svc factory chains `.After(answered)`. The loop body builds the api mock before the svc mock.
  - A nil message gets `msgMessageExpired`; `bogus`, `/start` and `done:x` get `msgMenuExpired`.
  - `/status` (subscribed / unsubscribed / `Any`), `/unsubscribe`, `/start`; a Subscription error sends nothing; a panicking svc is recovered.
- `TestDone`: the toast per Solve result (a write error with total 1 gives Counted, a refused call gives `""`); the `@username` fallback via `Eq` on Solve's args.
- `TestDialog` (edge branches):
  - Stale inputs: a time button on another message or step, forged `time:25:00`, `tz:Local`.
  - Difficulty buttons: `diff:Insane` gets `""` and no edit; an empty Save gets `msgPickAtLeastOne`, no svc call, and the session is kept.
  - Saves: setup Save calls `Subscribe` with `Eq` args, and a Subscribe error still shows All set. `/difficulty` Save with `ErrNotSubscribed` edits to `msgNotSubscribed`.
  - Sessions: a failed `/setup` send leaves no session; `/difficulty` in an unsubscribed chat keeps the existing session.
- `TestSender`: the exact `MessageConfig`. A 403 satisfies `errors.Is(err, domain.ErrBlocked)` and still `errors.As` to `*tgbotapi.Error`; a 500 passes through unwrapped. A cancelled ctx returns `context.Canceled` with a bare `MockbotAPI` (no calls).
- Pure tests:
  - `formatPick` (daily, random, unparseable date; copied from `leetcode/format_test.go`), `formatRating` (1 and many members, 4th+ numbering) and `formatDifficulties`.
  - Keyboards (rows, callback data), `tzLabel` with a half-hour zone.
  - `validTime`, `validTimezone` (rejects `""`, `Local`, `Mars/Base`), `toggle` and canonical order.

### 4.6 `internal/leetcode`

```go
//go:generate mockgen -source=http.go -destination=mocks/mock_deps.go -package=mocks   (unchanged; randSource)

func NewHTTPClient(endpoint string, c *http.Client) *httpClient // "" = https://leetcode.com/graphql; nil c = &http.Client{Timeout: 10 * time.Second}
func (hc *httpClient) FetchDaily(ctx context.Context) (domain.Problem, error)
func (hc *httpClient) FetchRandom(ctx context.Context, difficulties []string) (domain.Problem, error) // result has no Date
```

`query(ctx, payload, out)` uses `http.NewRequestWithContext(ctx, http.MethodPost, hc.endpoint, bytes.NewReader(payload))`; the headers and the error wrapping (`"http: %w"`) are unchanged, so `errors.Is(err, context.Canceled)` holds. The 10s per-request client timeout stays, so the effective bound is whichever comes first. `FetchRandom` keeps today's algorithm: validate against `domain.Difficulties()`, choose the level uniformly (all three when empty), then rejection-sample with `paidOnly` filtered client-side, up to `maxRandomAttempts = 8`. Values replace `*Problem`. `client.go` is deleted, and `FormatProblem`, `FormatRandomProblem`, `formatDate` and `formatBody` move to `telegram/view.go`.

Unit tests: the existing `http_test.go` is adapted to `NewHTTPClient(srv.URL, srv.Client())` and value returns.
- FetchDaily: ok, non-200, empty title, bad JSON, headers.
- FetchRandom: level via `MockrandSource`, paid draw rejected, empty draw, attempts exhausted, total 0, unknown difficulty makes no HTTP call, and the request variables.
- `TestNewHTTPClient`: defaults, and an explicit endpoint.
- New `TestContext`: a cancelled ctx gives `context.Canceled` with 0 server hits; a 50ms deadline against a handler that blocks until the request ctx is done fails within the deadline.
- `TestAllDifficulties` moves to domain.

### 4.7 `internal/storage`

```go
func NewJSONStorage(path string) (*jsonStorage, error)
func (s *jsonStorage) Get(ctx context.Context, chatID int64) (domain.Chat, bool, error)
func (s *jsonStorage) Upsert(ctx context.Context, chatID int64, fn func(*domain.Chat)) error                // creates Chat{ChatID} when missing
func (s *jsonStorage) Update(ctx context.Context, chatID int64, fn func(*domain.Chat) bool) (bool, error)  // missing: fn not called, nothing created; fn false: no write
func (s *jsonStorage) Delete(ctx context.Context, chatID int64) error
func (s *jsonStorage) All(ctx context.Context) ([]domain.Chat, error)                                      // sorted by ChatID
```

- Every method first returns `ctx.Err()` if the ctx is done, before taking the lock. Once a method has started, its read-modify-write and `os.WriteFile` run to completion, because file I/O is not cancellable.
- `Upsert` and `Update` share `mutate(chatID, create bool, fn func(*domain.Chat) bool)`: clone in, run fn under the lock, force `ChatID` to the key, store a clone, then save. The memory-then-disk order is unchanged.
- File format unchanged: `{"chats":{"<id>":Chat}}` through the private `jsonFile`, key `strconv.FormatInt`, `json.MarshalIndent(…, "", "  ")`, `os.WriteFile(path, data, 0644)`. Deliberately **not** temp-file-plus-rename, which fails with EBUSY on a bind-mounted `config.json`.
- A missing file means empty. A corrupt file is logged and the store starts fresh, as today (follow-up 1 changes this).
- Deep clones in and out: `maps.Clone(Members)`, `slices.Clone(Difficulties)`, and a copy of `DailyPick` with cloned `Tags`.

Unit tests (real files in `t.TempDir()`, no mocks):
- Reload persistence, and a round trip of every field (nil stays nil).
- No shared memory through Get, All or fn.
- Update: missing, fn false, fn true, and 50 concurrent updates all kept (`-race`).
- Upsert creates, and keeps `Members` and `DailyPick`.
- New `TestCancelledContext`: each of the 5 methods returns `context.Canceled` and leaves both file bytes and memory unchanged.
- `TestLoadLegacyConfig` keeps its rows and adds the golden fixture (5.2).

### 4.8 `internal/scheduler`

The job type **stays `func()`**, because that is the simpler choice. The closure built by notifier creates its own ctx, so the scheduler stays context-free and maps directly onto `cron.FuncJob`.

```go
func New() *cronScheduler // cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger))), not started
func (s *cronScheduler) Schedule(chatID int64, notifyTime, timezone string, job func()) error
func (s *cronScheduler) Remove(chatID int64)
func (s *cronScheduler) RunNow(chatID int64) bool            // test seam
func (s *cronScheduler) Next(chatID int64) (time.Time, bool) // test seam
func (s *cronScheduler) Start()
func (s *cronScheduler) Stop() // waits for running jobs (below)
```

- `Schedule`: `hh, mm, ok := strings.Cut(notifyTime, ":")`. When `!ok` it returns `fmt.Errorf("invalid time %q", notifyTime)`, as today. Then `cron.ParseStandard("CRON_TZ=<tz> <mm> <hh> * * *")` runs **first**, and a parse error is returned with the old entry kept. Only on success does it replace the chat's entry under `s.mu`: `c.Remove(old)` if there is one, then `c.Schedule(spec, cron.FuncJob(job))`, which returns the `EntryID` and cannot fail. It logs the same line as today: `Scheduled chat %d at %s %s (entry %d)`.
- `RunNow`: looks up the EntryID under `s.mu` and **releases it**, then `e := c.Entry(id); if e.Valid() { e.WrappedJob.Run() }`. This runs exactly what cron would run, Recover included, synchronously. It returns false when nothing is scheduled. Releasing the lock first means a job that unsubscribes (403, then Remove) cannot deadlock. It also works after `Stop`, because a stopped cron still returns its entries.
- `Next`: looks up the EntryID under `s.mu`, then `e := c.Entry(id)`. It returns `(time.Time{}, false)` when the chat is unknown or `!e.Valid()`, and otherwise `(e.Schedule.Next(time.Now()), true)`. It works before `Start`. The result is in `time.Local`, so callers compare `.In(loc)`.
- `Stop`: `<-s.c.Stop().Done()`, **without** holding `s.mu`. A job that is still running may call `Remove`, which takes `s.mu`. `Stop` waits only for firings that cron itself started; `RunNow` runs in the caller's goroutine.
- State as today: `c *cron.Cron`, `mu sync.Mutex`, `entries map[int64]cron.EntryID`. `SendFunc` and the storage import are deleted.

Unit tests (`cron_test.go`, real cron, recording closures, no mocks):
- `Next` lands on HH:MM in the zone within 24h (09:30 Europe/Moscow, 07:00 Asia/Dubai, UTC), before and after Start.
- `"0900"`, `"9am"` and `"Mars/Base"` are errors that keep the previous entry.
- Rescheduling replaces the entry (1 entry; RunNow runs the new func).
- RunNow and Next are false after Remove and for an unknown chat.
- Robustness: a self-Removing job inside RunNow does not deadlock (`-race`); a panicking job is recovered; Stop waits for a running job.

## 5. Domain model and `config.json` compatibility

### 5.1 Mapping

| Today | New | JSON |
|---|---|---|
| `storage.ChatConfig` | `domain.Chat` | `chat_id`, `notify_time`, `timezone`, `members` (no omitempty), `difficulties,omitempty`, `daily_pick,omitempty` |
| `storage.UserStat` | `domain.Member` | `name`, `count`, `last_solved_date` |
| `leetcode.Problem` (untagged) | `domain.Problem` | `date`, `id`, `title`, `link`, `difficulty`, `tags` |
| `storage.DailyPick` (7 flat fields) | `domain.Pick{Problem; DailyDifficulty}` | the embedded fields are flattened by `encoding/json`, plus `daily_difficulty` |

The JSON tags are a documented contract: renaming one requires a migration. A DB adapter later implements the same five `chatStore` methods, with Update and Upsert as a transaction, and ignores the tags.

### 5.2 Compatibility checks (verified in a scratch probe; pinned by tests)

- Today's flat `daily_pick {date, daily_difficulty, id, title, link, difficulty, tags}` decodes fully into `Pick`.
- `members: null` and a missing `difficulties` field decode to nil (no members; Any).
- **Rollback safety.** A file written by the new code, decoded with a verbatim copy of today's `ChatConfig`, is `DeepEqual` to what the old binary reads.
- **Key order.** The only byte difference is inside `daily_pick`. Today it is `date, daily_difficulty, id, title, link, difficulty, tags`; after the change it is `date, id, title, link, difficulty, tags, daily_difficulty`. No reader depends on key order.
- **Golden fixture.** `internal/storage/testdata/legacy_config.json` is produced by the **current** `jsonStorage` in migration step 3. A throwaway program outside the repo calls today's `NewJSONStorage` + `Set` for the three chats; its output bytes are committed and never regenerated. It holds a chat with members and a flat Easy `daily_pick`, a legacy chat with `members: null` and no `difficulties`, and a negative group ID. In step 3 the old `json_test.go` gains a `TestLoadLegacyConfig` row that loads it. In step 7 the new code must load it into the expected `domain.Chat`. After a new save, a test-local copy of today's `ChatConfig`, `UserStat` and `DailyPick` must decode it into exactly what the old binary would read.

## 6. Concurrency model

### 6.1 Store atomicity (unchanged strength)

One mutex guards the in-memory map and the file write. Solve, SetDifficulties, Subscribe and the pick save are each a single `Update` or `Upsert`, so a Done press, a difficulty change and a pick save that happen together never overwrite each other. `Update` never creates a chat, so a chat removed by `/unsubscribe` or a 403 is never brought back.

### 6.2 Pick of the day: compare-and-set replaces `locks.go`

```go
daily, err := s.lc.FetchDaily(ctx)            // err → return (fetch-failed)
c, ok, err := s.store.Get(ctx, chatID)        // err → return (fetch-failed)
if !ok || c.Wants(daily.Difficulty) { return domain.Pick{Problem: daily}, nil }
if p, ok := c.PickFor(daily); ok { return p, nil }
r, err := s.lc.FetchRandom(ctx, c.Difficulties) // err → return (fetch-failed, nothing saved)
r.Date = daily.Date
pick := domain.Pick{Problem: r, DailyDifficulty: daily.Difficulty}
if _, err := s.store.Update(ctx, chatID, func(c *domain.Chat) bool {
	if won, ok := c.PickFor(daily); ok { pick = won; return false } // someone committed first: adopt theirs
	saved := pick
	c.DailyPick = &saved
	return true
}); err != nil {
	log.Printf("save pick for %d: %v", chatID, err) // the pick is still sent
}
return pick, nil
```

- The first committed pick wins, and every concurrent loser sends it, so all sends for a chat agree.
- `PickFor` is re-evaluated under the store lock against the current difficulties, which is today's rule.
- A chat deleted mid-fetch is not re-created, and the fetched problem is still sent, as today.
- The guarantee lives in the store's atomic update, so it also holds for a transactional DB and several replicas.
- The cost: under contention the loser makes one redundant `FetchRandom`. At most two senders exist per chat (the sequential update loop and the chat's single cron entry), so there is at most one extra LeetCode call.

### 6.3 Cron versus the update loop

Cron runs each firing in its own goroutine, wrapped in `Recover`. `app.Run` handles updates one at a time, with `recover` in `Handle`. The service holds no mutable state apart from the store. The scheduler guards `entries` with its own mutex, and `RunNow` releases it before running a job, so "blocked, then Unsubscribe, then Remove" cannot deadlock (verified with `-race`); robfig's `Remove` while running talks to its run loop over a channel and never takes our mutex. The dialog `sessions` map has a mutex (about 5 lines), so it stays safe if routing is ever parallelised.

### 6.4 Where contexts come from

| Work | ctx | Created in | Deadline | Cancelled when |
|---|---|---|---|---|
| Restore at startup | the ctx passed to `app.New`, which is main's `signal.NotifyContext` ctx | `main` | none | SIGINT/SIGTERM during startup |
| Each update | `context.WithTimeout(context.WithoutCancel(runCtx), updateTimeout)` | `app.handle` | 2 min | the handler returns, or the deadline passes |
| Callback `act` closures | the update's ctx, captured | `telegram` | as the update | as the update |
| Jobs root | `context.WithCancel(context.WithoutCancel(newCtx))` | `app.New`; stored in `notifier.service.jobs` | none | `Run` returns, after `sched.Stop()` has waited (backstop) |
| Each cron job (and `RunNow`) | `context.WithTimeout(s.jobs, jobTimeout)` | notifier `job` closure | 2 min | the job returns, the deadline passes, or the root is cancelled |
| Confirming `getUpdates`, `getMe` | none (tgbotapi has no ctx API) | – | 75s client timeout | – |

**Why `WithoutCancel` for updates.** Telegram has already handed out the updates drained after SIGTERM. Buffered ones are confirmed by the poller's next offset, and the final batch by step 3 of 6.5. If their ctx were derived with cancellation from `runCtx`, every port would refuse, and those updates would be silently lost, which contradicts D3(e). The per-update ctx is still derived from `runCtx` (it inherits its values), and the 2-minute timeout bounds it. For the worst case of 1 daily plus 9 list requests at 10s each, plus a 75s send, the budget is only reached under a combined LeetCode and Telegram outage.

**Why the jobs root is cancelled only after `sched.Stop()`.** The same rule applies to jobs. A daily send already running when SIGTERM arrives is finished, not aborted; aborting it would lose that chat's problem until the next day. `sched.Stop()` stops new firings and waits, and cancelling the root afterwards is only a backstop: no job ctx outlives `Run` (row 19). This is what "cancelled on shutdown" means in D2.

**A signal during startup.** `signal.NotifyContext` is installed before `app.New`, so SIGINT/SIGTERM no longer kill the process at once while `New` runs. A signal that arrives while `New` is blocked in `getMe` (no ctx API) takes effect when `getMe` returns, within the 75s client timeout. `Restore`'s `All` then refuses, the error is logged, and `Run` goes straight into the shutdown sequence of 6.5.

**Port behaviour when a ctx is done** (details in 4.5–4.7). storage returns `ctx.Err()` before locking, and a started write completes. leetcode aborts the in-flight request. The telegram sender refuses to start a call, and a call already in flight is bounded by 75s. The scheduler takes no ctx.

### 6.5 Graceful shutdown (D3 e)

1. SIGINT/SIGTERM cancels `runCtx`, and `context.AfterFunc` calls `StopReceivingUpdates` exactly once.
2. The range loop keeps handling updates until the poller closes the channel after its in-flight poll (up to 60s in production). No buffered update is dropped, and each gets a live ctx (6.4).
3. One `GetUpdates(Offset: last+1, Limit: 1)` confirms the final batch. Without it, the probe showed each such update handled twice after a restart.
4. `sched.Stop()` stops new firings and waits for running jobs (each bounded by `jobTimeout`). Started work is allowed to finish; it is not cancelled.
5. `cancelJobs()` cancels the jobs root as a backstop, so a job ctx can never outlive `Run` (row 19).

Verified in a scratch probe over 5 restarts with an update injected during shutdown: every update was handled exactly once and the queue was fully confirmed. Every state change is persisted synchronously before its reply, so a SIGKILL loses nothing that was persisted. Unconfirmed updates are redelivered.

## 7. Telegram adapter

### 7.1 Routing

| Input | Route | Result |
|---|---|---|
| `Message`, text after `strings.TrimSpace` starts with `/` | `name, _, _ := strings.Cut(text, "@")`; `commands[name]` | the action; unknown names are ignored. Equivalent to today's `isCommand`: `/today@Other` matches, `"/today extra"` does not. |
| `Message` without `/`, session at `stepTime` or `stepTimezone` | `dialog.onText` | see 7.2 |
| any other `Message`, or an update with neither message nor callback | ignored | – |
| `CallbackQuery` with `Message == nil` | – | `answer(msgMessageExpired)` |
| callback data equal to a command marked `inMenu` | `("", act)` | answer `""`, then the action |
| `done` | `svc.Solve`, then the toast; no act | the answer comes after the count, as today |
| `time:<t>`, `tz:<zone>`, `diff:<d>`, `diffsave` | dialog | 7.2 |
| anything else (e.g. `bogus`, `/start`, `done:x`) | – | `answer(msgMenuExpired)`: **deviation (a)**, today unanswered |

Commands table (a field of `handler`: `map[string]command{run func(ctx, chatID int64); inMenu bool}`):

| Command | Action | Also a menu callback |
|---|---|---|
| `/start` | `msgWelcome` with botName, plus the start keyboard | no |
| `/about` | `msgAbout` | no |
| `/setup` | `startSetup` | yes |
| `/difficulty` | `startDifficulty` | yes |
| `/today` | `svc.SendToday` | yes |
| `/daily` | `svc.SendDaily` | yes |
| `/status` | `msgStatusInactive`, or `msgStatusActive` (difficulties shown as `Any` when the set is empty) | yes |
| `/rating` | `msgRatingEmpty` when not subscribed or no members; otherwise `formatRating(chat.Standings())` | yes |
| `/unsubscribe` | `svc.Unsubscribe` (error logged), then `msgDisabled` | yes |

A `Subscription` error in `/status`, `/rating`, `/difficulty` or the timezone step is logged, nothing is sent or edited, and the dialog session is left unchanged. In the timezone step the session stays at `stepTimezone` on the same message. For a `tz:` button, `Subscription` runs inside `act`, after the `""` answer, because `button` has no side effects. With the JSON store this only happens when the ctx is done, and then the sender would refuse anyway.

Done toasts:

| Solve result | Toast |
|---|---|
| `ErrNotSubscribed` | `msgNotSubscribed` |
| `ErrAlreadySolved` | `Already counted today!` |
| nil, or another error with total > 0 (logged) | `✅ Counted! Your total: N` |
| another error with total 0 (logged) | `""` |

The name is `From.FirstName`, falling back to `"@" + From.UserName`.

### 7.2 Dialog and session model (`dialog.go`)

```go
type step int // stepTime = iota + 1, stepTimezone, stepSetupDifficulty, stepEditDifficulty

type session struct {
	step                 step
	msgID                int // the one message whose buttons are live
	notifyTime, timezone string
	selected             []string // canonical order
}

type sessions struct{ mu sync.Mutex; m map[int64]session }
// get(chatID) session (a copy with selected cloned; the zero step means none), set(chatID, s) (stores a clone), drop(chatID)
// (s session) onDifficulty(msgID int) bool: (stepSetupDifficulty || stepEditDifficulty) && s.msgID == msgID
```

In the table below, `msgX(args)` means `fmt.Sprintf(msgX, args)`, and `msgID` is the session's `msgID` (for a button, it must equal the pressed message's ID). This replaces `stateStore` (5 maps, `pendingSetup`, 11 accessors, 126 lines) with about 30 lines. A timezone step without a time can no longer be represented, so `msgSessionExpired` and its unreachable checks are deleted. Sessions stay in memory and are lost on restart, as today.

| Trigger | Condition | Effect |
|---|---|---|
| `/setup` (command or button) | always | `drop`; send `msgChooseTime` with the time keyboard; on success `set{stepTime, msgID}`. A failed send leaves no session. |
| text at `stepTime` | `!validTime` | edit `msgID` to `msgInvalidTime` with the time keyboard |
| text at `stepTime` | valid | set `stepTimezone` and `notifyTime`; edit to `msgChooseTz(t)` with the tz keyboard |
| `time:<t>` | `step == stepTime && msgID match && validTime(t)` | `("", act)`: same as valid text; otherwise `(msgMenuExpired, nil)` |
| text at `stepTimezone` | `!validTimezone` | edit to `msgInvalidTz` with the tz keyboard |
| `tz:<zone>` | `step == stepTimezone && msgID match && validTimezone(zone)` | `("", act)`; otherwise `(msgMenuExpired, nil)` |
| valid timezone (text or button) | – | `selected = canonical(initialDifficulties(Subscription.Difficulties))`, which is the saved set or all three; set `stepSetupDifficulty`; edit to `msgChooseDifficulty` with the difficulty keyboard |
| `/difficulty` | unsubscribed | send `msgNotSubscribed`; any existing session is left alone |
| `/difficulty` | subscribed | `drop`; send the difficulty keyboard; on success `set{stepEditDifficulty, msgID, selected}` |
| `diff:<d>` | `!onDifficulty(msgID)` | `(msgMenuExpired, nil)` |
| `diff:<d>` | `d` not in `domain.Difficulties()` | `("", nil)`, no change |
| `diff:<d>` | otherwise | `("", act)`: toggle, set, `editMessageReplyMarkup` |
| `diffsave` | `!onDifficulty(msgID)` | `(msgMenuExpired, nil)` |
| `diffsave` | empty selection | `(msgPickAtLeastOne, nil)`; the session is kept |
| `diffsave` at `stepSetupDifficulty` | – | `("", act)`: `drop`; `svc.Subscribe` (error logged); edit to `msgAllSet(time, tz, formatDifficulties(selected))` with no keyboard |
| `diffsave` at `stepEditDifficulty` | – | `("", act)`: `drop`; `svc.SetDifficulties`; `ErrNotSubscribed` edits to `msgNotSubscribed`, anything else (errors logged) edits to `msgDifficultyUpdated(formatDifficulties(selected))`, with no keyboard |

`validTime` keeps today's regexp `^([01]\d|2[0-3]):([0-5]\d)$`. `validTimezone` rejects `""` and `"Local"` (cron would accept `CRON_TZ=Local`), then calls `time.LoadLocation`.

### 7.3 Callback protocol: (toast, act)

Each button function **validates only** and returns `(toast string, act func())`. `onCallback` then runs:

```go
toast, act := h.button(ctx, chatID, msgID, cb.From, cb.Data)
h.answer(ctx, cb.ID, toast)
if act != nil {
	act()
}
```

There is one answer point, so every callback is answered exactly once by construction. The answer still comes before the edit or send, exactly as today. The single exception is `done`, which calls `Solve` inside `button`, so its toast can report the count (today's order). A panic before the answer leaves that callback unanswered; the handler recovers it.

### 7.4 Rendering (`view.go`): byte-identical

- **Constants.** Every `msg*` constant from `const.go` is copied byte for byte, except `msgSessionExpired`. The inline strings from `commands.go` are copied too: `🏆 Solved: <b>%d</b>`, `🏆 <b>Rating</b>\n\n`, `%s %s — %d\n` for the 🥇🥈🥉 lines, `%d. %s — %d\n` from 4th place, `Already counted today!` and `✅ Counted! Your total: %d`.
- **Keyboards.** start (3/2/2), done, time (8 buttons), tz (7 buttons in rows 2/2/2/1: six labelled by `tzLabel`, plus the fixed `UTC+0`) and difficulty (toggles and `💾 Save`) keep the same labels and callback data. So buttons on messages sent before the deploy keep working: `time:`, `tz:`, `diff:`, `/setup`, `/today`, `/daily`, `/unsubscribe`, `/status`, `done`, `/rating`, `/difficulty` and `diffsave`.
- **`formatPick(p)`.** When `DailyDifficulty == ""` it uses `"📅 LeetCode Daily — %s\n\n%s"`. Otherwise it uses `"🎲 LeetCode Random — %s\nToday's daily is %s, so here's a random %s problem for you.\n\n%s"`. The body is `"🔢 %s. %s\n💪 Difficulty: %s\n🏷 %s\n\n🔗 https://leetcode.com%s"`, and the date is `"January 2, 2006"`, falling back to the raw string. This is identical to today's `FormatProblem` and `FormatRandomProblem`, with the old test cases copied as the reference.
- Every `sendMessage` and `editMessageText` uses `parse_mode=HTML`, and `editMessageText` without a keyboard removes it, as today.

### 7.5 Error mapping and panic recovery

- `isBotBlocked(err)`: `var e *tgbotapi.Error; errors.As(err, &e) && e.Code == http.StatusForbidden`.
- Only `SendProblem` maps the error, as today, returning `fmt.Errorf("%w: %w", domain.ErrBlocked, err)`. Other sends log their errors.
- `Handle` has `defer func() { if r := recover(); r != nil { log.Printf("panic in update %d: %v\n%s", u.UpdateID, r, debug.Stack()) } }()`.
- Cron jobs are recovered by `cron.Recover(cron.DefaultLogger)`, and `RunNow` goes through the same `WrappedJob`.

## 8. Integration suite

### 8.1 Build tag and layout

Every file starts with `//go:build integration` and declares `package app`. The suite is white-box, so it reaches `a.sched.RunNow` and `a.sched.Next` without exported test hooks. The unit job never compiles it.

| File | Content | Size |
|---|---|---|
| `integration_test.go` | `TestMain` (quiet-minute guard), plus `TestIntegration`: a table of scenarios, each `t.Run` + `t.Parallel` on a fresh env | ~525 |
| `harness_test.go` | `env` and its helpers, plus the cleanup invariants | ~220 |
| `faketelegram_test.go` | fake Bot API | ~220 |
| `fakeleetcode_test.go` | fake LeetCode GraphQL | ~110 |

### 8.2 Harness

| Helper | Behaviour |
|---|---|
| `start()` | `e.ctx, e.cancel = context.WithCancel(context.Background())`, then `a, err := New(e.ctx, Config{Token: "TEST:TOKEN", StoragePath: filepath.Join(dir, "config.json"), TelegramEndpoint: tg.srv.URL + "/bot%s/%s", LeetCodeEndpoint: lc.srv.URL + "/graphql"})`, then `go a.Run(e.ctx)`. This is exactly what main calls, with a fresh ctx per started app. Restore runs inside `New`, so `fire` and `scheduledAt` are valid once start returns. |
| `stop()` | `e.cancel()`, then waits for `Run` to return (bounded by the fake's 100ms poll cap). It is guarded by `sync.Once` per started app and registered with `t.Cleanup` **after** the servers' `Close`, so the LIFO order stops the app first and tgbotapi's 3s retry path is never hit. `e.a` keeps pointing at the stopped app until the next `start()`. |
| `restart()` | `sync()`, `stop()`, then `start()` on the same dir and fakes: a new BotAPI (getMe), the store reloaded from disk, a new cron, and Restore. |
| `seed(json)` | Writes `config.json` in today's exact format before start. |
| `stored(chatID) (legacyChat, bool)` | Decodes `config.json` from disk into a **test-local** copy of today's `ChatConfig`, `UserStat` and `DailyPick` tags. |
| `fire(chat) bool` | `a.sched.RunNow(chat)`: the real entry, with Recover, run synchronously. false means nothing is scheduled. |
| `scheduledAt(chat, "09:00", "Europe/Moscow")` | `a.sched.Next(chat).In(loc)` has that HH:MM and is within 24h. |
| `notScheduled(chat)` | `Next` returns false. |
| `say(chat, user, text)` | Enqueues a message update (chat type private for positive IDs, supergroup for negative ones) and wakes the pollers. |
| `press(chat, user, data) cbID` | Presses on the **newest** bot message whose current keyboard has that `callback_data`. Fails if there is none. |
| `pressOn(chat, user, msgID, data)` | For stale and forged buttons. |
| `pressInline(user, data)` | A callback with `Message == nil`. |
| `block(chat)` | Later `sendMessage` calls to the chat return 403. |
| `expectMessage(chat)` | The next unconsumed send or edit for the chat, in order. Waits up to 5s on a broadcast channel, and on timeout fails with a transcript dump. Wrapped by `sent(chat, text, kb)`, `edited(chat, msgID, text, kb)`, `rekeyed(chat, msgID, kb)` and `blockedAttempt(chat)`. |
| `expectAnswer(cbID) string` | Keyed by cbID, separate from the message stream. |
| `sync()` | Barrier: `/about` from reserved probe chat 1, then consumes the reply. The loop is sequential, so every earlier update has been handled. |
| `expectQuiet(chat)` | `sync()`, then asserts that nothing new reached the chat. |

Users: `alice = {ID: 7, FirstName: "Alice"}` and `bob = {ID: 8, UserName: "bob"}` (the `@bob` fallback). Expected texts are **literal copies** of today's strings, not references to telegram constants. The tz keyboard is compared by callback data only, because its labels change with DST. In the 8.6 table, "…" only abbreviates the rest of a text that follows from the 7.4 templates and the fakes' data in 8.4. The tests spell every expected text out in full and compare it exactly.

### 8.3 Fake Telegram (`httptest.Server`, `POST /bot{token}/{method}`, form-encoded)

| Method | Semantics |
|---|---|
| wrong token | 401 `{"ok":false,"error_code":401}` |
| `getMe` | `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test","username":"TestBot"}}` |
| `getUpdates` | At-least-once. Deletes queued updates with `update_id < offset` (a missing offset means 0), then returns up to `limit` (default 100) of the rest. When nothing is left and `timeout` is present, it waits up to 100ms for an enqueue or close. Without `timeout` (the confirming call) it returns immediately. It never errors. |
| `sendMessage` | Records `chat_id`, `text`, `parse_mode` and `reply_markup` (decoded into `tgbotapi.InlineKeyboardMarkup`). Assigns increasing per-chat `message_id`s, updates the transcript (msgID → current text and keyboard) and returns a `Message` `{message_id, date, chat{id,type}, text}`. For a blocked chat it records the attempt and returns HTTP 403 `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`. |
| `editMessageText` | Records and updates the transcript. A missing `reply_markup` removes the keyboard. Returns a `Message` (tgbotapi unmarshals `Send` results into `Message`). |
| `editMessageReplyMarkup` | Records and replaces the keyboard; returns a `Message`. |
| `answerCallbackQuery` | Records `answers[callback_query_id] = text` (absent means `""`) and returns `true`. |
| anything else | `t.Errorf`, plus a 404 JSON |

**Strict invariants, checked at cleanup after the app stops:**
1. Every issued cbID was answered exactly once.
2. Every `sendMessage` and `editMessageText` used `parse_mode=HTML`.
3. Every recorded call for every chat was consumed by an assertion, which catches extra or duplicate messages.
4. No unknown method was called.
5. The update queue is fully confirmed.

### 8.4 Fake LeetCode (`POST /graphql`)

- Checks `Content-Type: application/json` and `User-Agent: Mozilla/5.0`.
- **Daily query** (`activeDailyCodingChallengeQuestion`): returns the current daily. The default is date `2026-09-28`, Hard, `4. Median of Two Sorted Arrays`, `/problems/median-of-two-sorted-arrays/`, tags `Array, Binary Search, Divide and Conquer`.
- **List query** (`questionList(`): asserts `categorySlug=algorithms` and serves `{total, questions}` from a catalogue keyed by `filters.difficulty` and sliced by skip and limit. The catalogue has exactly **one free problem per level** (`total: 1`):
  - Easy: `1. Two Sum`, slug `two-sum`, tags `Array, Hash Table`.
  - Medium: `2. Add Two Numbers`, slug `add-two-numbers`, tags `Linked List, Math, Recursion`.
  - Hard: `4. Median of Two Sorted Arrays`, slug `median-of-two-sorted-arrays`, tags `Array, Binary Search, Divide and Conquer`.

  So real `math/rand` is deterministic as long as random-path rows use single-level sets. Paid rejection stays in the unit tests.
- **Knobs:** `setDaily(date, difficulty)`; `failDaily(bool)` and `failList(bool)` (500 responses); `setListDelay(d)`; `setRotating(bool)` (every draw returns a different free problem regardless of skip); `dailyCalls()` and `listCalls()`.

### 8.5 Time

Cron has no clock injection. Triggering goes only through `fire` (RunNow), and timing is checked only through `scheduledAt` (Next). `TestMain` waits until `time.Now().Second() <= 45` before `m.Run()`, so the few-second suite never crosses a cron minute boundary and real cron never fires mid-test. A Done row crossing UTC midnight has a millisecond window, which is accepted.

### 8.6 Scenarios

Rows 1–16 land in step 3. They **must pass against the old code**, then pass **unchanged** after the switch. Rows 17–19 are added after the switch (step 8), because they pin deliberate changes.

| # | Scenario | Steps and assertions |
|---|---|---|
| 1 | Menu | `/start` shows the welcome naming TestBot and the 3/2/2 menu with the exact callback data. `/start@TestBot` works too. `/about`. Plain text gives `expectQuiet`. |
| 2 | Subscribe with buttons (private) | The ⚙️ Setup button is answered `""` and sends the time prompt with 8 buttons. `time:09:00` edits the **same** message to the tz prompt. `tz:Europe/Moscow` shows all three ✅. `diff:Hard` is rekeyed to ⬜ Hard. `diffsave` gives `✅ All set! I'll send you the daily problem at <b>09:00</b> (Europe/Moscow)\nDifficulty: <b>Easy, Medium</b>` with the keyboard removed. `stored` matches; `scheduledAt` 09:00 Europe/Moscow. |
| 3 | Subscribe by typing (group -100500) | `/setup@TestBot`. `9:00` gives the invalid-time edit; `21:15` gives the tz prompt; `Local` and `Mars/Base` give the invalid-tz edit; `Asia/Tbilisi` gives the difficulty keyboard. Save gives All set with `Easy, Medium, Hard`. `stored`; `scheduledAt` 21:15 Asia/Tbilisi. |
| 4 | Stale and forged buttons | The old prompt's `time:07:00` after completion gets `msgMenuExpired` and `expectQuiet`. `/setup` twice: the first prompt's button has expired, the second works. `tz:Local` and `tz:Mars/Base` expire. `diff:Insane` is answered `""` with no edit. Untick all three and save: toast `Pick at least one difficulty`, no edit. Tick Easy and save: works. `pressInline` gets `⚠️ This message is too old. Use /today to get a fresh one.`. A Save after completion has expired. |
| 5 | Re-running /setup keeps rating and pick | Seeded members and `daily_pick`. The keyboard is pre-ticked with the saved set. After Save, members and `daily_pick` are unchanged and the time is new. |
| 6 | Scheduled daily, Done, single rating | `fire` returns true with `📅 LeetCode Daily — September 28, 2026` … `🔢 4. Median of Two Sorted Arrays` … and `[✅ Done]`. Alice Done gives `✅ Counted! Your total: 1`; again gives `Already counted today!`. `/rating` gives `🏆 Solved: <b>1</b>`. `stored` members. |
| 7 | Group rating, no ties | Seeded -100123 with Alice at count 2 and an old date. `/today@TestBot`; Alice Done gives 3, Bob Done gives 1. `/rating` gives `🏆 <b>Rating</b>`, then `🥇 Alice — 3` and `🥈 @bob — 1`. |
| 8 | /difficulty leads to a random pick repeated all day | Subscribe with all three (daily Hard). `/difficulty` shows all ticked. Untick Medium and Hard; Save gives `✅ Difficulty updated: <b>Easy</b>`. `fire` gives `🎲 LeetCode Random — September 28, 2026` + `Today's daily is Hard, so here's a random Easy problem for you.` + `🔢 1. Two Sum…`, and `daily_pick` is stored. `/today` and the 📅 button repeat the identical text with `listCalls` unchanged. After `setDaily("2026-09-29", Hard)`, `fire` makes a new pick (`listCalls` grows). |
| 9 | Concurrent sends agree | An Easy-only chat, Hard daily, `setRotating(true)`, `setListDelay(200ms)`. `go fire()` and say `/today` at the same moment. Both messages are identical and equal to the stored `daily_pick`. Call counts are not asserted: the old lock makes 1 fetch, compare-and-set may make 2. |
| 10 | /daily | An unsubscribed chat gets the official Hard daily with Done. In an Easy-only chat, `/daily` and the 🗓 button send the daily, not the pick, and `daily_pick` stays nil. |
| 11 | Status and not-subscribed paths | The inactive text before setup, the active text after, and `Difficulty: <b>Any</b>` for a seeded legacy chat with no difficulties. `/difficulty` when unsubscribed, and Done on a `/daily` message in an unsubscribed chat, both give `⚠️ Use /setup to subscribe first.`. A `/difficulty` Save after `/unsubscribe` is edited to that text. |
| 12 | Unsubscribe stops sends | The 🛑 button is answered `""` and sends `🛑 Notifications disabled.`. `fire` returns false, `notScheduled`, the chat is gone from `config.json`, and `/status` is inactive. |
| 13 | Blocked bot auto-unsubscribes | Subscribe, then `block(chat)`. `fire` returns true with a `blockedAttempt`. The chat is removed from `config.json`, and `fire` then returns false. |
| 14 | LeetCode outage | With `failDaily`, `/today`, `fire` and `/daily` each send `⚠️ Failed to fetch the problem from LeetCode. Try /today later.` and the subscription is kept. `failList` with an Easy-only chat on a Hard daily gives the same notice. |
| 15 | Restart restores schedules | Seeded in today's format: a chat with members and a flat Easy `daily_pick` for 2026-09-28, plus a legacy chat with no difficulties and `members: null`. `scheduledAt` holds for both. `fire` resends the seeded pick with no list call, and the legacy chat gets the daily. `/rating` shows the seeded members. Then `/setup` a new chat, `restart()`, check `scheduledAt` for it, and `fire` delivers. `config.json` still decodes with the legacy struct. |
| 16 | Shutdown drains exactly once | Chat 1600: `say(/about)`, then immediately `stop()` without waiting for the reply, then `start()` on the same dir. Exactly one `msgAbout` reaches the chat across both runs, then `expectQuiet`. This passes whether the update was drained before shutdown (with a live ctx) or redelivered after restart. A drop or a duplicate fails it. |
| 17 | Unknown callbacks are answered (deviation a) | `pressOn` with `bogus`, `/start` and `done:x` each get `⚠️ This menu is no longer active. Use /setup or /difficulty to start again.` and `expectQuiet`. |
| 18 | Rating ties by name (deviation b) | Seeded members `Carol 2`, `Alice 2` and `Bob 3`. `/rating` gives `🥇 Bob — 3`, `🥈 Alice — 2`, `🥉 Carol — 2`. |
| 19 | Jobs root is cancelled when Run returns | Subscribe, `sync()`, then `stop()`. `fire(chat)` on the stopped app returns true, but `dailyCalls` is unchanged (the request never leaves the client) and the strict invariant shows no Bot API call was made. |

Run time is about 2–5s with parallel subtests under `-race`.

## 9. CI and tooling

`.github/workflows/ci.yml`. The `test` job (`go test ./... -race -count=1`) and the `build` job are unchanged, and the build tag keeps the suite out of `test`. The `lint` job gains the mocks check, and a new job is added:

```yaml
  lint:
    name: Lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.10.1
      - run: golangci-lint run ./...
      # new:
      - run: go install go.uber.org/mock/mockgen@v0.6.0
      - name: Mocks up to date
        run: |
          PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
          git status --porcelain
          test -z "$(git status --porcelain)"

  integration:
    name: Integration
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go test -tags integration -race -count=1 -timeout 5m ./...
```

`git status --porcelain` also catches newly generated, untracked mock files, which `git diff --exit-code` would miss. `go generate ./...` on the current tree was checked in a scratch copy and leaves it clean. The integration job is hermetic: no Docker, no services and no secrets. Under the tag it also re-runs the unit tests, so a tagged file added later in any package is picked up.

`.golangci.yml`. The resulting file passes `golangci-lint config verify` (2.13.2); the current one does not.

```diff
 run:
   timeout: 3m
+  build-tags:
+    - integration

 linters:
   # enable: and settings: unchanged
+  exclusions:
+    rules:
+      - path: _test\.go
+        linters:
+          - errcheck

 formatters:
   # unchanged (goimports, local-prefixes)
-
-issues:
-  exclude-rules:
-    - path: "_test\\.go"
-      linters: [errcheck]
```

`Makefile` (`build`, `test`, `lint` and `tidy` are unchanged):

```make
.PHONY: build test test-integration lint generate tidy

test-integration:
	go test -tags integration -race -count=1 -timeout 5m ./...

generate:
	PATH="$$(go env GOPATH)/bin:$$PATH" go generate ./...
```

The `PATH` prefix is there because mockgen lives in `$(go env GOPATH)/bin`, which is not on PATH in every shell. The Dockerfile is unchanged. In the README, the Development section gains `make test-integration` and `make generate`, and "Go 1.24+" becomes "Go 1.26+" to match `go.mod`.

## 10. Migration plan

Each step is one commit and ends green: `go build ./...`, `go test ./... -race -count=1`, `make lint` and the mocks check, plus `make test-integration` from step 3 on.

| # | Commit | Content | Extra gate |
|---|---|---|---|
| 1 | CI and lint hygiene | The `.golangci.yml` v2 exclusions, Makefile `generate`, and the CI mocks-up-to-date step. No Go changes. | `golangci-lint config verify` passes |
| 2 | Composition root and seams on the old code | (a) `leetcode.NewHTTPClient(endpoint, c)`, with `""` meaning `graphqlURL`. This updates its one production caller (in main today; it moves into `app.New` in (d)) and the 3 test call sites in `http_test.go`, and `TestNewHTTPClient` gains the explicit-endpoint row. (b) The old `cronScheduler` gains `Start` (the constructor still starts it; `cron.Start` is idempotent), `Stop`, `RunNow` and `Next`. (c) The old bot gains `Handle(_ context.Context, u tgbotapi.Update) { b.handleMessage(u) }`; `Run` is deleted, `GetUpdatesChan` is dropped from `telegramSender`, and `go generate`. (d) `internal/app`: `Config`, `New(_ context.Context, cfg)` with the 75s client and today's wiring (bot.New, NewCronScheduler, SetScheduler, restore loop), and `Run(ctx)` in its final form except `defer a.cancelJobs()`. (e) `main.go` shrinks to about 25 lines. | No user-visible change except D3(d) and D3(e) |
| 3 | Characterization suite | `internal/app/*_test.go` with fakes, harness and rows 1–16; `run.build-tags`; Makefile `test-integration`; the CI Integration job. The storage golden fixture is produced by the old `jsonStorage` and loaded by the old `json_test.go`. | The suite passes **against the old implementation** |
| 4 | `internal/domain` | Model with the legacy tags, rules, errors and pure tests. Nothing imports it yet. | – |
| 5 | `internal/notifier` | `deps.go` + `go generate`, `service.go`, `delivery.go` and unit tests. Not wired yet. | – |
| 6 | `internal/telegram` | `deps.go`, `sender.go`, `handler.go`, `dialog.go`, `view.go`, mocks and unit tests. `view_test.go` copies `leetcode/format_test.go` byte for byte. Not wired yet. | – |
| 7 | Switch (one mechanical commit) | (a) storage: delete `storage.go`; `json.go` moves to `domain.Chat` with ctx, `Upsert`, `Update` over `mutate`, `Get`/`All` with error, and `All` sorted; adapt `json_test.go` and add the rollback check. (b) leetcode: delete `client.go` and the `Format*` functions; ctx and value returns; adapt `http_test.go`; delete `client_test.go` and `format_test.go`. (c) scheduler: rewrite `cron.go` and delete `scheduler.go`; add `cron_test.go`. (d) app: the new wiring in `New` (jobs root, `Restore(ctx)`) and `defer a.cancelJobs()` in `Run`. (e) Delete `internal/bot` (10 production files, 9 test files, `mocks/`). (f) `go generate ./...` and `go mod tidy` (no new modules). | `git diff HEAD~1 -- 'internal/app/*_test.go'` is **empty** and the suite passes |
| 8 | Deliberate-fix rows and docs | Integration rows 17–19. README (Development, Go version). The CLAUDE.md Test Style example, which still shows the deleted `New(tt.senderMock(ctrl), "TestBot", …)`. The project-memory architecture notes, outside the repo. | – |

Deploy notes. `config.json` is read as is and stays readable by the old binary, so rollback is safe (5.2). Callback data strings are unchanged, so ✅ Done buttons on earlier messages keep working. Dialog sessions in progress at deploy time are lost, as on any restart today.

## 11. Behaviour changes and what stays identical

**Deliberate changes (exhaustive):**

| # | Change | Today | Visible to users |
|---|---|---|---|
| a | Unknown or forged callback data is answered with `msgMenuExpired` | never answered (spinner) | yes, a toast |
| b | `/rating` ties are ordered by name | random map order (`sort.Slice` is not stable) | yes, only for ties |
| c | Panics in cron jobs and update handling are recovered and logged | the process dies | only via uptime |
| d | The Telegram `http.Client` has a 75s timeout | no timeout | no |
| e | SIGINT/SIGTERM shut down gracefully: drain, confirm, wait for jobs; the process then exits with status 0 instead of dying from the signal | immediate exit; buffered updates can be lost, or the last batch redelivered | no |
| f | `Schedule` parses before replacing, so an invalid spec keeps the old job | the old job is removed first | no (input is validated upstream) |
| g | Key order inside `daily_pick` in `config.json` | `date, daily_difficulty, …` | no (5.2) |
| h | An update or job that exceeds its 2-minute ctx budget is cut off at the next port call. This is the direct consequence of D2's per-update and per-job timeouts, not a separate decision. | no overall budget (only LeetCode's 10s per request) | only under a combined LeetCode and Telegram outage |

**Invisible internals.** Under contention compare-and-set may make one extra `FetchRandom`. `Unsubscribe` now removes the cron entry before deleting the chat. `Restore` runs in `ChatID` order. Some log lines have new texts; log output is not a contract.

**Byte-identical:** every message text; every keyboard label and callback data; `parse_mode=HTML` on every send and text edit; answer-before-edit order; which failures produce the fetch-failed notice; 403 handling on problem sends only; `config.json` content apart from (g); `BOT_TOKEN`, `STORAGE_PATH` and the default `config.json`; the `BOT_TOKEN environment variable is not set`, `storage: …`, `NewBotAPI: …` and `Authorized as @…` log lines; the Dockerfile and `go run main.go`.

## 12. Risks and mitigations

| Risk | Mitigation |
|---|---|
| The switch commit (step 7) is the largest. | The new packages are unit-tested in steps 4–6, and the step-3 suite must pass against both implementations without edits. |
| JSON tags on domain types make the domain the persisted contract; a careless rename silently changes the file. | The golden and rollback storage test, plus the legacy-struct `stored()` in the suite. |
| Compare-and-set gives up "exactly one FetchRandom under contention" (the old `locks_test` asserted `Times(1)`). | Bounded to one extra call, only when a cron tick and `/today` coincide for one chat. Row 9 asserts agreement, not counts. |
| Shutdown normally takes up to 60s for the in-flight long poll. Under an outage it can take longer: up to `updateTimeout` per drained update and `jobTimeout` for running jobs. Docker's default 10s grace then ends in SIGKILL. | SIGKILL is safe: state is persisted synchronously and unconfirmed updates are redelivered (the last batch may be handled once more). The follow-up raises the grace to ≥ 70s. |
| An in-flight tgbotapi call ignores ctx. | The 75s client timeout. The sender refuses to start calls on a done ctx. |
| A lifecycle ctx is stored in `notifier.service.jobs`. | Deliberate: it is the owner of job contexts and is documented on the field. The alternative, a service-owned `Close`, would need another app interface. |
| Fake drift: the fakes model only what tgbotapi v5.5.1 and our two GraphQL queries use. | Unknown methods fail tests. There is no live smoke test; this is accepted. |
| `RunNow` and `Next` are exported only as seams. | They run or inspect only what cron itself would run (`WrappedJob`, `Schedule.Next`). |
| Integration timing. | 5s waits; the quiet-minute guard; single-level sets on random paths; the barrier `sync()`. |
| `os.WriteFile` without rename can truncate `config.json` on a crash mid-write, as today. | The price of bind-mount compatibility. The README already disclaims durability. |
| The integration tests are white-box, so renaming an app field breaks them. | This fails at compile time, never silently. |

## 13. Follow-ups (out of scope; each a separate PR after the refactor)

1. **Fail fast on a corrupt `config.json`.** Today an unmarshal error is logged, the store starts empty, and the next save overwrites the file, which loses every subscription. Return the error from `NewJSONStorage` instead.
2. **HTML-escape member names and problem titles** in HTML-mode messages (`formatRating`, `formatPick`). A name like `<b` currently breaks the message.
3. **Per-chat parallel update handling**: parallel across chats, ordered within a chat, so one slow LeetCode fetch stops stalling every chat. The suite's `sync()` barrier must become per chat.
4. **Docker stop grace ≥ 70s**: `stop_grace_period: 70s` in Compose, or `docker run --stop-timeout 70`, wherever the deployment is defined (this repo holds only the Dockerfile). This makes shutdown fully graceful across the 60s long poll in the normal case, with no LeetCode or Telegram outage.

Also manual: in GitHub branch protection on `main`, make the **Integration** check required. This is a repository setting the owner changes; it is not part of any PR.

## 14. Estimated size

Before is measured with `wc -l` at `8879c84`; after is estimated. Generated mocks are excluded from the production and unit columns.

| Package | Prod before | Prod after | Unit tests before | Unit tests after | Mocks before → after |
|---|---|---|---|---|---|
| `main.go` | 48 | ~25 | 0 | 0 | – |
| `internal/app` | – | ~90 | – | 0 (integration ~1075) | – |
| `internal/bot` | 1096 | deleted | 3541 | deleted | 273 → – |
| `internal/domain` | – | ~110 | – | ~180 | – |
| `internal/notifier` | – | ~215 (deps 30, service 115, delivery 70) | – | ~580 | – → ~210 |
| `internal/telegram` | – | ~500 (deps 30, sender 70, handler 125, dialog 125, view 150) | – | ~460 | – → ~135 |
| `internal/leetcode` | 283 | ~230 | 682 | ~480 | 54 → 54 |
| `internal/storage` | 153 | ~120 | 373 | ~360 | – |
| `internal/scheduler` | 68 | ~85 | 0 | ~110 | – |
| **Total** | **1648** | **~1375 (−17%)** | **4596** | **~2170 (−53%)** | **327 → ~400** |

Threading ctx costs about +60 production lines compared with the ctx-free variant (~1315). Integration: fake Telegram ~220, fake LeetCode ~110, harness ~220, scenarios ~525.

Where the old code goes:
- `bot.go`, `router.go` and `commands.go` become `handler.go`; `setup.go`, `difficulty.go` and `state.go` become `dialog.go`; `const.go`, `keyboard.go` and leetcode's `Format*` become `view.go`.
- `send.go` splits into `telegram/sender.go` and `notifier/delivery.go`.
- Deleted outright: `locks.go` and `locks_test.go` (103 lines); `SetScheduler`, `SendFunc` and the scheduler-to-storage coupling; `storage.Set` and the Update-then-Set dance; the duplicated `DailyPick` and its 2 converters; `msgSessionExpired`; 7 duplicated `answerCB("")` calls and the 12-case callback switch; about 3000 lines of `MessageConfig` scenario tests.
