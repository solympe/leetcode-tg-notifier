# Architecture Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Split the single `internal/bot` package into a stdlib-only domain, a use-case core behind ctx-first ports declared by their consumers, and thin Telegram, LeetCode, JSON-file and cron adapters wired by one composition root. Texts, keyboards, callback data and `config.json` stay byte-compatible, apart from the approved deviations in spec §11.

**Architecture:** Compact hexagonal-lite (spec §3). `main` → `internal/app` (composition root, lifecycle, integration suite) → `internal/telegram`, `internal/notifier`, `internal/storage`, `internal/leetcode` and `internal/scheduler`; all of these except scheduler import the stdlib-only `internal/domain`. A characterization suite (Tasks 4-5) is written against today's code and must pass unchanged after the switch (Task 10).

**Tech Stack:** Go 1.26.1; `github.com/go-telegram-bot-api/telegram-bot-api/v5` v5.5.1; `github.com/robfig/cron/v3` v3.0.1; `go.uber.org/mock` v0.6.0 (mockgen v0.6.0, `-source` mode); golangci-lint v2 (2.13.2 locally, v2.10.1 in CI); GitHub Actions.

**Spec:** docs/superpowers/specs/2026-09-28-architecture-refactor-design.md

## Global Constraints

- Module `github.com/solympe/leetcode-tg-notifier`, `go 1.26.1`; no new modules, so `go mod tidy` changes nothing in any task.
- Every I/O port method takes `ctx context.Context` first: `chatStore` (Get/Upsert/Update/Delete/All), `problemSource`, `messenger`, every exported notifier method and telegram's `service`. `dailyScheduler` and `jobRunner` take no ctx (spec D2, §4.8).
- Interfaces are private and declared by their consumer: `deps.go` in notifier and telegram, `randSource` in leetcode, `jobRunner` and `updateHandler` in app. No exported interface; every constructor returns `*privateStruct`.
- `//go:generate mockgen -source=<file> -destination=mocks/mock_deps.go -package=mocks` appears only in `notifier/deps.go`, `telegram/deps.go` and `leetcode/http.go`. There is one `mocks/` per consumer, and no mock imports the package it mocks.
- Unit tests are table-driven. Mock factories are struct fields `func(*gomock.Controller) *mocks.MockX` with `EXPECT()` inside, and the bare constructor is used when there are no expectations. Each `Test` declares `ctx := t.Context()` once, before its table. IDs in matchers are typed: `gomock.Eq(int64(100))`.
- Timeouts: Telegram `http.Client` 75s; long poll 60s; per-update ctx 2m; per-job ctx `jobTimeout = 2 * time.Minute`; LeetCode client 10s per request; `maxRandomAttempts = 8`.
- `config.json` is `{"chats":{"<id>":Chat}}`, written with `json.MarshalIndent(…, "", "  ")` and `os.WriteFile(path, data, 0644)` in place, never through a temp file and rename. The only byte change is the key order inside `daily_pick`.
- Every text, keyboard label and callback data stays byte-identical, and `parse_mode=HTML` is set on every `sendMessage` and `editMessageText`. The only behaviour changes are spec §11 a-h.
- `BOT_TOKEN` and `STORAGE_PATH` (default `config.json`) are unchanged, and so are the log lines `BOT_TOKEN environment variable is not set`, `storage: …`, `NewBotAPI: …` and `Authorized as @…`.
- Imports are grouped stdlib | third-party | `github.com/solympe/...` (goimports `local-prefixes`).
- The integration suite uses `//go:build integration` and `package app`. Its expected texts are literal copies, and `TestMain` waits until second ≤ 45 of a minute.
- Each task is one commit and ends green: `go build ./...`, `go test ./... -race -count=1`, `make lint`, the mocks check (`PATH="$(go env GOPATH)/bin:$PATH" go generate ./...`, then `test -z "$(git status --porcelain)"`), and `make test-integration` from Task 4 on.
- Commit messages are conventional, and their last line is `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.
- Every tracked file in the working tree is currently read-only (`r--r--r--`). Task 4 Step 1 makes them writable; git does not track the write bit.

## Review Focus

These five inputs follow from the spec, but no test the spec lists exercises them. Each one is now pinned by a test in its owning task. The Task 5 pins pass against both the old and the new code.

1. **A deploy mid-dialog** (spec §10 deploy notes): after a restart, `time:`, `tz:`, `diff:` and `diffsave` on a prompt sent before the restart get `⚠️ This menu is no longer active. Use /setup or /difficulty to start again.` with no edit. Typed dialog input is ignored, and `✅ Done` and the menu buttons on older messages keep working. Pinned by Task 5, row `20 restart mid-dialog expires its buttons, not Done`.
2. **One unschedulable stored chat** (a hand-edited `"0900"`, or a zone the host's tzdata no longer has, such as `US/Pacific-New`, which tzdata 2020b dropped): restore logs it, startup and every other chat carry on, and nothing is deleted from `config.json` (spec §4.2, "never fatal"). Pinned by Task 5, row `21 an unschedulable stored chat does not block the others`.
3. **Operator misconfiguration**: a wrong `BOT_TOKEN` makes `New` fail with `NewBotAPI: …`, wrapping the 401 `*tgbotapi.Error`. A `STORAGE_PATH` that is a directory fails with `storage: load storage: …` (the spec §11 log lines). Pinned by Task 5, `TestNewFails`.
4. **A bind-mounted `config.json`** (spec §4.7): every Upsert, Update and Delete rewrites the same file, with the same inode and mode and no temp file left behind. Pinned by Task 10, `TestSaveInPlace`.
5. **A damaged `config.json`** (empty, truncated by a crash mid-write, or with a field of the wrong type; spec §12): `NewJSONStorage` logs the error and starts empty, never half-loaded, and the next save writes a valid file. Follow-up 1 changes this on purpose. Pinned by Task 10, `TestLoadDamagedFile`.

---

### Task 1: CI and lint hygiene (done: ab2a02a)

**Files:**
- Modify: `.golangci.yml` (whole file: `issues.exclude-rules`, lines 28-31, becomes `linters.exclusions.rules`)
- Modify: `Makefile` (whole file: new `generate` target)
- Modify: `.github/workflows/ci.yml` (lint job, after line 39: mockgen install and the mocks-up-to-date step)

**Interfaces:** Consumes: nothing. Produces: `make generate` (runs `go generate ./...` with `$(go env GOPATH)/bin` on PATH); `.golangci.yml` with a `linters.exclusions` section that passes `golangci-lint config verify` (Task 4 adds `run.build-tags: [integration]`); the CI lint step "Mocks up to date". Task 4 adds the `test-integration` target (and its `.PHONY` entry) and the CI `integration` job. No Go changes.

- [x] **Step 1: Confirm that the current config fails verification**

Run: `golangci-lint config verify; echo "exit=$?"`
Expected:
```
jsonschema: "issues" does not validate with "/properties/issues/additionalProperties": additional properties 'exclude-rules' not allowed
The command is terminated due to an error: the configuration contains invalid elements
exit=3
```

- [x] **Step 2: Replace `.golangci.yml`**

```yaml
version: "2"

run:
  timeout: 3m

linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - revive
    - misspell
  settings:
    revive:
      rules:
        - name: exported
          disabled: true
        - name: package-comments
          disabled: true
  exclusions:
    rules:
      - path: _test\.go
        linters:
          - errcheck

formatters:
  enable:
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/solympe/leetcode-tg-notifier
```

- [x] **Step 3: Verify the config and lint**

Run: `golangci-lint config verify; echo "exit=$?"; make lint`
Expected: no output from verify, then `exit=0`, then `golangci-lint run ./...` and `0 issues.`

- [x] **Step 4: Replace `Makefile`**

Recipe lines start with a tab.

```make
.PHONY: build test lint generate tidy

build:
	go build ./...

test:
	go test ./... -race -count=1

lint:
	golangci-lint run ./...

generate:
	PATH="$$(go env GOPATH)/bin:$$PATH" go generate ./...

tidy:
	go mod tidy
```

- [x] **Step 5: Check that `make generate` leaves the mocks untouched**

Run: `make generate && git status --porcelain`
Expected: `PATH="$(go env GOPATH)/bin:$PATH" go generate ./...`, then only
```
 M .golangci.yml
 M Makefile
```

- [x] **Step 6: Replace `.github/workflows/ci.yml`**

`test` and `build` are unchanged. The lint job gains the last two steps.

```yaml
name: CI

on:
  push:
    branches: ["**"]
  pull_request:
    branches: ["**"]

jobs:
  test:
    name: Test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go test ./... -race -count=1

  build:
    name: Build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go build ./...

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
      - run: go install go.uber.org/mock/mockgen@v0.6.0
      - name: Mocks up to date
        run: |
          PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
          git status --porcelain
          test -z "$(git status --porcelain)"
```

- [x] **Step 7: Task gate**

Run:
```bash
golangci-lint config verify && go build ./... && go test ./... -race -count=1 && make lint
```
Expected: `ok` for `internal/bot`, `internal/leetcode` and `internal/storage` (`internal/scheduler` has no test files yet), then `0 issues.`

- [x] **Step 8: Commit**

```bash
git add .golangci.yml Makefile .github/workflows/ci.yml
git commit -F - <<'EOF'
ci: v2 lint exclusions, make generate and a mocks-up-to-date check

golangci-lint config verify rejected issues.exclude-rules, which is v1
syntax; the test-file errcheck rule moves to linters.exclusions. The
lint job now fails when go generate changes or adds a file.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [x] **Step 9: Mocks check on the committed tree (the CI step, verbatim)**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `CLEAN` and nothing else. If files are listed, `git add` them and `git commit --amend --no-edit`.

---

### Task 2: Seams on the old code (done: c9eb414)

**Files:**
- Modify: `internal/leetcode/http.go:5-6` (import `cmp`), `internal/leetcode/http.go:104-111` (`NewHTTPClient`)
- Modify: `internal/leetcode/http_test.go:384-386`, `:408-437` (`TestNewHTTPClient`), `:511-514`
- Create: `internal/scheduler/cron_test.go`
- Modify: `internal/scheduler/cron.go` (whole file: import `time`; add `Start`, `Stop`, `RunNow`, `Next` and the private `entry`)
- Create: `internal/bot/bot_test.go`
- Modify: `internal/bot/bot.go:5-10` (import `context`), `:15` (drop `GetUpdatesChan`), `:61-68` (`Run` becomes `Handle`)
- Modify (generated): `internal/bot/mocks/mock_deps.go` (loses `GetUpdatesChan`)
- Modify: `internal/bot/setup_test.go:294` (comment only)
- Modify: `main.go:3-5` (import `context`), `:37`, `:47` (interim polling loop; Task 3 replaces the whole file)

**Interfaces:** Consumes: nothing from earlier tasks (Task 1 only changed tooling). Produces:
- `leetcode.NewHTTPClient(endpoint string, c *http.Client) *httpClient`: `""` means `graphqlURL` (`https://leetcode.com/graphql`); `nil` c means `&http.Client{Timeout: 10 * time.Second}`. `FetchDaily() (*Problem, error)` and `FetchRandom([]string) (*Problem, error)` are unchanged.
- On the old `*scheduler.cronScheduler` (still built by `NewCronScheduler(send SendFunc)`, which still starts cron): `Start()`, `Stop()`, `RunNow(chatID int64) bool`, `Next(chatID int64) (time.Time, bool)`. `Schedule(chatID int64, cfg storage.ChatConfig) error` and `Remove(chatID int64)` are unchanged.
- On the old `*bot.tgBot`: `Handle(_ context.Context, u tgbotapi.Update)`. Deleted: `(*tgBot).Run`, `telegramSender.GetUpdatesChan`, `(*mocks.MocktelegramSender).GetUpdatesChan`.
- Test-only, in `package scheduler`: `recorder` (`send(chatID int64)`, `sent() []int64`, field `then func(int64)`) and `chatAt(chatID int64, notifyTime, timezone string) storage.ChatConfig`. Task 10's rewrite of `cron_test.go` replaces them.

- [x] **Step 1: Make the leetcode tests pass the endpoint to the constructor**

In `internal/leetcode/http_test.go`, `TestFetchRandom` loop body (lines 384-386), replace
```go
			hc := NewHTTPClient(srv.Client())
			hc.endpoint = srv.URL
			hc.rnd = tt.randMock(ctrl)
```
with
```go
			hc := NewHTTPClient(srv.URL, srv.Client())
			hc.rnd = tt.randMock(ctrl)
```

In `TestFetchDaily` (lines 511-514), replace
```go
			hc := NewHTTPClient(srv.Client())
			hc.endpoint = srv.URL

			got, err := hc.FetchDaily()
```
with
```go
			hc := NewHTTPClient(srv.URL, srv.Client())

			got, err := hc.FetchDaily()
```

Replace the whole `TestNewHTTPClient` (lines 408-437) with:
```go
func TestNewHTTPClient(t *testing.T) {
	explicit := &http.Client{Timeout: time.Minute}
	const local = "http://127.0.0.1:8080/graphql"

	tests := []struct {
		name         string
		endpoint     string
		client       *http.Client
		wantEndpoint string
		wantTimeout  time.Duration
	}{
		{name: "nil gets a client with a 10s timeout", client: nil, wantEndpoint: graphqlURL, wantTimeout: 10 * time.Second},
		{name: "explicit client is used as is", client: explicit, wantEndpoint: graphqlURL, wantTimeout: time.Minute},
		{name: "explicit endpoint is used as is", endpoint: local, client: nil, wantEndpoint: local, wantTimeout: 10 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := NewHTTPClient(tt.endpoint, tt.client)
			if tt.client != nil && hc.http != tt.client {
				t.Errorf("http client: got %p, want the explicit %p", hc.http, tt.client)
			}
			if hc.http == http.DefaultClient {
				t.Error("http client must not be http.DefaultClient, which has no timeout")
			}
			if hc.http.Timeout != tt.wantTimeout {
				t.Errorf("timeout: got %v, want %v", hc.http.Timeout, tt.wantTimeout)
			}
			if hc.endpoint != tt.wantEndpoint {
				t.Errorf("endpoint: got %q, want %q", hc.endpoint, tt.wantEndpoint)
			}
		})
	}
}
```

- [x] **Step 2: Run the leetcode tests to see them fail**

Run: `go test ./internal/leetcode/ -race -count=1`
Expected: build failure at `http_test.go:384`, `:425` and `:514`, each reading
```
too many arguments in call to NewHTTPClient
	have (string, *http.Client)
	want (*http.Client)
```

- [x] **Step 3: Give `NewHTTPClient` an endpoint parameter**

In `internal/leetcode/http.go`, change the import block's first lines from
```go
import (
	"bytes"
```
to
```go
import (
	"bytes"
	"cmp"
```
and replace lines 104-111
```go
// NewHTTPClient returns a LeetCode client that sends requests with c, or with
// a client limited to defaultHTTPTimeout per request when c is nil.
func NewHTTPClient(c *http.Client) *httpClient {
	if c == nil {
		c = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &httpClient{http: c, endpoint: graphqlURL, rnd: globalRand{}}
}
```
with
```go
// NewHTTPClient returns a LeetCode client that POSTs to endpoint, or to
// graphqlURL when endpoint is empty. It sends requests with c, or with a
// client limited to defaultHTTPTimeout per request when c is nil.
func NewHTTPClient(endpoint string, c *http.Client) *httpClient {
	if c == nil {
		c = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &httpClient{http: c, endpoint: cmp.Or(endpoint, graphqlURL), rnd: globalRand{}}
}
```

- [x] **Step 4: Update the production caller**

In `main.go:37`, replace
```go
	b := bot.New(api, api.Self.UserName, store, leetcode.NewHTTPClient(nil), nil)
```
with
```go
	b := bot.New(api, api.Self.UserName, store, leetcode.NewHTTPClient("", nil), nil)
```

- [x] **Step 5: Re-run the leetcode tests and build**

Run: `go test ./internal/leetcode/ -race -count=1 && go build ./...`
Expected: `ok  	github.com/solympe/leetcode-tg-notifier/internal/leetcode`, and the build prints nothing.

- [x] **Step 6: Write the scheduler seam tests**

Create `internal/scheduler/cron_test.go`:
```go
package scheduler

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

// recorder is a SendFunc that records the chats it ran for and then calls
// then, when set.
type recorder struct {
	mu    sync.Mutex
	chats []int64
	then  func(chatID int64)
}

func (r *recorder) send(chatID int64) {
	r.mu.Lock()
	r.chats = append(r.chats, chatID)
	r.mu.Unlock()
	if r.then != nil {
		r.then(chatID)
	}
}

func (r *recorder) sent() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.chats)
}

func chatAt(chatID int64, notifyTime, timezone string) storage.ChatConfig {
	return storage.ChatConfig{ChatID: chatID, NotifyTime: notifyTime, Timezone: timezone}
}

func TestNext(t *testing.T) {
	tests := []struct {
		name       string
		notifyTime string
		timezone   string
		lifecycle  func(s *cronScheduler)
	}{
		{name: "Europe/Moscow", notifyTime: "09:30", timezone: "Europe/Moscow", lifecycle: func(*cronScheduler) {}},
		{name: "Asia/Dubai", notifyTime: "07:00", timezone: "Asia/Dubai", lifecycle: func(*cronScheduler) {}},
		{name: "UTC", notifyTime: "00:00", timezone: "UTC", lifecycle: func(*cronScheduler) {}},
		{name: "second Start is a no-op", notifyTime: "09:30", timezone: "Europe/Moscow", lifecycle: func(s *cronScheduler) { s.Start() }},
		{name: "after Stop", notifyTime: "07:00", timezone: "Asia/Dubai", lifecycle: func(s *cronScheduler) { s.Stop() }},
		{name: "after Stop and Start", notifyTime: "21:15", timezone: "UTC", lifecycle: func(s *cronScheduler) { s.Stop(); s.Start() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := time.LoadLocation(tt.timezone)
			if err != nil {
				t.Fatalf("LoadLocation: %v", err)
			}
			s := NewCronScheduler((&recorder{}).send)
			t.Cleanup(s.Stop)
			if err := s.Schedule(100, chatAt(100, tt.notifyTime, tt.timezone)); err != nil {
				t.Fatalf("Schedule: %v", err)
			}
			tt.lifecycle(s)

			before := time.Now()
			got, ok := s.Next(100)
			if !ok {
				t.Fatal("Next: got false, want true")
			}
			if hm := got.In(loc).Format("15:04"); hm != tt.notifyTime {
				t.Errorf("Next in %s: got %s, want %s", tt.timezone, hm, tt.notifyTime)
			}
			if d := got.Sub(before); d <= 0 || d > 24*time.Hour {
				t.Errorf("Next is %v from now, want within (0, 24h]", d)
			}
		})
	}
}

func TestRunNow(t *testing.T) {
	// Twelve and thirteen hours from now, so cron itself never fires these
	// entries while the test runs.
	idle := time.Now().UTC().Add(12 * time.Hour).Format("15:04")
	later := time.Now().UTC().Add(13 * time.Hour).Format("15:04")

	tests := []struct {
		name        string
		setup       func(s *cronScheduler) error
		then        func(s *cronScheduler, chatID int64) // runs inside the job
		wantRun     bool
		wantSent    []int64
		wantNext    bool
		wantEntries int
	}{
		{
			name:        "scheduled chat runs its job synchronously",
			setup:       func(s *cronScheduler) error { return s.Schedule(100, chatAt(100, idle, "UTC")) },
			wantRun:     true,
			wantSent:    []int64{100},
			wantNext:    true,
			wantEntries: 1,
		},
		{
			name: "only the chat's own job runs",
			setup: func(s *cronScheduler) error {
				if err := s.Schedule(200, chatAt(200, idle, "UTC")); err != nil {
					return err
				}
				return s.Schedule(100, chatAt(100, idle, "UTC"))
			},
			wantRun:     true,
			wantSent:    []int64{100},
			wantNext:    true,
			wantEntries: 2,
		},
		{
			name: "rescheduling leaves one entry",
			setup: func(s *cronScheduler) error {
				if err := s.Schedule(100, chatAt(100, idle, "UTC")); err != nil {
					return err
				}
				return s.Schedule(100, chatAt(100, later, "UTC"))
			},
			wantRun:     true,
			wantSent:    []int64{100},
			wantNext:    true,
			wantEntries: 1,
		},
		{
			name:  "unknown chat",
			setup: func(*cronScheduler) error { return nil },
		},
		{
			name: "removed chat",
			setup: func(s *cronScheduler) error {
				if err := s.Schedule(100, chatAt(100, idle, "UTC")); err != nil {
					return err
				}
				s.Remove(100)
				return nil
			},
		},
		{
			name: "runs after Stop",
			setup: func(s *cronScheduler) error {
				if err := s.Schedule(100, chatAt(100, idle, "UTC")); err != nil {
					return err
				}
				s.Stop()
				return nil
			},
			wantRun:     true,
			wantSent:    []int64{100},
			wantNext:    true,
			wantEntries: 1,
		},
		{
			// A blocked chat unsubscribes from inside its own job.
			name:     "a job that removes its own chat does not deadlock",
			setup:    func(s *cronScheduler) error { return s.Schedule(100, chatAt(100, idle, "UTC")) },
			then:     func(s *cronScheduler, chatID int64) { s.Remove(chatID) },
			wantRun:  true,
			wantSent: []int64{100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			var s *cronScheduler
			if tt.then != nil {
				rec.then = func(chatID int64) { tt.then(s, chatID) }
			}
			s = NewCronScheduler(rec.send)
			t.Cleanup(s.Stop)
			if err := tt.setup(s); err != nil {
				t.Fatalf("setup: %v", err)
			}

			done := make(chan bool, 1)
			go func() { done <- s.RunNow(100) }()
			select {
			case got := <-done:
				if got != tt.wantRun {
					t.Errorf("RunNow: got %v, want %v", got, tt.wantRun)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("RunNow did not return: deadlock")
			}
			if got := rec.sent(); !slices.Equal(got, tt.wantSent) {
				t.Errorf("sent: got %v, want %v", got, tt.wantSent)
			}
			if _, ok := s.Next(100); ok != tt.wantNext {
				t.Errorf("Next: got %v, want %v", ok, tt.wantNext)
			}
			if got := len(s.c.Entries()); got != tt.wantEntries {
				t.Errorf("cron entries: got %d, want %d", got, tt.wantEntries)
			}
		})
	}
}

func TestStop(t *testing.T) {
	tests := []struct {
		name     string
		job      func(s *cronScheduler) // runs once the job is released
		wantNext bool
	}{
		{name: "waits for a running job", job: func(*cronScheduler) {}, wantNext: true},
		{name: "a running job may call Remove", job: func(s *cronScheduler) { s.Remove(100) }, wantNext: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewCronScheduler((&recorder{}).send)
			t.Cleanup(s.Stop)
			started, release := make(chan struct{}), make(chan struct{})
			releaseJob := sync.OnceFunc(func() { close(release) })
			t.Cleanup(releaseJob) // runs before s.Stop, so a failed test never hangs

			// A one-second schedule makes cron itself start the job; the
			// daily specs Schedule builds never fire within a test.
			var runs atomic.Int32
			id := s.c.Schedule(cron.Every(time.Second), cron.FuncJob(func() {
				if runs.Add(1) > 1 {
					return
				}
				close(started)
				<-release
				tt.job(s)
			}))
			s.mu.Lock()
			s.entries[100] = id
			s.mu.Unlock()

			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("cron did not start the job")
			}
			stopped := make(chan struct{})
			go func() {
				s.Stop()
				close(stopped)
			}()
			select {
			case <-stopped:
				t.Fatal("Stop returned while the job was running")
			case <-time.After(100 * time.Millisecond):
			}
			releaseJob()
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("Stop did not return after the job finished")
			}
			if _, ok := s.Next(100); ok != tt.wantNext {
				t.Errorf("Next: got %v, want %v", ok, tt.wantNext)
			}
		})
	}
}
```

- [x] **Step 7: Run them to see them fail**

Run: `go test ./internal/scheduler/ -race -count=1`
Expected: build failure starting with
```
s.Start undefined (type *cronScheduler has no field or method Start)
s.Stop undefined (type *cronScheduler has no field or method Stop)
```
and also listing `s.Next undefined` and `s.RunNow undefined`.

- [x] **Step 8: Add the seams to the old scheduler**

Replace `internal/scheduler/cron.go` with (lines 1-65 are today's code plus the `time` import; `Start`, `Stop`, `RunNow`, `Next` and `entry` are new):
```go
package scheduler

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

type cronScheduler struct {
	c       *cron.Cron
	send    SendFunc
	mu      sync.Mutex
	entries map[int64]cron.EntryID
}

func NewCronScheduler(send SendFunc) *cronScheduler {
	c := cron.New()
	c.Start()
	return &cronScheduler{
		c:       c,
		send:    send,
		entries: make(map[int64]cron.EntryID),
	}
}

func (cs *cronScheduler) Schedule(chatID int64, cfg storage.ChatConfig) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if id, ok := cs.entries[chatID]; ok {
		cs.c.Remove(id)
	}

	parts := strings.Split(cfg.NotifyTime, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid time %q", cfg.NotifyTime)
	}
	hour, minute := parts[0], parts[1]
	spec := fmt.Sprintf("CRON_TZ=%s %s %s * * *", cfg.Timezone, minute, hour)

	id, err := cs.c.AddFunc(spec, func() {
		cs.send(chatID)
	})
	if err != nil {
		return fmt.Errorf("AddFunc: %w", err)
	}
	cs.entries[chatID] = id
	log.Printf("Scheduled chat %d at %s %s (entry %d)", chatID, cfg.NotifyTime, cfg.Timezone, id)
	return nil
}

func (cs *cronScheduler) Remove(chatID int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if id, ok := cs.entries[chatID]; ok {
		cs.c.Remove(id)
		delete(cs.entries, chatID)
	}
}

// Start starts firing scheduled jobs. NewCronScheduler has already started
// the scheduler and cron ignores a second Start, so calling it is always safe.
func (cs *cronScheduler) Start() {
	cs.c.Start()
}

// Stop stops new firings and waits for the jobs cron has already started.
// It does not hold cs.mu while waiting, so a running job may call Remove.
func (cs *cronScheduler) Stop() {
	<-cs.c.Stop().Done()
}

// RunNow runs the chat's scheduled job synchronously, exactly as cron would
// run it, and reports whether the chat has one. cs.mu is released before the
// job runs, so a job that removes its own chat cannot deadlock. It also works
// after Stop.
func (cs *cronScheduler) RunNow(chatID int64) bool {
	e, ok := cs.entry(chatID)
	if !ok {
		return false
	}
	e.WrappedJob.Run()
	return true
}

// Next returns the chat's next firing time, in time.Local, and whether the
// chat has a scheduled job. It works whether or not cron is running.
func (cs *cronScheduler) Next(chatID int64) (time.Time, bool) {
	e, ok := cs.entry(chatID)
	if !ok {
		return time.Time{}, false
	}
	return e.Schedule.Next(time.Now()), true
}

// entry returns a snapshot of the chat's cron entry. It holds cs.mu only to
// look up the entry ID.
func (cs *cronScheduler) entry(chatID int64) (cron.Entry, bool) {
	cs.mu.Lock()
	id, ok := cs.entries[chatID]
	cs.mu.Unlock()
	if !ok {
		return cron.Entry{}, false
	}
	e := cs.c.Entry(id)
	return e, e.Valid()
}
```

- [x] **Step 9: Re-run the scheduler tests**

Run: `go test ./internal/scheduler/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected:
```
--- PASS: TestNext
--- PASS: TestRunNow
--- PASS: TestStop
ok  	github.com/solympe/leetcode-tg-notifier/internal/scheduler
```
(each `--- PASS` line ends with its duration; TestStop takes about 2s because cron's shortest schedule is one second).

- [x] **Step 10: Write the `Handle` test**

Create `internal/bot/bot_test.go`:
```go
package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/bot/mocks"
)

func TestHandle(t *testing.T) {
	ctx := t.Context()
	chat := &tgbotapi.Chat{ID: 100}

	tests := []struct {
		name       string
		update     tgbotapi.Update
		senderMock func(*gomock.Controller) *mocks.MocktelegramSender
	}{
		{
			name:   "message goes to its command",
			update: tgbotapi.Update{UpdateID: 1, Message: &tgbotapi.Message{Chat: chat, Text: "/about"}},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Send(gomock.Eq(textMsg(100, msgAbout))).Return(tgbotapi.Message{MessageID: 1}, nil)
				return m
			},
		},
		{
			name:   "callback goes to the callback router",
			update: tgbotapi.Update{UpdateID: 2, CallbackQuery: &tgbotapi.CallbackQuery{ID: "cb1", Data: cbCmdToday}},
			senderMock: func(ctrl *gomock.Controller) *mocks.MocktelegramSender {
				m := mocks.NewMocktelegramSender(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb1", msgMessageExpired))).Return(answered, nil)
				return m
			},
		},
		{
			name:       "update without a message or callback is ignored",
			update:     tgbotapi.Update{UpdateID: 3},
			senderMock: mocks.NewMocktelegramSender,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			b := New(
				tt.senderMock(ctrl),
				"TestBot",
				mocks.NewMockchatStore(ctrl),
				mocks.NewMocklcFetcher(ctrl),
				mocks.NewMocktaskScheduler(ctrl),
			)

			b.Handle(ctx, tt.update)
		})
	}
}
```

- [x] **Step 11: Run it to see it fail**

Run: `go test ./internal/bot/ -race -count=1 -run TestHandle`
Expected: `b.Handle undefined (type *tgBot has no field or method Handle)`, then `FAIL	github.com/solympe/leetcode-tg-notifier/internal/bot [build failed]`

- [x] **Step 12: Replace `Run` with `Handle` and drop `GetUpdatesChan` from the port**

In `internal/bot/bot.go`, replace the import block and `telegramSender` (lines 5-16)
```go
import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

type telegramSender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
	GetUpdatesChan(config tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel
}
```
with
```go
import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

type telegramSender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}
```
and replace `Run` (lines 61-68)
```go
func (b *tgBot) Run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)
	for update := range updates {
		b.handleMessage(update)
	}
}
```
with
```go
// Handle handles one update. The context is not used yet: the ports have no
// context parameter until the refactor threads one through.
func (b *tgBot) Handle(_ context.Context, u tgbotapi.Update) {
	b.handleMessage(u)
}
```

- [x] **Step 13: Regenerate the mocks**

Run: `PATH="$(go env GOPATH)/bin:$PATH" go generate ./... && git diff --stat -- internal/bot/mocks`
Expected:
```
 internal/bot/mocks/mock_deps.go | 14 --------------
 1 file changed, 14 deletions(-)
```
(the `GetUpdatesChan` mock and recorder methods are gone).

- [x] **Step 14: Move the polling loop into `main.go` until Task 3**

In `main.go`, change the import block's first lines from
```go
import (
	"log"
	"os"
```
to
```go
import (
	"context"
	"log"
	"os"
```
and replace `main.go:47`
```go
	b.Run()
```
with
```go
	for u := range api.GetUpdatesChan(tgbotapi.UpdateConfig{Timeout: 60}) {
		b.Handle(context.Background(), u)
	}
```
This is the same `getUpdates` request `Run` made (`NewUpdate(0)` with `Timeout = 60`).

- [x] **Step 15: Fix the stale comment**

In `internal/bot/setup_test.go:294`, replace
```go
// handleMessage, as Run does: /setup, a time button, a sticker and then a
```
with
```go
// handleMessage, as Handle does: /setup, a time button, a sticker and then a
```

- [x] **Step 16: Re-run the `Handle` test and build**

Run: `go test ./internal/bot/ -race -count=1 -run TestHandle && go build ./... && go vet ./...`
Expected: `ok  	github.com/solympe/leetcode-tg-notifier/internal/bot`, then no output.

- [x] **Step 17: Task gate**

Run: `go build ./... && go test ./... -race -count=1 && make lint`
Expected: `ok` for `internal/bot`, `internal/leetcode`, `internal/scheduler` and `internal/storage`, then `0 issues.`

- [x] **Step 18: Commit**

```bash
git add internal/leetcode/http.go internal/leetcode/http_test.go \
  internal/scheduler/cron.go internal/scheduler/cron_test.go \
  internal/bot/bot.go internal/bot/bot_test.go internal/bot/mocks/mock_deps.go internal/bot/setup_test.go \
  main.go
git commit -F - <<'EOF'
refactor: add endpoint, scheduler and Handle seams to the old code

- leetcode.NewHTTPClient takes the GraphQL endpoint; "" is LeetCode.
- The cron scheduler gains Start, Stop (waits for running jobs without
  holding its mutex), RunNow (runs the entry's WrappedJob after
  releasing the mutex) and Next, pinned by a new cron_test.go.
- The bot gains Handle(ctx, update); Run and GetUpdatesChan are gone
  and main.go polls until app takes over.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [x] **Step 19: Mocks check on the committed tree**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `CLEAN` and nothing else.

---

### Task 3: `internal/app` on the old wiring, and `main.go` (done: e15d891)

**Files:**
- Create: `internal/app/app.go`
- Modify: `main.go` (whole file, 48 lines to 25)

**Interfaces:** Consumes (Task 2): `leetcode.NewHTTPClient(endpoint string, c *http.Client) *httpClient`; `scheduler.NewCronScheduler(send SendFunc) *cronScheduler` with `Start()`, `Stop()`, `RunNow(chatID int64) bool`, `Next(chatID int64) (time.Time, bool)`, `Schedule(chatID int64, cfg storage.ChatConfig) error`; `bot.New(api telegramSender, botName string, store chatStore, lc lcFetcher, sched taskScheduler) *tgBot` with `SetScheduler(taskScheduler)`, `SendDailyProblem(chatID int64)`, `Handle(_ context.Context, u tgbotapi.Update)`; `storage.NewJSONStorage(path string) (*jsonStorage, error)` with `All() []ChatConfig`. tgbotapi v5.5.1 (checked in Step 1): `NewBotAPIWithClient(token, apiEndpoint string, client HTTPClient) (*BotAPI, error)`, `(*BotAPI).GetUpdatesChan(UpdateConfig) UpdatesChannel`, `StopReceivingUpdates()`, `GetUpdates(UpdateConfig) ([]Update, error)`, `APIEndpoint`.
Produces (in `package app`, for the Task 4-5 suite and Task 10):
- `type Config struct { Token, StoragePath, TelegramEndpoint, LeetCodeEndpoint string }`
- `func New(_ context.Context, cfg Config) (*application, error)`: restore runs inside `New`, so `a.sched.RunNow` and `a.sched.Next` are valid as soon as it returns.
- `func (a *application) Run(ctx context.Context)`, `func (a *application) handle(base context.Context, u tgbotapi.Update)`
- `type application struct { api *tgbotapi.BotAPI; sched jobRunner; bot updateHandler }`. Task 10 adds `cancelJobs context.CancelFunc` and `defer a.cancelJobs()` as the first line of `Run`.
- `type jobRunner interface { Start(); Stop(); RunNow(chatID int64) bool; Next(chatID int64) (time.Time, bool) }`, `type updateHandler interface { Handle(ctx context.Context, u tgbotapi.Update) }`
- `const telegramTimeout = 75 * time.Second`, `pollTimeout = 60`, `updateTimeout = 2 * time.Minute`

No unit tests for `internal/app` (spec §4.2). `go build`/`go vet` prove the wiring and that `*cronScheduler` satisfies `jobRunner` and `*tgBot` satisfies `updateHandler`. The integration suite (Task 4) is the first test of `app`.

- [x] **Step 1: Check the tgbotapi v5.5.1 API that `app` relies on**

Run:
```bash
M="$(go env GOMODCACHE)/github.com/go-telegram-bot-api/telegram-bot-api/v5@v5.5.1"
grep -n 'func NewBotAPIWithClient\|func (bot \*BotAPI) GetUpdates\|func (bot \*BotAPI) StopReceivingUpdates\|close(bot.shutdownChannel)\|case <-bot.shutdownChannel' "$M/bot.go"
grep -n 'APIEndpoint = ' "$M/configs.go"
grep -n -A 5 'func (config UpdateConfig) params' "$M/configs.go"
```
Expected:
```
55:func NewBotAPIWithClient(token, apiEndpoint string, client HTTPClient) (*BotAPI, error) {
404:func (bot *BotAPI) GetUpdates(config UpdateConfig) ([]Update, error) {
431:func (bot *BotAPI) GetUpdatesChan(config UpdateConfig) UpdatesChannel {
437:			case <-bot.shutdownChannel:
465:func (bot *BotAPI) StopReceivingUpdates() {
469:	close(bot.shutdownChannel)
16:	APIEndpoint = "https://api.telegram.org/bot%s/%s"
1146:func (config UpdateConfig) params() (Params, error) {
1147-	params := make(Params)
1148-
1149-	params.AddNonZero("offset", config.Offset)
1150-	params.AddNonZero("limit", config.Limit)
1151-	params.AddNonZero("timeout", config.Timeout)
```
What this establishes: `NewBotAPIWithClient` calls `getMe` and accepts our `*http.Client`. The poller checks `shutdownChannel` only between polls, so after `StopReceivingUpdates` it finishes the in-flight poll, pushes its updates and then closes the channel: `range` drains everything. `StopReceivingUpdates` is a bare `close`, so a second call panics (hence `context.AfterFunc`). The confirming `GetUpdates{Offset: last+1, Limit: 1}` sends no `timeout`, so it returns at once.

- [x] **Step 2: Create `internal/app/app.go`**

```go
package app

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/bot"
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/scheduler"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

// Config is everything the process needs from its environment. The endpoints
// exist for the integration suite; production leaves them empty.
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
	api   *tgbotapi.BotAPI // concrete third-party type: GetUpdatesChan, StopReceivingUpdates, GetUpdates
	sched jobRunner
	bot   updateHandler
}

// New builds the object graph and restores every stored schedule. It calls
// getMe, so it fails on a bad token or an unreachable Telegram endpoint.
func New(_ context.Context, cfg Config) (*application, error) {
	store, err := storage.NewJSONStorage(cmp.Or(cfg.StoragePath, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	api, err := tgbotapi.NewBotAPIWithClient(cfg.Token, cmp.Or(cfg.TelegramEndpoint, tgbotapi.APIEndpoint),
		&http.Client{Timeout: telegramTimeout})
	if err != nil {
		return nil, fmt.Errorf("NewBotAPI: %w", err)
	}
	log.Printf("Authorized as @%s", api.Self.UserName)

	b := bot.New(api, api.Self.UserName, store, leetcode.NewHTTPClient(cfg.LeetCodeEndpoint, nil), nil)
	sched := scheduler.NewCronScheduler(b.SendDailyProblem)
	b.SetScheduler(sched)
	for _, c := range store.All() {
		if err := sched.Schedule(c.ChatID, c); err != nil {
			log.Printf("restore schedule for %d: %v", c.ChatID, err)
		}
	}
	return &application{api: api, sched: sched, bot: b}, nil
}

// Run handles updates one at a time until ctx is done, then shuts down
// gracefully: it drains every update already received, confirms the last
// batch and waits for running jobs. Call it once per application.
func (a *application) Run(ctx context.Context) {
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

- [x] **Step 3: Build and vet the new package**

Run: `go build ./... && go vet ./internal/app/`
Expected: no output. (`main.go` still wires the old way; it compiles because `app` is not imported yet.)

- [x] **Step 4: Replace `main.go`**

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/solympe/leetcode-tg-notifier/internal/app"
)

func main() {
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("BOT_TOKEN environment variable is not set")
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

- [x] **Step 5: Task gate**

Run: `go build ./... && go vet ./... && go test ./... -race -count=1 && make lint`
Expected: `?   	github.com/solympe/leetcode-tg-notifier/internal/app	[no test files]`, `ok` for `internal/bot`, `internal/leetcode`, `internal/scheduler` and `internal/storage`, then `0 issues.` Spec §10 step 2's extra gate holds by construction: the only user-visible changes are D3(d) (75s client timeout) and D3(e) (graceful shutdown); texts, `config.json`, `BOT_TOKEN`/`STORAGE_PATH` and the `storage: …`, `NewBotAPI: …`, `Authorized as @…` log lines are unchanged.

- [x] **Step 6: Commit**

```bash
git add internal/app/app.go main.go
git commit -F - <<'EOF'
refactor(app): add composition root with graceful shutdown

internal/app owns wiring and the process lifecycle on today's packages:
the Telegram client gets a 75s timeout (D3 d), and SIGINT/SIGTERM drain
the buffered updates, confirm the last batch and wait for running jobs
(D3 e). Endpoints are configurable for the integration suite. main.go
only reads the environment and builds a signal-aware context.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [x] **Step 7: Mocks check on the committed tree**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `CLEAN` and nothing else.

**Review fix-ups after Task 3 (done: af23735, c3666fc).** Two commits landed on the branch after the text above was written. Task 4 starts from `c3666fc`:
- `main.go`: after `defer stop()`, `context.AfterFunc(ctx, func() { stop(); log.Print("shutting down; signal again to force") })` restores the default handler, so a second signal force-quits (spec §4.1 was updated to match).
- `internal/scheduler/cron_test.go`: a failed-reschedule row (`25:00`: a stale EntryID must not run) and a `Stop` cleanup with a time bound.
- `.golangci.yml`: revive gets `enable-default-rules: true` and disables `unexported-return`, which contradicts `New() *privateStruct`. Task 4 Step 10 carries both lines.

---

### Task 4: Integration harness, fakes and row 1

**Files:**
- Create: `internal/app/integration_test.go` (`TestMain` quiet-minute guard; `TestIntegration` with row 1)
- Create: `internal/app/faketelegram_test.go`
- Create: `internal/app/fakeleetcode_test.go`
- Create: `internal/app/harness_test.go`
- Modify: `.golangci.yml:3-4` (`run.build-tags: [integration]`)
- Modify: `Makefile` (whole file: `test-integration` target and its `.PHONY` entry)
- Modify: `.github/workflows/ci.yml` (append the `integration` job after line 45)

**Interfaces:** Consumes (Task 3, `package app`): `type Config struct { Token, StoragePath, TelegramEndpoint, LeetCodeEndpoint string }`; `func New(_ context.Context, cfg Config) (*application, error)` (restore runs inside `New`); `func (a *application) Run(ctx context.Context)` (drains, confirms with `GetUpdates{Offset: last+1, Limit: 1}`, then `sched.Stop()`). tgbotapi v5.5.1 sends `POST {endpoint}/bot{token}/{method}` form-encoded; `limit`, `offset` and `timeout` are sent only when non-zero; the confirming `GetUpdates` has no `timeout`.
Produces (test-only, `//go:build integration`, `package app`):
- `const testToken = "TEST:TOKEN"`, `pollCap = 100 * time.Millisecond`, `probeChat = 1`, `waitFor = 5 * time.Second`; `var botUser tgbotapi.User`, `var alice = tgbotapi.User{ID: 7, FirstName: "Alice"}`
- `type tgCall struct { method string; chatID int64; msgID int; text, parseMode string; kb *tgbotapi.InlineKeyboardMarkup; blocked bool }` with `String()`
- `func newFakeTelegram(t *testing.T) *fakeTelegram` (`srv *httptest.Server`; `say(chatID int64, from tgbotapi.User, text string)`, `next(chatID int64, wait time.Duration) (tgCall, bool)`, `pending(chatID int64) []tgCall`, `transcript(chatID int64) string`, `verify()`), and the helpers `chatOf`, `message`, `markupString`, `writeJSON`, `writeResult`
- `func newFakeLeetCode(t *testing.T) *fakeLeetCode` (`srv`; `var catalogue map[string]lcProblem`, one free problem per level; default daily 2026-09-28 Hard)
- `type button struct{ text, data string }`, `type keyboard [][]button` with `matches(*tgbotapi.InlineKeyboardMarkup) bool` and `String()`; an empty `text` matches any label, and a nil `keyboard` means no keyboard
- `func newEnv(t *testing.T) *env` (fields `t, dir, tg, lc, a *application, halt func()`); `(*env).start()`, `say(chatID int64, from tgbotapi.User, text string)`, `expectMessage(chatID int64) tgCall`, `sent(chatID int64, text string, kb keyboard) int`, `sync()`, `expectQuiet(chatID int64)`
- `const welcomeText`, `aboutText`; `var startKB keyboard`; `TestIntegration` table `[]struct{ name string; run func(e *env) }`, row `"01 menu"`
- `make test-integration`; the CI `Integration` job; golangci-lint lints tagged files.

`unused` (on by default in golangci-lint v2) rejects test helpers that nothing calls, so the helpers that only rows 2-16 use (`stop`, `restart`, `seed`, `stored`, `storedEq`, `notStored`, `fire`, `scheduledAt`, `notScheduled`, `press`, `pressOn`, `pressInline`, `block`, `edited`, `rekeyed`, `blockedAttempt`, `expectAnswer`, `answered`, the fake Bot API's `newest`, `callback`, `block` and `answer`, and the fake LeetCode's knobs) are appended in Task 5.

- [ ] **Step 1: Make the tracked files writable**

Run: `git ls-files -z | xargs -0 chmod u+w && test -z "$(git status --porcelain)" && echo CLEAN`
Expected: `CLEAN`. Every tracked file was read-only (`r--r--r--`), and git does not track the write bit.

- [ ] **Step 2: Write the suite entry point with row 1**

Create `internal/app/integration_test.go`:
```go
//go:build integration

package app

import (
	"os"
	"testing"
	"time"
)

// TestMain starts the suite early in a minute. The suite takes a few seconds,
// so it never crosses a cron minute boundary and real cron never fires a
// schedule mid-test: every firing goes through fire().
func TestMain(m *testing.M) {
	if s := time.Now().Second(); s > 45 {
		time.Sleep(time.Duration(61-s) * time.Second)
	}
	os.Exit(m.Run())
}

// Expected texts: literal copies of today's messages.
const (
	welcomeText = "Hi! I'm <b>TestBot</b>. I help you subscribe to a daily LeetCode challenge newsletter and get the problem of the day anytime.\n\nCommands:\n• /setup — create your daily subscription\n• /difficulty — choose problem difficulty\n• /today — get today's problem (your difficulty)\n• /daily — get the official LeetCode daily (any difficulty)\n• /rating — show solve leaderboard\n• /status — check your subscription status\n• /about — learn more about this bot"
	aboutText   = "For questions, suggestions, and bug reports — DM @solympe"
)

// Expected keyboards.
var startKB = keyboard{
	{{"📅 Today's problem", "/today"}, {"🗓 LeetCode daily", "/daily"}, {"⚙️ Setup", "/setup"}},
	{{"ℹ️ Status", "/status"}, {"🏆 Rating", "/rating"}},
	{{"🎚 Difficulty", "/difficulty"}, {"🛑 Unsubscribe", "/unsubscribe"}},
}

func TestIntegration(t *testing.T) {
	tests := []struct {
		name string
		run  func(e *env)
	}{
		{
			name: "01 menu",
			run: func(e *env) {
				e.say(100, alice, "/start")
				e.sent(100, welcomeText, startKB)
				e.say(100, alice, "/start@TestBot")
				e.sent(100, welcomeText, startKB)
				e.say(100, alice, "/about")
				e.sent(100, aboutText, nil)
				e.say(100, alice, "hello")
				e.expectQuiet(100)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.start()
			tt.run(e)
		})
	}
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test -tags integration -race -count=1 ./internal/app/`
Expected:
```
# github.com/solympe/leetcode-tg-notifier/internal/app [github.com/solympe/leetcode-tg-notifier/internal/app.test]
internal/app/integration_test.go:28:15: undefined: keyboard
internal/app/integration_test.go:29:2: missing type in composite literal
internal/app/integration_test.go:30:2: missing type in composite literal
internal/app/integration_test.go:31:2: missing type in composite literal
internal/app/integration_test.go:37:16: undefined: env
internal/app/integration_test.go:41:17: undefined: env
internal/app/integration_test.go:42:16: undefined: alice
internal/app/integration_test.go:44:16: undefined: alice
internal/app/integration_test.go:46:16: undefined: alice
internal/app/integration_test.go:48:16: undefined: alice
internal/app/integration_test.go:48:16: too many errors
FAIL	github.com/solympe/leetcode-tg-notifier/internal/app [build failed]
FAIL
```

- [ ] **Step 4: Create the fake Bot API**

Create `internal/app/faketelegram_test.go`:
```go
//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	testToken = "TEST:TOKEN"
	// pollCap is the longest a getUpdates with a timeout waits, so stopping
	// the app never waits for a real 60s long poll.
	pollCap = 100 * time.Millisecond
)

var botUser = tgbotapi.User{ID: 1, IsBot: true, FirstName: "Test", UserName: "TestBot"}

// tgCall is one recorded sendMessage, editMessageText or editMessageReplyMarkup.
type tgCall struct {
	method    string
	chatID    int64
	msgID     int // assigned by sendMessage, the target of an edit
	text      string
	parseMode string
	kb        *tgbotapi.InlineKeyboardMarkup // nil: the call had no reply_markup
	blocked   bool                           // a sendMessage rejected with 403
}

func (c tgCall) String() string {
	s := fmt.Sprintf("%s msg=%d parse_mode=%q text=%q", c.method, c.msgID, c.parseMode, c.text)
	if c.kb != nil {
		s += " kb=" + markupString(c.kb)
	}
	if c.blocked {
		s += " (403)"
	}
	return s
}

// shown is a bot message as the chat currently sees it.
type shown struct {
	text string
	kb   *tgbotapi.InlineKeyboardMarkup
}

// fakeTelegram is an in-process Bot API: POST /bot{token}/{method} with
// form-encoded parameters, as tgbotapi v5.5.1 sends them.
type fakeTelegram struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	changed  chan struct{} // closed and replaced on every state change
	queue    []tgbotapi.Update
	lastUpd  int
	lastMsg  map[int64]int // per-chat message_id counter
	shown    map[int64]map[int]shown
	calls    map[int64][]tgCall
	consumed map[int64]int
	blocked  map[int64]bool
	answers  map[string][]string // every issued cbID has an entry
	unknown  []string
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	f := &fakeTelegram{
		t:        t,
		changed:  make(chan struct{}),
		lastMsg:  make(map[int64]int),
		shown:    make(map[int64]map[int]shown),
		calls:    make(map[int64][]tgCall),
		consumed: make(map[int64]int),
		blocked:  make(map[int64]bool),
		answers:  make(map[string][]string),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTelegram) serve(w http.ResponseWriter, r *http.Request) {
	token, method, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/bot"), "/")
	if !ok || token != testToken {
		writeJSON(w, http.StatusUnauthorized, `{"ok":false,"error_code":401}`)
		return
	}
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("fake telegram: %s: parse form: %v", method, err)
	}
	switch method {
	case "getMe":
		writeJSON(w, http.StatusOK, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test","username":"TestBot"}}`)
	case "getUpdates":
		f.getUpdates(w, r)
	case "sendMessage":
		f.sendMessage(w, r)
	case "editMessageText", "editMessageReplyMarkup":
		f.edit(w, r, method)
	case "answerCallbackQuery":
		f.answerCallback(w, r)
	default:
		f.mu.Lock()
		f.unknown = append(f.unknown, method)
		f.mu.Unlock()
		f.t.Errorf("fake telegram: unexpected method %s %v", method, r.Form)
		writeJSON(w, http.StatusNotFound, `{"ok":false,"error_code":404,"description":"Not Found"}`)
	}
}

// getUpdates is at-least-once: it deletes the updates below offset, returns
// up to limit of the rest and, when there are none and timeout is set, waits
// up to pollCap for an enqueue. The confirming call has no timeout and
// returns at once.
func (f *fakeTelegram) getUpdates(w http.ResponseWriter, r *http.Request) {
	offset := f.formInt(r, "offset", 0)
	limit := f.formInt(r, "limit", 100)
	_, long := r.Form["timeout"]
	timer := time.NewTimer(pollCap)
	defer timer.Stop()
	for {
		f.mu.Lock()
		f.queue = slices.DeleteFunc(f.queue, func(u tgbotapi.Update) bool { return u.UpdateID < offset })
		if len(f.queue) > 0 || !long {
			batch := append([]tgbotapi.Update{}, f.queue[:min(limit, len(f.queue))]...)
			f.mu.Unlock()
			writeResult(w, batch)
			return
		}
		ch := f.changed
		f.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			writeResult(w, []tgbotapi.Update{})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (f *fakeTelegram) sendMessage(w http.ResponseWriter, r *http.Request) {
	c := f.parseCall(r, "sendMessage")
	f.mu.Lock()
	if f.blocked[c.chatID] {
		c.blocked = true
		f.recordLocked(c)
		f.mu.Unlock()
		writeJSON(w, http.StatusForbidden, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
		return
	}
	f.lastMsg[c.chatID]++
	c.msgID = f.lastMsg[c.chatID]
	f.messagesLocked(c.chatID)[c.msgID] = shown{text: c.text, kb: c.kb}
	f.recordLocked(c)
	f.mu.Unlock()
	writeResult(w, message(c.chatID, c.msgID, c.text, c.kb))
}

// edit applies editMessageText (a missing reply_markup removes the keyboard)
// or editMessageReplyMarkup and returns the edited Message, which tgbotapi's
// Send unmarshals.
func (f *fakeTelegram) edit(w http.ResponseWriter, r *http.Request, method string) {
	c := f.parseCall(r, method)
	f.mu.Lock()
	msgs := f.messagesLocked(c.chatID)
	cur, ok := msgs[c.msgID]
	if !ok {
		f.mu.Unlock()
		f.t.Errorf("fake telegram: %s of unknown message %d in chat %d", method, c.msgID, c.chatID)
		writeJSON(w, http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`)
		return
	}
	if method == "editMessageText" {
		cur.text = c.text
	}
	cur.kb = c.kb
	msgs[c.msgID] = cur
	f.recordLocked(c)
	f.mu.Unlock()
	writeResult(w, message(c.chatID, c.msgID, cur.text, cur.kb))
}

func (f *fakeTelegram) answerCallback(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("callback_query_id")
	f.mu.Lock()
	prev, issued := f.answers[id]
	if issued {
		f.answers[id] = append(prev, r.Form.Get("text"))
		f.broadcastLocked()
	}
	f.mu.Unlock()
	if !issued {
		f.t.Errorf("fake telegram: answer to unknown callback %q", id)
	}
	writeJSON(w, http.StatusOK, `{"ok":true,"result":true}`)
}

func (f *fakeTelegram) parseCall(r *http.Request, method string) tgCall {
	chatID, err := strconv.ParseInt(r.Form.Get("chat_id"), 10, 64)
	if err != nil {
		f.t.Errorf("fake telegram: %s: chat_id: %v", method, err)
	}
	c := tgCall{
		method:    method,
		chatID:    chatID,
		msgID:     f.formInt(r, "message_id", 0),
		text:      r.Form.Get("text"),
		parseMode: r.Form.Get("parse_mode"),
	}
	if raw := r.Form.Get("reply_markup"); raw != "" {
		var kb tgbotapi.InlineKeyboardMarkup
		if err := json.Unmarshal([]byte(raw), &kb); err != nil {
			f.t.Errorf("fake telegram: %s: reply_markup %q: %v", method, raw, err)
		}
		c.kb = &kb
	}
	return c
}

func (f *fakeTelegram) formInt(r *http.Request, key string, def int) int {
	s := r.Form.Get(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		f.t.Errorf("fake telegram: %s=%q: %v", key, s, err)
	}
	return n
}

func (f *fakeTelegram) messagesLocked(chatID int64) map[int]shown {
	m, ok := f.shown[chatID]
	if !ok {
		m = make(map[int]shown)
		f.shown[chatID] = m
	}
	return m
}

func (f *fakeTelegram) recordLocked(c tgCall) {
	f.calls[c.chatID] = append(f.calls[c.chatID], c)
	f.broadcastLocked()
}

func (f *fakeTelegram) broadcastLocked() {
	close(f.changed)
	f.changed = make(chan struct{})
}

func (f *fakeTelegram) enqueueLocked(u tgbotapi.Update) {
	f.lastUpd++
	u.UpdateID = f.lastUpd
	f.queue = append(f.queue, u)
	f.broadcastLocked()
}

// say enqueues a text message from user; a negative chatID is a supergroup.
func (f *fakeTelegram) say(chatID int64, from tgbotapi.User, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastMsg[chatID]++
	f.enqueueLocked(tgbotapi.Update{Message: &tgbotapi.Message{
		MessageID: f.lastMsg[chatID],
		From:      &from,
		Date:      int(time.Now().Unix()),
		Chat:      chatOf(chatID),
		Text:      text,
	}})
}

// next consumes the chat's next recorded call, waiting up to wait for it.
func (f *fakeTelegram) next(chatID int64, wait time.Duration) (tgCall, bool) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		f.mu.Lock()
		if i := f.consumed[chatID]; i < len(f.calls[chatID]) {
			f.consumed[chatID]++
			c := f.calls[chatID][i]
			f.mu.Unlock()
			return c, true
		}
		ch := f.changed
		f.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			return tgCall{}, false
		}
	}
}

// pending returns the chat's recorded calls no assertion has consumed.
func (f *fakeTelegram) pending(chatID int64) []tgCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls[chatID][f.consumed[chatID]:])
}

// transcript renders every call recorded for the chat, marking the consumed ones.
func (f *fakeTelegram) transcript(chatID int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sb strings.Builder
	fmt.Fprintf(&sb, "chat %d transcript (%d consumed):\n", chatID, f.consumed[chatID])
	for i, c := range f.calls[chatID] {
		mark := " "
		if i < f.consumed[chatID] {
			mark = "✓"
		}
		fmt.Fprintf(&sb, "  %s %d. %s\n", mark, i+1, c)
	}
	return sb.String()
}

// verify checks the strict invariants once the app has stopped.
func (f *fakeTelegram) verify() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range slices.Sorted(maps.Keys(f.answers)) {
		if got := f.answers[id]; len(got) != 1 {
			f.t.Errorf("invariant: callback %s answered %d times %q, want exactly once", id, len(got), got)
		}
	}
	for _, chatID := range slices.Sorted(maps.Keys(f.calls)) {
		calls := f.calls[chatID]
		for _, c := range calls {
			if (c.method == "sendMessage" || c.method == "editMessageText") && c.parseMode != "HTML" {
				f.t.Errorf("invariant: chat %d: %s without parse_mode=HTML", chatID, c)
			}
		}
		for _, c := range calls[f.consumed[chatID]:] {
			f.t.Errorf("invariant: chat %d: call never asserted: %s", chatID, c)
		}
	}
	if len(f.unknown) > 0 {
		f.t.Errorf("invariant: unknown methods called: %q", f.unknown)
	}
	if len(f.queue) > 0 {
		f.t.Errorf("invariant: %d updates never confirmed, first update_id %d", len(f.queue), f.queue[0].UpdateID)
	}
}

func chatOf(id int64) *tgbotapi.Chat {
	if id < 0 {
		return &tgbotapi.Chat{ID: id, Type: "supergroup", Title: "Test group"}
	}
	return &tgbotapi.Chat{ID: id, Type: "private"}
}

func message(chatID int64, msgID int, text string, kb *tgbotapi.InlineKeyboardMarkup) *tgbotapi.Message {
	from := botUser
	return &tgbotapi.Message{
		MessageID:   msgID,
		From:        &from,
		Date:        int(time.Now().Unix()),
		Chat:        chatOf(chatID),
		Text:        text,
		ReplyMarkup: kb,
	}
}

func markupString(kb *tgbotapi.InlineKeyboardMarkup) string {
	rows := make([]string, 0, len(kb.InlineKeyboard))
	for _, row := range kb.InlineKeyboard {
		buttons := make([]string, 0, len(row))
		for _, b := range row {
			data := "<nil>"
			if b.CallbackData != nil {
				data = *b.CallbackData
			}
			buttons = append(buttons, b.Text+"|"+data)
		}
		rows = append(rows, "["+strings.Join(buttons, ", ")+"]")
	}
	return strings.Join(rows, " ")
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func writeResult(w http.ResponseWriter, result any) {
	raw, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, `{"ok":true,"result":`+string(raw)+`}`)
}
```

- [ ] **Step 5: Create the fake LeetCode**

Create `internal/app/fakeleetcode_test.go`:
```go
//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// lcProblem is one catalogue entry of the fake LeetCode.
type lcProblem struct {
	id, title, slug, difficulty string
	tags                        []string
}

// catalogue holds exactly one free algorithms problem per level, keyed by the
// list query's upper-case difficulty filter, so a single-level random draw is
// deterministic under real math/rand.
var catalogue = map[string]lcProblem{
	"EASY":   {id: "1", title: "Two Sum", slug: "two-sum", difficulty: "Easy", tags: []string{"Array", "Hash Table"}},
	"MEDIUM": {id: "2", title: "Add Two Numbers", slug: "add-two-numbers", difficulty: "Medium", tags: []string{"Linked List", "Math", "Recursion"}},
	"HARD":   {id: "4", title: "Median of Two Sorted Arrays", slug: "median-of-two-sorted-arrays", difficulty: "Hard", tags: []string{"Array", "Binary Search", "Divide and Conquer"}},
}

// fakeLeetCode serves the two GraphQL queries the client sends: the active
// daily and the algorithms question list.
type fakeLeetCode struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	date       string
	daily      lcProblem
	dailyFails bool
	listFails  bool
	listDelay  time.Duration
	rotating   bool
	nDaily     int
	nList      int
	nRotated   int
}

func newFakeLeetCode(t *testing.T) *fakeLeetCode {
	f := &fakeLeetCode{t: t, date: "2026-09-28", daily: catalogue["HARD"]}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

type lcRequest struct {
	Query     string `json:"query"`
	Variables struct {
		CategorySlug string `json:"categorySlug"`
		Limit        int    `json:"limit"`
		Skip         int    `json:"skip"`
		Filters      struct {
			Difficulty string `json:"difficulty"`
		} `json:"filters"`
	} `json:"variables"`
}

func (f *fakeLeetCode) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
		f.t.Errorf("fake leetcode: unexpected %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	if got := r.Header.Get("Content-Type"); got != "application/json" {
		f.t.Errorf("fake leetcode: Content-Type %q, want application/json", got)
	}
	if got := r.Header.Get("User-Agent"); got != "Mozilla/5.0" {
		f.t.Errorf("fake leetcode: User-Agent %q, want Mozilla/5.0", got)
	}
	var req lcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("fake leetcode: decode body: %v", err)
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	switch {
	case strings.Contains(req.Query, "activeDailyCodingChallengeQuestion"):
		f.serveDaily(w)
	case strings.Contains(req.Query, "questionList("):
		f.serveList(w, r, req)
	default:
		f.t.Errorf("fake leetcode: unknown query %q", req.Query)
		http.Error(w, "unknown query", http.StatusBadRequest)
	}
}

func (f *fakeLeetCode) serveDaily(w http.ResponseWriter) {
	f.mu.Lock()
	f.nDaily++
	fail, date, p := f.dailyFails, f.date, f.daily
	f.mu.Unlock()
	if fail {
		http.Error(w, "outage", http.StatusInternalServerError)
		return
	}
	writeGraphQL(w, map[string]any{
		"activeDailyCodingChallengeQuestion": map[string]any{
			"date": date,
			"link": "/problems/" + p.slug + "/",
			"question": map[string]any{
				"title":              p.title,
				"frontendQuestionId": p.id,
				"difficulty":         p.difficulty,
				"topicTags":          topicTags(p.tags),
			},
		},
	})
}

func (f *fakeLeetCode) serveList(w http.ResponseWriter, r *http.Request, req lcRequest) {
	v := req.Variables
	if v.CategorySlug != "algorithms" {
		f.t.Errorf("fake leetcode: categorySlug %q, want algorithms", v.CategorySlug)
	}
	f.mu.Lock()
	f.nList++
	fail, delay, rotating := f.listFails, f.listDelay, f.rotating
	p, known := catalogue[v.Filters.Difficulty]
	if rotating {
		f.nRotated++
		n := f.nRotated
		p = lcProblem{
			id:         strconv.Itoa(1000 + n),
			title:      fmt.Sprintf("%s %d", p.title, n),
			slug:       fmt.Sprintf("%s-%d", p.slug, n),
			difficulty: p.difficulty,
			tags:       p.tags,
		}
	}
	f.mu.Unlock()

	select {
	case <-time.After(delay):
	case <-r.Context().Done():
		return
	}
	if fail {
		http.Error(w, "outage", http.StatusInternalServerError)
		return
	}
	if !known {
		f.t.Errorf("fake leetcode: unknown difficulty filter %q", v.Filters.Difficulty)
		http.Error(w, "unknown difficulty", http.StatusBadRequest)
		return
	}
	questions := []map[string]any{}
	if rotating || (v.Skip == 0 && v.Limit > 0) {
		questions = append(questions, map[string]any{
			"frontendQuestionId": p.id,
			"title":              p.title,
			"titleSlug":          p.slug,
			"difficulty":         p.difficulty,
			"paidOnly":           false,
			"topicTags":          topicTags(p.tags),
		})
	}
	writeGraphQL(w, map[string]any{
		"problemsetQuestionList": map[string]any{"total": 1, "questions": questions},
	})
}

func topicTags(names []string) []map[string]string {
	tags := make([]map[string]string, 0, len(names))
	for _, n := range names {
		tags = append(tags, map[string]string{"name": n})
	}
	return tags
}

func writeGraphQL(w http.ResponseWriter, data map[string]any) {
	raw, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}
```

- [ ] **Step 6: Create the harness**

Create `internal/app/harness_test.go`:
```go
//go:build integration

package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	probeChat = 1               // reserved for sync()
	waitFor   = 5 * time.Second // the longest any expectation waits
)

var alice = tgbotapi.User{ID: 7, FirstName: "Alice"}

// button is an expected inline button; an empty text matches any label.
type button struct{ text, data string }

// keyboard is an expected inline keyboard; nil means no keyboard.
type keyboard [][]button

func (k keyboard) matches(got *tgbotapi.InlineKeyboardMarkup) bool {
	if k == nil || got == nil {
		return k == nil && got == nil
	}
	if len(got.InlineKeyboard) != len(k) {
		return false
	}
	for i, row := range k {
		if len(got.InlineKeyboard[i]) != len(row) {
			return false
		}
		for j, want := range row {
			b := got.InlineKeyboard[i][j]
			if b.CallbackData == nil || *b.CallbackData != want.data || (want.text != "" && b.Text != want.text) {
				return false
			}
		}
	}
	return true
}

func (k keyboard) String() string {
	if k == nil {
		return "<none>"
	}
	rows := make([]string, 0, len(k))
	for _, row := range k {
		buttons := make([]string, 0, len(row))
		for _, b := range row {
			buttons = append(buttons, b.text+"|"+b.data)
		}
		rows = append(rows, "["+strings.Join(buttons, ", ")+"]")
	}
	return strings.Join(rows, " ")
}

// env is one hermetic bot: a temp dir, the two fakes and the application
// that main would build, started with a fresh ctx.
type env struct {
	t    *testing.T
	dir  string
	tg   *fakeTelegram
	lc   *fakeLeetCode
	a    *application // the last started app; kept after stop()
	halt func()       // stops the last started app, once
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, dir: t.TempDir(), tg: newFakeTelegram(t), lc: newFakeLeetCode(t)}
	// Registered after the servers' Close and before any start(), so cleanup
	// stops the app, then verifies, then closes the servers.
	t.Cleanup(func() {
		if !t.Failed() {
			e.tg.verify()
		}
	})
	return e
}

// start builds the app exactly as main does and runs it in the background.
// Restore runs inside New, so fire and scheduledAt are valid on return.
func (e *env) start() {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a, err := New(ctx, Config{
		Token:            testToken,
		StoragePath:      filepath.Join(e.dir, "config.json"),
		TelegramEndpoint: e.tg.srv.URL + "/bot%s/%s",
		LeetCodeEndpoint: e.lc.srv.URL + "/graphql",
	})
	if err != nil {
		cancel()
		e.t.Fatalf("New: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(ctx)
	}()
	var once sync.Once
	e.a = a
	e.halt = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(waitFor):
				e.t.Errorf("Run did not return within %v of cancel", waitFor)
			}
		})
	}
	e.t.Cleanup(e.halt)
}

// say sends a text message from user to the bot.
func (e *env) say(chatID int64, from tgbotapi.User, text string) {
	e.tg.say(chatID, from, text)
}

// expectMessage consumes the chat's next send or edit, in order.
func (e *env) expectMessage(chatID int64) tgCall {
	e.t.Helper()
	c, ok := e.tg.next(chatID, waitFor)
	if !ok {
		e.t.Fatalf("chat %d: no further message within %v\n%s", chatID, waitFor, e.tg.transcript(chatID))
	}
	return c
}

// sent expects a delivered sendMessage and returns its message_id.
func (e *env) sent(chatID int64, text string, kb keyboard) int {
	e.t.Helper()
	c := e.expectMessage(chatID)
	if c.method != "sendMessage" || c.blocked || c.text != text || !kb.matches(c.kb) {
		e.t.Fatalf("chat %d:\n got %s\nwant sendMessage text=%q kb=%s\n%s", chatID, c, text, kb, e.tg.transcript(chatID))
	}
	return c.msgID
}

// sync returns once every update sent so far is handled: the loop is
// sequential, so the reply to a later /about comes after all of them.
func (e *env) sync() {
	e.t.Helper()
	e.say(probeChat, alice, "/about")
	e.sent(probeChat, aboutText, nil)
}

// expectQuiet asserts that nothing beyond what was already asserted reached the chat.
func (e *env) expectQuiet(chatID int64) {
	e.t.Helper()
	e.sync()
	for _, c := range e.tg.pending(chatID) {
		e.t.Errorf("chat %d: unexpected %s", chatID, c)
	}
}
```

- [ ] **Step 7: Run row 1 against the old code**

Run: `go test -tags integration -race -count=1 -v ./internal/app/ 2>&1 | grep -E '^(\s*--- |ok|FAIL)'`
Expected:
```
--- PASS: TestIntegration (0.00s)
    --- PASS: TestIntegration/01_menu (0.12s)
ok  	github.com/solympe/leetcode-tg-notifier/internal/app	1.2s
```
(durations vary; when started after second 45 of a minute, `TestMain` first sleeps up to 16s.)

- [ ] **Step 8: Check that the untagged build skips the suite**

Run: `go test ./internal/app/`
Expected: `?   	github.com/solympe/leetcode-tg-notifier/internal/app	[no test files]`

- [ ] **Step 9: Prove the suite catches a text change, then revert it**

Run:
```bash
sed -i.bak 's/suggestions, and bug/suggestions and bug/' internal/bot/const.go && rm internal/bot/const.go.bak
go test -tags integration -race -count=1 ./internal/app/ 2>&1 | grep -E '^\s+(--- FAIL|got|want)|^FAIL'
git checkout internal/bot/const.go
```
Expected (before the checkout):
```
    --- FAIL: TestIntegration/01_menu (0.12s)
             got sendMessage msg=6 parse_mode="HTML" text="For questions, suggestions and bug reports — DM @solympe"
            want sendMessage text="For questions, suggestions, and bug reports — DM @solympe" kb=<none>
FAIL
FAIL	github.com/solympe/leetcode-tg-notifier/internal/app	0.4s
FAIL
```
then `Updated 1 path from the index`.

- [ ] **Step 10: Lint the tagged files**

Replace `.golangci.yml` with:
```yaml
version: "2"

run:
  timeout: 3m
  build-tags:
    - integration

linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - revive
    - misspell
  settings:
    revive:
      enable-default-rules: true # without it revive runs only the rules listed below
      rules:
        - name: unexported-return # CLAUDE.md: New() returns *privateStruct
          disabled: true
        - name: exported
          disabled: true
        - name: package-comments
          disabled: true
  exclusions:
    rules:
      - path: _test\.go
        linters:
          - errcheck

formatters:
  enable:
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/solympe/leetcode-tg-notifier
```

- [ ] **Step 11: Add `make test-integration`**

Replace `Makefile` with (recipe lines start with a tab):
```make
.PHONY: build test test-integration lint generate tidy

build:
	go build ./...

test:
	go test ./... -race -count=1

test-integration:
	go test -tags integration -race -count=1 -timeout 5m ./...

lint:
	golangci-lint run ./...

generate:
	PATH="$$(go env GOPATH)/bin:$$PATH" go generate ./...

tidy:
	go mod tidy
```

- [ ] **Step 12: Add the CI Integration job**

Replace `.github/workflows/ci.yml` with (`test`, `build` and `lint` are unchanged; `integration` is new):
```yaml
name: CI

on:
  push:
    branches: ["**"]
  pull_request:
    branches: ["**"]

jobs:
  test:
    name: Test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go test ./... -race -count=1

  build:
    name: Build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go build ./...

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

- [ ] **Step 13: Verify the config and lint**

Run: `golangci-lint config verify && make lint`
Expected: no output from verify, then `golangci-lint run ./...` and `0 issues.`

- [ ] **Step 14: Task gate**

Run:
```bash
go build ./... && go test ./... -race -count=1 && make lint && make test-integration
```
Expected: `go test` prints `[no test files]` for `internal/app` and `ok` for `internal/bot`, `internal/leetcode`, `internal/scheduler` and `internal/storage`; then `0 issues.`; then `make test-integration` prints `ok` for `internal/app` and the same four packages.

- [ ] **Step 15: Commit**

```bash
git add internal/app/faketelegram_test.go internal/app/fakeleetcode_test.go internal/app/harness_test.go \
  internal/app/integration_test.go .golangci.yml Makefile .github/workflows/ci.yml
git commit -F - <<'EOF'
test(app): add the integration harness, fakes and the menu scenario

A hermetic suite under //go:build integration boots the real object
graph (app.New + Run) against an in-process fake Bot API and fake
LeetCode GraphQL. Cleanup checks the strict invariants: every callback
answered exactly once, parse_mode=HTML on every send and text edit,
every recorded call asserted, no unknown method, the update queue fully
confirmed. TestMain starts the suite early in a minute so real cron
never fires mid-test.

make test-integration and a CI Integration job run it; golangci-lint
lints the tagged files.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [ ] **Step 16: Mocks check on the committed tree**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `CLEAN` and nothing else.

---

### Task 5: Characterization rows 2-16 and the storage golden fixture

**Files:**
- Modify: `internal/app/integration_test.go` (whole file: rows 2-16, the Review Focus rows 20-21 and `TestNewFails`, the `seed` column, texts, keyboards and scenario helpers)
- Modify: `internal/app/faketelegram_test.go:68` (field `lastCb`), append at end of file (`newest`, `callback`, `block`, `answer`, `hasButton`)
- Modify: `internal/app/fakeleetcode_test.go` (append the knobs at end of file)
- Modify: `internal/app/harness_test.go:5-11` (stdlib imports), `:21` (`bob`), append at end of file (legacy structs and the remaining helpers)
- Modify: `internal/storage/json_test.go:3-10` (imports), `:310-373` (`TestLoadLegacyConfig`, to the end of the file)
- Create: `internal/storage/testdata/legacy_config.json` (written by the old `jsonStorage`; never regenerated)

**Interfaces:** Consumes (Task 4): everything it produces; (Task 3) `a.sched.RunNow(chatID int64) bool` and `a.sched.Next(chatID int64) (time.Time, bool)` through the `jobRunner` field `sched`; (old storage) `storage.NewJSONStorage(path string) (*jsonStorage, error)`, `(*jsonStorage).Set(cfg ChatConfig) error`, `All() []ChatConfig`, and `ChatConfig`, `UserStat`, `DailyPick`.
Produces (test-only, `package app`; Task 11 inserts rows 17-19 between rows 16 and 20, and Task 10 must leave these files byte-identical):
- `var bob = tgbotapi.User{ID: 8, UserName: "bob"}`; `type legacyFile`, `legacyChat{ChatID int64; NotifyTime, Timezone string; Members map[string]legacyStat; Difficulties []string; DailyPick *legacyPick}`, `legacyStat{Name string; Count int; LastSolvedDate string}` and `legacyPick{Date, DailyDifficulty, ID, Title, Link, Difficulty string; Tags []string}`, which copy today's JSON tags
- `(*env)`: `stop()`, `restart()`, `seed(raw string)`, `stored(chatID int64) (legacyChat, bool)`, `storedEq(chatID int64, want legacyChat)`, `notStored(chatID int64)`, `fire(chatID int64) bool`, `scheduledAt(chatID int64, hhmm, zone string)`, `notScheduled(chatID int64)`, `press(chatID int64, from tgbotapi.User, data string) string`, `pressOn(chatID int64, from tgbotapi.User, msgID int, data string) string`, `pressInline(from tgbotapi.User, data string) string`, `block(chatID int64)`, `edited(chatID int64, msgID int, text string, kb keyboard)`, `rekeyed(chatID int64, msgID int, kb keyboard)`, `blockedAttempt(chatID int64) string`, `expectAnswer(cbID string) string`, `answered(cbID, want string)`, `subscribe(chatID int64, hhmm, zone string)`; `func asJSON(v any) string`
- `(*fakeLeetCode)`: `setDaily(date, difficulty string)`, `failDaily(fail bool)`, `failList(fail bool)`, `setListDelay(d time.Duration)`, `setRotating(on bool)`, `dailyCalls() int`, `listCalls() int`
- Texts: `menuExpiredText`, `messageExpiredText`, `notSubscribedText`, `disabledText`, `fetchFailedText`, `statusInactiveText`, `pickAtLeastOneText`, `chooseTimeText`, `chooseDifficultyText`, `invalidTimeText`, `invalidTzText`, `dailyText`, `randomEasyText`; keyboards `doneKB`, `timeKB`, `tzKB` (callback data only), `diffKB(ticked ...string) keyboard`; `tzPrompt(hhmm string) string`, `randomText(p *legacyPick) string`, `today() string`
- `TestIntegration` table `[]struct{ name, seed string; run func(e *env) }`, rows `"01 menu"` through `"16 shutdown drains exactly once"`, then `"20 restart mid-dialog expires its buttons, not Done"` and `"21 an unschedulable stored chat does not block the others"`
- `TestNewFails` (rows `"a wrong BOT_TOKEN"` and `"a STORAGE_PATH that is a directory"`): `New` fails with the `NewBotAPI: ` and `storage: load storage: ` prefixes
- `internal/storage/testdata/legacy_config.json` (1187 bytes, sha256 `cdeb9b96faadece072cd982cb90ce465ac95ee16bc506003021b2ff8dbbdc07e`): chats `-1001234567890` (members Alice 1 and `@bob` 4, `[Medium, Hard]`), `100` (Alice 5, `[Easy]`, flat Easy `daily_pick` Two Sum for 2026-09-28, daily Hard) and `200` (`members: null`, no `difficulties`); `TestLoadLegacyConfig` row `"golden file written by the pre-refactor storage"` compares `All()` sorted by `ChatID` to `want []ChatConfig`. Task 10 ports this row to `domain.Chat`.

Rows follow spec §8.6. Two precise choices: row 16 calls `sync()` before `say`, so the update races the shutdown of a poller that is already long-polling (Step 7 shows that the row fails without Run's confirming `GetUpdates`), and row 14 asserts `dailyCalls() == 3`, which keeps that knob (needed by row 19) in use. Rows 20-21 and `TestNewFails` are Review Focus pins 1-3. Numbers 17-19 are left for Task 11 (spec §8.6).

- [ ] **Step 1: Write rows 2-16, 20-21 and `TestNewFails`**

Replace `internal/app/integration_test.go` with:
```go
//go:build integration

package app

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TestMain starts the suite early in a minute. The suite takes a few seconds,
// so it never crosses a cron minute boundary and real cron never fires a
// schedule mid-test: every firing goes through fire().
func TestMain(m *testing.M) {
	if s := time.Now().Second(); s > 45 {
		time.Sleep(time.Duration(61-s) * time.Second)
	}
	os.Exit(m.Run())
}

// Expected texts: literal copies of today's messages.
const (
	welcomeText          = "Hi! I'm <b>TestBot</b>. I help you subscribe to a daily LeetCode challenge newsletter and get the problem of the day anytime.\n\nCommands:\n• /setup — create your daily subscription\n• /difficulty — choose problem difficulty\n• /today — get today's problem (your difficulty)\n• /daily — get the official LeetCode daily (any difficulty)\n• /rating — show solve leaderboard\n• /status — check your subscription status\n• /about — learn more about this bot"
	aboutText            = "For questions, suggestions, and bug reports — DM @solympe"
	chooseTimeText       = "Choose notification time or type your own (<b>HH:MM</b>, 24h):"
	chooseDifficultyText = "Choose difficulty (tap to toggle, then Save).\nIf today's daily doesn't match, I'll send a random problem of your level instead."
	invalidTimeText      = "⚠️ Invalid format. Please enter time as <b>HH:MM</b> (e.g. <code>09:00</code>):"
	invalidTzText        = "⚠️ Unknown timezone. Try again (e.g. <code>Europe/Moscow</code>):"
	pickAtLeastOneText   = "Pick at least one difficulty"
	menuExpiredText      = "⚠️ This menu is no longer active. Use /setup or /difficulty to start again."
	messageExpiredText   = "⚠️ This message is too old. Use /today to get a fresh one."
	notSubscribedText    = "⚠️ Use /setup to subscribe first."
	disabledText         = "🛑 Notifications disabled."
	fetchFailedText      = "⚠️ Failed to fetch the problem from LeetCode. Try /today later."
	statusInactiveText   = "❌ No active subscription. Use /setup to configure."

	dailyText      = "📅 LeetCode Daily — September 28, 2026\n\n🔢 4. Median of Two Sorted Arrays\n💪 Difficulty: Hard\n🏷 Array, Binary Search, Divide and Conquer\n\n🔗 https://leetcode.com/problems/median-of-two-sorted-arrays/"
	randomEasyText = "🎲 LeetCode Random — September 28, 2026\nToday's daily is Hard, so here's a random Easy problem for you.\n\n🔢 1. Two Sum\n💪 Difficulty: Easy\n🏷 Array, Hash Table\n\n🔗 https://leetcode.com/problems/two-sum/"
)

// Expected keyboards.
var (
	startKB = keyboard{
		{{"📅 Today's problem", "/today"}, {"🗓 LeetCode daily", "/daily"}, {"⚙️ Setup", "/setup"}},
		{{"ℹ️ Status", "/status"}, {"🏆 Rating", "/rating"}},
		{{"🎚 Difficulty", "/difficulty"}, {"🛑 Unsubscribe", "/unsubscribe"}},
	}
	doneKB = keyboard{{{"✅ Done", "done"}}}
	timeKB = keyboard{
		{{"7:00", "time:07:00"}, {"8:00", "time:08:00"}, {"9:00", "time:09:00"}, {"10:00", "time:10:00"}},
		{{"18:00", "time:18:00"}, {"19:00", "time:19:00"}, {"20:00", "time:20:00"}, {"21:00", "time:21:00"}},
	}
	// tzKB is compared by callback data only: its labels change with DST.
	tzKB = keyboard{
		{{"", "tz:America/New_York"}, {"", "tz:Europe/London"}},
		{{"", "tz:Europe/Lisbon"}, {"", "tz:UTC"}},
		{{"", "tz:Europe/Moscow"}, {"", "tz:Asia/Dubai"}},
		{{"", "tz:Asia/Bangkok"}},
	}
)

// diffKB is the difficulty keyboard with the given difficulties ticked.
func diffKB(ticked ...string) keyboard {
	row := make([]button, 0, 3)
	for _, d := range []string{"Easy", "Medium", "Hard"} {
		label := "⬜ " + d
		if slices.Contains(ticked, d) {
			label = "✅ " + d
		}
		row = append(row, button{label, "diff:" + d})
	}
	return keyboard{row, {{"💾 Save", "diffsave"}}}
}

// tzPrompt is the timezone step's text after hhmm was chosen.
func tzPrompt(hhmm string) string {
	return "Time: <b>" + hhmm + "</b>\n\nChoose your timezone or type it manually (e.g. <code>Europe/Moscow</code>)\nhttps://en.wikipedia.org/wiki/List_of_tz_database_time_zones"
}

// randomText is the text of a random pick replacing a daily.
func randomText(p *legacyPick) string {
	date, err := time.Parse(time.DateOnly, p.Date)
	if err != nil {
		return "unparseable pick date " + p.Date
	}
	return "🎲 LeetCode Random — " + date.Format("January 2, 2006") +
		"\nToday's daily is " + p.DailyDifficulty + ", so here's a random " + p.Difficulty + " problem for you.\n\n" +
		"🔢 " + p.ID + ". " + p.Title + "\n💪 Difficulty: " + p.Difficulty + "\n🏷 " + strings.Join(p.Tags, ", ") +
		"\n\n🔗 https://leetcode.com" + p.Link
}

// subscribe runs /setup by typing hhmm and zone and saves all three
// difficulties, asserting every reply. The chat must have no saved difficulties.
func (e *env) subscribe(chatID int64, hhmm, zone string) {
	e.t.Helper()
	e.say(chatID, alice, "/setup")
	m := e.sent(chatID, chooseTimeText, timeKB)
	e.say(chatID, alice, hhmm)
	e.edited(chatID, m, tzPrompt(hhmm), tzKB)
	e.say(chatID, alice, zone)
	e.edited(chatID, m, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
	e.answered(e.press(chatID, alice, "diffsave"), "")
	e.edited(chatID, m, "✅ All set! I'll send you the daily problem at <b>"+hhmm+"</b> ("+zone+")\nDifficulty: <b>Easy, Medium, Hard</b>", nil)
}

func today() string {
	return time.Now().UTC().Format(time.DateOnly)
}

func TestIntegration(t *testing.T) {
	tests := []struct {
		name string
		seed string // config.json written before start; "" = none
		run  func(e *env)
	}{
		{
			name: "01 menu",
			run: func(e *env) {
				e.say(100, alice, "/start")
				e.sent(100, welcomeText, startKB)
				e.say(100, alice, "/start@TestBot")
				e.sent(100, welcomeText, startKB)
				e.say(100, alice, "/about")
				e.sent(100, aboutText, nil)
				e.say(100, alice, "hello")
				e.expectQuiet(100)
			},
		},
		{
			name: "02 subscribe with buttons",
			run: func(e *env) {
				e.say(200, alice, "/start")
				e.sent(200, welcomeText, startKB)
				e.answered(e.press(200, alice, "/setup"), "")
				m := e.sent(200, chooseTimeText, timeKB)
				e.answered(e.press(200, alice, "time:09:00"), "")
				e.edited(200, m, tzPrompt("09:00"), tzKB)
				e.answered(e.press(200, alice, "tz:Europe/Moscow"), "")
				e.edited(200, m, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
				e.answered(e.press(200, alice, "diff:Hard"), "")
				e.rekeyed(200, m, diffKB("Easy", "Medium"))
				e.answered(e.press(200, alice, "diffsave"), "")
				e.edited(200, m, "✅ All set! I'll send you the daily problem at <b>09:00</b> (Europe/Moscow)\nDifficulty: <b>Easy, Medium</b>", nil)
				e.storedEq(200, legacyChat{ChatID: 200, NotifyTime: "09:00", Timezone: "Europe/Moscow", Difficulties: []string{"Easy", "Medium"}})
				e.scheduledAt(200, "09:00", "Europe/Moscow")
			},
		},
		{
			name: "03 subscribe by typing in a group",
			run: func(e *env) {
				e.say(-100500, alice, "/setup@TestBot")
				m := e.sent(-100500, chooseTimeText, timeKB)
				e.say(-100500, alice, "9:00")
				e.edited(-100500, m, invalidTimeText, timeKB)
				e.say(-100500, alice, "21:15")
				e.edited(-100500, m, tzPrompt("21:15"), tzKB)
				e.say(-100500, alice, "Local")
				e.edited(-100500, m, invalidTzText, tzKB)
				e.say(-100500, alice, "Mars/Base")
				e.edited(-100500, m, invalidTzText, tzKB)
				e.say(-100500, alice, "Asia/Tbilisi")
				e.edited(-100500, m, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
				e.answered(e.press(-100500, alice, "diffsave"), "")
				e.edited(-100500, m, "✅ All set! I'll send you the daily problem at <b>21:15</b> (Asia/Tbilisi)\nDifficulty: <b>Easy, Medium, Hard</b>", nil)
				e.storedEq(-100500, legacyChat{ChatID: -100500, NotifyTime: "21:15", Timezone: "Asia/Tbilisi", Difficulties: []string{"Easy", "Medium", "Hard"}})
				e.scheduledAt(-100500, "21:15", "Asia/Tbilisi")
			},
		},
		{
			name: "04 stale and forged buttons",
			run: func(e *env) {
				e.say(400, alice, "/setup")
				first := e.sent(400, chooseTimeText, timeKB)
				e.say(400, alice, "/setup")
				m := e.sent(400, chooseTimeText, timeKB)
				e.answered(e.pressOn(400, alice, first, "time:09:00"), menuExpiredText)
				e.answered(e.press(400, alice, "time:09:00"), "")
				e.edited(400, m, tzPrompt("09:00"), tzKB)
				e.answered(e.pressOn(400, alice, m, "tz:Local"), menuExpiredText)
				e.answered(e.pressOn(400, alice, m, "tz:Mars/Base"), menuExpiredText)
				e.answered(e.press(400, alice, "tz:UTC"), "")
				e.edited(400, m, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
				e.answered(e.pressOn(400, alice, m, "diff:Insane"), "")
				e.answered(e.press(400, alice, "diff:Easy"), "")
				e.rekeyed(400, m, diffKB("Medium", "Hard"))
				e.answered(e.press(400, alice, "diff:Medium"), "")
				e.rekeyed(400, m, diffKB("Hard"))
				e.answered(e.press(400, alice, "diff:Hard"), "")
				e.rekeyed(400, m, diffKB())
				e.answered(e.press(400, alice, "diffsave"), pickAtLeastOneText)
				e.answered(e.press(400, alice, "diff:Easy"), "")
				e.rekeyed(400, m, diffKB("Easy"))
				e.answered(e.press(400, alice, "diffsave"), "")
				e.edited(400, m, "✅ All set! I'll send you the daily problem at <b>09:00</b> (UTC)\nDifficulty: <b>Easy</b>", nil)
				e.answered(e.pressOn(400, alice, first, "time:07:00"), menuExpiredText)
				e.answered(e.pressInline(alice, "/today"), messageExpiredText)
				e.answered(e.pressOn(400, alice, m, "diffsave"), menuExpiredText)
				e.expectQuiet(400)
				e.storedEq(400, legacyChat{ChatID: 400, NotifyTime: "09:00", Timezone: "UTC", Difficulties: []string{"Easy"}})
			},
		},
		{
			name: "05 re-running setup keeps rating and pick",
			seed: `{"chats":{"500":{"chat_id":500,"notify_time":"08:00","timezone":"UTC",` +
				`"members":{"7":{"name":"Alice","count":3,"last_solved_date":"2026-09-27"}},"difficulties":["Easy"],` +
				`"daily_pick":{"date":"2026-09-28","daily_difficulty":"Hard","id":"1","title":"Two Sum","link":"/problems/two-sum/","difficulty":"Easy","tags":["Array","Hash Table"]}}}}`,
			run: func(e *env) {
				e.scheduledAt(500, "08:00", "UTC")
				e.say(500, alice, "/setup")
				m := e.sent(500, chooseTimeText, timeKB)
				e.answered(e.press(500, alice, "time:10:00"), "")
				e.edited(500, m, tzPrompt("10:00"), tzKB)
				e.answered(e.press(500, alice, "tz:Asia/Dubai"), "")
				e.edited(500, m, chooseDifficultyText, diffKB("Easy"))
				e.answered(e.press(500, alice, "diff:Medium"), "")
				e.rekeyed(500, m, diffKB("Easy", "Medium"))
				e.answered(e.press(500, alice, "diffsave"), "")
				e.edited(500, m, "✅ All set! I'll send you the daily problem at <b>10:00</b> (Asia/Dubai)\nDifficulty: <b>Easy, Medium</b>", nil)
				e.storedEq(500, legacyChat{
					ChatID:       500,
					NotifyTime:   "10:00",
					Timezone:     "Asia/Dubai",
					Members:      map[string]legacyStat{"7": {Name: "Alice", Count: 3, LastSolvedDate: "2026-09-27"}},
					Difficulties: []string{"Easy", "Medium"},
					DailyPick: &legacyPick{
						Date: "2026-09-28", DailyDifficulty: "Hard", ID: "1", Title: "Two Sum",
						Link: "/problems/two-sum/", Difficulty: "Easy", Tags: []string{"Array", "Hash Table"},
					},
				})
				e.scheduledAt(500, "10:00", "Asia/Dubai")
			},
		},
		{
			name: "06 scheduled daily, done and a single rating",
			seed: `{"chats":{"600":{"chat_id":600,"notify_time":"09:00","timezone":"UTC","members":null}}}`,
			run: func(e *env) {
				if !e.fire(600) {
					e.t.Fatal("fire(600): nothing scheduled")
				}
				e.sent(600, dailyText, doneKB)
				e.answered(e.press(600, alice, "done"), "✅ Counted! Your total: 1")
				e.answered(e.press(600, alice, "done"), "Already counted today!")
				e.say(600, alice, "/rating")
				e.sent(600, "🏆 Solved: <b>1</b>", nil)
				e.storedEq(600, legacyChat{
					ChatID:     600,
					NotifyTime: "09:00",
					Timezone:   "UTC",
					Members:    map[string]legacyStat{"7": {Name: "Alice", Count: 1, LastSolvedDate: today()}},
				})
			},
		},
		{
			name: "07 group rating without ties",
			seed: `{"chats":{"-100123":{"chat_id":-100123,"notify_time":"09:00","timezone":"UTC",` +
				`"members":{"7":{"name":"Alice","count":2,"last_solved_date":"2026-09-01"}}}}}`,
			run: func(e *env) {
				e.say(-100123, alice, "/today@TestBot")
				e.sent(-100123, dailyText, doneKB)
				e.answered(e.press(-100123, alice, "done"), "✅ Counted! Your total: 3")
				e.answered(e.press(-100123, bob, "done"), "✅ Counted! Your total: 1")
				e.say(-100123, alice, "/rating")
				e.sent(-100123, "🏆 <b>Rating</b>\n\n🥇 Alice — 3\n🥈 @bob — 1\n", nil)
				e.storedEq(-100123, legacyChat{
					ChatID:     -100123,
					NotifyTime: "09:00",
					Timezone:   "UTC",
					Members: map[string]legacyStat{
						"7": {Name: "Alice", Count: 3, LastSolvedDate: today()},
						"8": {Name: "@bob", Count: 1, LastSolvedDate: today()},
					},
				})
			},
		},
		{
			name: "08 difficulty leads to a random pick repeated all day",
			run: func(e *env) {
				e.subscribe(800, "09:00", "UTC")
				e.say(800, alice, "/difficulty")
				d := e.sent(800, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
				e.answered(e.press(800, alice, "diff:Medium"), "")
				e.rekeyed(800, d, diffKB("Easy", "Hard"))
				e.answered(e.press(800, alice, "diff:Hard"), "")
				e.rekeyed(800, d, diffKB("Easy"))
				e.answered(e.press(800, alice, "diffsave"), "")
				e.edited(800, d, "✅ Difficulty updated: <b>Easy</b>", nil)

				if !e.fire(800) {
					e.t.Fatal("fire(800): nothing scheduled")
				}
				e.sent(800, randomEasyText, doneKB)
				pick := legacyPick{
					Date: "2026-09-28", DailyDifficulty: "Hard", ID: "1", Title: "Two Sum",
					Link: "/problems/two-sum/", Difficulty: "Easy", Tags: []string{"Array", "Hash Table"},
				}
				e.storedEq(800, legacyChat{ChatID: 800, NotifyTime: "09:00", Timezone: "UTC", Difficulties: []string{"Easy"}, DailyPick: &pick})
				lists := e.lc.listCalls()

				e.say(800, alice, "/today")
				e.sent(800, randomEasyText, doneKB)
				e.say(800, alice, "/start")
				e.sent(800, welcomeText, startKB)
				e.answered(e.press(800, alice, "/today"), "")
				e.sent(800, randomEasyText, doneKB)
				if got := e.lc.listCalls(); got != lists {
					e.t.Errorf("list calls: got %d, want %d (the pick is resent, not fetched again)", got, lists)
				}

				e.lc.setDaily("2026-09-29", "Hard")
				if !e.fire(800) {
					e.t.Fatal("fire(800): nothing scheduled")
				}
				e.sent(800, "🎲 LeetCode Random — September 29, 2026\nToday's daily is Hard, so here's a random Easy problem for you.\n\n🔢 1. Two Sum\n💪 Difficulty: Easy\n🏷 Array, Hash Table\n\n🔗 https://leetcode.com/problems/two-sum/", doneKB)
				if got := e.lc.listCalls(); got <= lists {
					e.t.Errorf("list calls: got %d, want more than %d (a new day makes a new pick)", got, lists)
				}
				pick.Date = "2026-09-29"
				e.storedEq(800, legacyChat{ChatID: 800, NotifyTime: "09:00", Timezone: "UTC", Difficulties: []string{"Easy"}, DailyPick: &pick})
			},
		},
		{
			name: "09 concurrent sends agree",
			seed: `{"chats":{"900":{"chat_id":900,"notify_time":"09:00","timezone":"UTC","members":null,"difficulties":["Easy"]}}}`,
			run: func(e *env) {
				e.lc.setRotating(true)
				e.lc.setListDelay(200 * time.Millisecond)
				fired := make(chan bool, 1)
				go func() { fired <- e.fire(900) }()
				e.say(900, alice, "/today")
				first, second := e.expectMessage(900), e.expectMessage(900)
				select {
				case ok := <-fired:
					if !ok {
						e.t.Fatal("fire(900): nothing scheduled")
					}
				case <-time.After(waitFor):
					e.t.Fatal("fire(900) did not return")
				}
				got, ok := e.stored(900)
				if !ok || got.DailyPick == nil {
					e.t.Fatalf("chat 900: no daily_pick stored: %s", asJSON(got))
				}
				want := randomText(got.DailyPick)
				for _, c := range []tgCall{first, second} {
					if c.method != "sendMessage" || c.blocked || c.text != want || !doneKB.matches(c.kb) {
						e.t.Errorf("chat 900:\n got %s\nwant sendMessage text=%q kb=%s", c, want, doneKB)
					}
				}
			},
		},
		{
			name: "10 daily ignores subscription and pick",
			seed: `{"chats":{"1001":{"chat_id":1001,"notify_time":"09:00","timezone":"UTC","members":null,"difficulties":["Easy"]}}}`,
			run: func(e *env) {
				e.say(1000, alice, "/daily")
				e.sent(1000, dailyText, doneKB)
				e.say(1001, alice, "/daily")
				e.sent(1001, dailyText, doneKB)
				e.say(1001, alice, "/start")
				e.sent(1001, welcomeText, startKB)
				e.answered(e.press(1001, alice, "/daily"), "")
				e.sent(1001, dailyText, doneKB)
				e.notStored(1000)
				e.storedEq(1001, legacyChat{ChatID: 1001, NotifyTime: "09:00", Timezone: "UTC", Difficulties: []string{"Easy"}})
				if got := e.lc.listCalls(); got != 0 {
					e.t.Errorf("list calls: got %d, want 0", got)
				}
			},
		},
		{
			name: "11 status and not-subscribed paths",
			seed: `{"chats":{"1101":{"chat_id":1101,"notify_time":"07:00","timezone":"UTC","members":null}}}`,
			run: func(e *env) {
				e.say(1100, alice, "/status")
				e.sent(1100, statusInactiveText, nil)
				e.subscribe(1100, "09:00", "Europe/Moscow")
				e.say(1100, alice, "/status")
				e.sent(1100, "✅ Subscription active\nTime: <b>09:00</b> (Europe/Moscow)\nDifficulty: <b>Easy, Medium, Hard</b>", nil)
				e.say(1101, alice, "/status")
				e.sent(1101, "✅ Subscription active\nTime: <b>07:00</b> (UTC)\nDifficulty: <b>Any</b>", nil)

				e.say(1102, alice, "/difficulty")
				e.sent(1102, notSubscribedText, nil)
				e.say(1102, alice, "/daily")
				e.sent(1102, dailyText, doneKB)
				e.answered(e.press(1102, alice, "done"), notSubscribedText)

				e.say(1100, alice, "/difficulty")
				d := e.sent(1100, chooseDifficultyText, diffKB("Easy", "Medium", "Hard"))
				e.say(1100, alice, "/unsubscribe")
				e.sent(1100, disabledText, nil)
				e.answered(e.press(1100, alice, "diffsave"), "")
				e.edited(1100, d, notSubscribedText, nil)
				e.notStored(1100)
				e.notStored(1102)
			},
		},
		{
			name: "12 unsubscribe stops sends",
			run: func(e *env) {
				e.subscribe(1200, "09:00", "UTC")
				e.say(1200, alice, "/start")
				e.sent(1200, welcomeText, startKB)
				e.answered(e.press(1200, alice, "/unsubscribe"), "")
				e.sent(1200, disabledText, nil)
				if e.fire(1200) {
					e.t.Error("fire(1200): still scheduled after unsubscribe")
				}
				e.notScheduled(1200)
				e.notStored(1200)
				e.say(1200, alice, "/status")
				e.sent(1200, statusInactiveText, nil)
			},
		},
		{
			name: "13 blocked bot auto-unsubscribes",
			run: func(e *env) {
				e.subscribe(1300, "09:00", "UTC")
				e.block(1300)
				if !e.fire(1300) {
					e.t.Fatal("fire(1300): nothing scheduled")
				}
				if got := e.blockedAttempt(1300); got != dailyText {
					e.t.Errorf("blocked attempt text: got %q, want %q", got, dailyText)
				}
				e.notStored(1300)
				if e.fire(1300) {
					e.t.Error("fire(1300): still scheduled after the 403")
				}
				e.notScheduled(1300)
			},
		},
		{
			name: "14 leetcode outage",
			seed: `{"chats":{"1400":{"chat_id":1400,"notify_time":"09:00","timezone":"UTC","members":null},` +
				`"1401":{"chat_id":1401,"notify_time":"10:00","timezone":"UTC","members":null,"difficulties":["Easy"]}}}`,
			run: func(e *env) {
				e.lc.failDaily(true)
				e.say(1400, alice, "/today")
				e.sent(1400, fetchFailedText, nil)
				if !e.fire(1400) {
					e.t.Fatal("fire(1400): nothing scheduled")
				}
				e.sent(1400, fetchFailedText, nil)
				e.say(1400, alice, "/daily")
				e.sent(1400, fetchFailedText, nil)
				if got := e.lc.dailyCalls(); got != 3 {
					e.t.Errorf("daily calls: got %d, want 3 (one per attempt)", got)
				}
				e.storedEq(1400, legacyChat{ChatID: 1400, NotifyTime: "09:00", Timezone: "UTC"})
				e.scheduledAt(1400, "09:00", "UTC")

				e.lc.failDaily(false)
				e.lc.failList(true)
				e.say(1401, alice, "/today")
				e.sent(1401, fetchFailedText, nil)
				if !e.fire(1401) {
					e.t.Fatal("fire(1401): nothing scheduled")
				}
				e.sent(1401, fetchFailedText, nil)
				e.storedEq(1401, legacyChat{ChatID: 1401, NotifyTime: "10:00", Timezone: "UTC", Difficulties: []string{"Easy"}})
				e.scheduledAt(1401, "10:00", "UTC")
			},
		},
		{
			name: "15 restart restores schedules",
			seed: `{
  "chats": {
    "1500": {
      "chat_id": 1500,
      "notify_time": "09:30",
      "timezone": "Europe/Moscow",
      "members": {
        "7": {"name": "Alice", "count": 5, "last_solved_date": "2026-09-27"},
        "8": {"name": "@bob", "count": 2, "last_solved_date": "2026-09-27"}
      },
      "difficulties": ["Easy"],
      "daily_pick": {
        "date": "2026-09-28",
        "daily_difficulty": "Hard",
        "id": "9",
        "title": "Palindrome Number",
        "link": "/problems/palindrome-number/",
        "difficulty": "Easy",
        "tags": ["Math"]
      }
    },
    "1501": {
      "chat_id": 1501,
      "notify_time": "07:00",
      "timezone": "Asia/Dubai",
      "members": null
    }
  }
}`,
			run: func(e *env) {
				seeded := legacyChat{
					ChatID:     1500,
					NotifyTime: "09:30",
					Timezone:   "Europe/Moscow",
					Members: map[string]legacyStat{
						"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-27"},
						"8": {Name: "@bob", Count: 2, LastSolvedDate: "2026-09-27"},
					},
					Difficulties: []string{"Easy"},
					DailyPick: &legacyPick{
						Date: "2026-09-28", DailyDifficulty: "Hard", ID: "9", Title: "Palindrome Number",
						Link: "/problems/palindrome-number/", Difficulty: "Easy", Tags: []string{"Math"},
					},
				}
				legacy := legacyChat{ChatID: 1501, NotifyTime: "07:00", Timezone: "Asia/Dubai"}
				e.scheduledAt(1500, "09:30", "Europe/Moscow")
				e.scheduledAt(1501, "07:00", "Asia/Dubai")

				if !e.fire(1500) {
					e.t.Fatal("fire(1500): nothing scheduled")
				}
				e.sent(1500, "🎲 LeetCode Random — September 28, 2026\nToday's daily is Hard, so here's a random Easy problem for you.\n\n🔢 9. Palindrome Number\n💪 Difficulty: Easy\n🏷 Math\n\n🔗 https://leetcode.com/problems/palindrome-number/", doneKB)
				if got := e.lc.listCalls(); got != 0 {
					e.t.Errorf("list calls: got %d, want 0 (the seeded pick is resent)", got)
				}
				if !e.fire(1501) {
					e.t.Fatal("fire(1501): nothing scheduled")
				}
				e.sent(1501, dailyText, doneKB)
				e.say(1500, alice, "/rating")
				e.sent(1500, "🏆 <b>Rating</b>\n\n🥇 Alice — 5\n🥈 @bob — 2\n", nil)

				e.subscribe(1502, "18:30", "Asia/Bangkok")
				e.restart()
				e.scheduledAt(1500, "09:30", "Europe/Moscow")
				e.scheduledAt(1501, "07:00", "Asia/Dubai")
				e.scheduledAt(1502, "18:30", "Asia/Bangkok")
				if !e.fire(1502) {
					e.t.Fatal("fire(1502): nothing scheduled after restart")
				}
				e.sent(1502, dailyText, doneKB)
				e.storedEq(1500, seeded)
				e.storedEq(1501, legacy)
				e.storedEq(1502, legacyChat{ChatID: 1502, NotifyTime: "18:30", Timezone: "Asia/Bangkok", Difficulties: []string{"Easy", "Medium", "Hard"}})
			},
		},
		{
			name: "16 shutdown drains exactly once",
			run: func(e *env) {
				e.sync() // the poller is up and in a long poll
				e.say(1600, alice, "/about")
				e.stop()
				e.start()
				e.sent(1600, aboutText, nil)
				e.expectQuiet(1600)
			},
		},
		{
			// Review focus: a deploy mid-dialog. The sessions die with the old
			// process, so every dialog button on a prompt sent before the
			// restart has expired and typed input is plain text again, while
			// the buttons that need no session keep working.
			name: "20 restart mid-dialog expires its buttons, not Done",
			seed: `{"chats":{"2000":{"chat_id":2000,"notify_time":"08:00","timezone":"UTC","members":null,"difficulties":["Hard"]}}}`,
			run: func(e *env) {
				e.say(2000, alice, "/start")
				menu := e.sent(2000, welcomeText, startKB)
				e.say(2000, alice, "/today")
				problem := e.sent(2000, dailyText, doneKB)
				e.say(2000, alice, "/difficulty")
				d := e.sent(2000, chooseDifficultyText, diffKB("Hard"))
				e.say(2001, alice, "/setup")
				s := e.sent(2001, chooseTimeText, timeKB)
				e.answered(e.press(2001, alice, "time:09:00"), "")
				e.edited(2001, s, tzPrompt("09:00"), tzKB)

				e.restart()

				e.answered(e.pressOn(2001, alice, s, "tz:Europe/Moscow"), menuExpiredText)
				e.say(2001, alice, "Europe/Moscow")
				e.expectQuiet(2001)
				e.notStored(2001)
				e.answered(e.pressOn(2000, alice, d, "diff:Easy"), menuExpiredText)
				e.answered(e.pressOn(2000, alice, d, "diffsave"), menuExpiredText)
				e.answered(e.pressOn(2000, alice, problem, "done"), "✅ Counted! Your total: 1")
				e.answered(e.pressOn(2000, alice, menu, "/today"), "")
				e.sent(2000, dailyText, doneKB)
				e.expectQuiet(2000)
				e.storedEq(2000, legacyChat{
					ChatID:       2000,
					NotifyTime:   "08:00",
					Timezone:     "UTC",
					Members:      map[string]legacyStat{"7": {Name: "Alice", Count: 1, LastSolvedDate: today()}},
					Difficulties: []string{"Hard"},
				})
			},
		},
		{
			// Review focus: one stored chat that no longer schedules (a
			// hand-edited time, a zone gone from tzdata) is logged at restore;
			// startup and every other chat carry on, and nothing is deleted.
			name: "21 an unschedulable stored chat does not block the others",
			seed: `{"chats":{` +
				`"2100":{"chat_id":2100,"notify_time":"0900","timezone":"UTC","members":null},` +
				`"2101":{"chat_id":2101,"notify_time":"09:00","timezone":"Mars/Base","members":null},` +
				`"2102":{"chat_id":2102,"notify_time":"07:00","timezone":"Asia/Dubai","members":null}}}`,
			run: func(e *env) {
				e.notScheduled(2100)
				e.notScheduled(2101)
				e.scheduledAt(2102, "07:00", "Asia/Dubai")
				if !e.fire(2102) {
					e.t.Fatal("fire(2102): nothing scheduled")
				}
				e.sent(2102, dailyText, doneKB)
				e.storedEq(2100, legacyChat{ChatID: 2100, NotifyTime: "0900", Timezone: "UTC"})
				e.storedEq(2101, legacyChat{ChatID: 2101, NotifyTime: "09:00", Timezone: "Mars/Base"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			if tt.seed != "" {
				e.seed(tt.seed)
			}
			e.start()
			tt.run(e)
		})
	}
}

// TestNewFails pins the two startup errors an operator sees before main
// exits (spec §11): New returns them wrapped with today's prefixes.
func TestNewFails(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name       string
		cfg        func(tg *fakeTelegram, dir string) Config
		wantPrefix string
		wantCode   int // the Bot API error code; 0 = not a Bot API error
	}{
		{
			name: "a wrong BOT_TOKEN",
			cfg: func(tg *fakeTelegram, dir string) Config {
				return Config{Token: "WRONG:TOKEN", StoragePath: filepath.Join(dir, "config.json"), TelegramEndpoint: tg.srv.URL + "/bot%s/%s"}
			},
			wantPrefix: "NewBotAPI: ",
			wantCode:   http.StatusUnauthorized,
		},
		{
			name: "a STORAGE_PATH that is a directory",
			cfg: func(tg *fakeTelegram, dir string) Config {
				return Config{Token: testToken, StoragePath: dir, TelegramEndpoint: tg.srv.URL + "/bot%s/%s"}
			},
			wantPrefix: "storage: load storage: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := New(ctx, tt.cfg(newFakeTelegram(t), t.TempDir()))
			if err == nil {
				a.sched.Stop()
				t.Fatal("New: got nil error")
			}
			if !strings.HasPrefix(err.Error(), tt.wantPrefix) {
				t.Errorf("New: got %q, want the prefix %q", err, tt.wantPrefix)
			}
			gotCode := 0
			var tgErr *tgbotapi.Error
			if errors.As(err, &tgErr) {
				gotCode = tgErr.Code
			}
			if gotCode != tt.wantCode {
				t.Errorf("New: Bot API error code %d, want %d (%v)", gotCode, tt.wantCode, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test -tags integration -race -count=1 ./internal/app/`
Expected:
```
# github.com/solympe/leetcode-tg-notifier/internal/app [github.com/solympe/leetcode-tg-notifier/internal/app.test]
internal/app/integration_test.go:88:20: undefined: legacyPick
internal/app/integration_test.go:106:4: e.edited undefined (type *env has no field or method edited)
internal/app/integration_test.go:108:4: e.edited undefined (type *env has no field or method edited)
internal/app/integration_test.go:109:4: e.answered undefined (type *env has no field or method answered)
internal/app/integration_test.go:109:15: e.press undefined (type *env has no field or method press)
internal/app/integration_test.go:110:4: e.edited undefined (type *env has no field or method edited)
internal/app/integration_test.go:141:7: e.answered undefined (type *env has no field or method answered)
internal/app/integration_test.go:141:18: e.press undefined (type *env has no field or method press)
internal/app/integration_test.go:143:7: e.answered undefined (type *env has no field or method answered)
internal/app/integration_test.go:143:18: e.press undefined (type *env has no field or method press)
internal/app/integration_test.go:143:18: too many errors
FAIL	github.com/solympe/leetcode-tg-notifier/internal/app [build failed]
FAIL
```

- [ ] **Step 3: Give the fake Bot API button presses and blocking**

In `internal/app/faketelegram_test.go`, in `type fakeTelegram struct`, replace
```go
	lastUpd  int
```
with
```go
	lastUpd  int
	lastCb   int
```
and append to the end of the file:
```go

// newest returns the newest bot message in the chat whose current keyboard
// has a button with that callback data.
func (f *fakeTelegram) newest(chatID int64, data string) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs := f.shown[chatID]
	for _, id := range slices.Backward(slices.Sorted(maps.Keys(msgs))) {
		if hasButton(msgs[id].kb, data) {
			return id, true
		}
	}
	return 0, false
}

// callback enqueues a button press on message msgID of the chat, or an
// inline one (Message == nil) when inline is true, and returns its ID.
func (f *fakeTelegram) callback(chatID int64, from tgbotapi.User, msgID int, inline bool, data string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCb++
	id := "cb" + strconv.Itoa(f.lastCb)
	f.answers[id] = nil
	cb := &tgbotapi.CallbackQuery{ID: id, From: &from, ChatInstance: "instance", Data: data}
	if inline {
		cb.InlineMessageID = "inline-" + id
	} else {
		cur := f.shown[chatID][msgID]
		cb.Message = message(chatID, msgID, cur.text, cur.kb)
	}
	f.enqueueLocked(tgbotapi.Update{CallbackQuery: cb})
	return id
}

func (f *fakeTelegram) block(chatID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocked[chatID] = true
}

// answer returns the first answer to cbID, waiting up to wait for it.
func (f *fakeTelegram) answer(cbID string, wait time.Duration) (string, bool) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		f.mu.Lock()
		if got := f.answers[cbID]; len(got) > 0 {
			f.mu.Unlock()
			return got[0], true
		}
		ch := f.changed
		f.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			return "", false
		}
	}
}

func hasButton(kb *tgbotapi.InlineKeyboardMarkup, data string) bool {
	if kb == nil {
		return false
	}
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData != nil && *b.CallbackData == data {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 4: Give the fake LeetCode its knobs**

Append to the end of `internal/app/fakeleetcode_test.go`:
```go

// setDaily makes the catalogue problem of difficulty the daily of date.
func (f *fakeLeetCode) setDaily(date, difficulty string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.date, f.daily = date, catalogue[strings.ToUpper(difficulty)]
}

// failDaily makes daily queries answer 500.
func (f *fakeLeetCode) failDaily(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dailyFails = fail
}

// failList makes list queries answer 500.
func (f *fakeLeetCode) failList(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listFails = fail
}

// setListDelay delays every list response by d.
func (f *fakeLeetCode) setListDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listDelay = d
}

// setRotating makes every list response return a new free problem of the
// requested level, whatever skip is.
func (f *fakeLeetCode) setRotating(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rotating = on
}

func (f *fakeLeetCode) dailyCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nDaily
}

func (f *fakeLeetCode) listCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nList
}
```

- [ ] **Step 5: Complete the harness**

In `internal/app/harness_test.go`, replace the first lines of the import block
```go
import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
```
with
```go
import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
```
(the blank line and the `tgbotapi` import after them stay), replace
```go
var alice = tgbotapi.User{ID: 7, FirstName: "Alice"}
```
with
```go
var (
	alice = tgbotapi.User{ID: 7, FirstName: "Alice"}
	bob   = tgbotapi.User{ID: 8, UserName: "bob"} // no first name: shown as @bob
)
```
and append to the end of the file:
```go

// legacyFile, legacyChat, legacyStat and legacyPick copy today's
// storage.ChatConfig, UserStat and DailyPick tags verbatim, so stored() reads
// config.json exactly as the pre-refactor binary does.
type legacyFile struct {
	Chats map[string]legacyChat `json:"chats"`
}

type legacyStat struct {
	Name           string `json:"name"`
	Count          int    `json:"count"`
	LastSolvedDate string `json:"last_solved_date"`
}

type legacyPick struct {
	Date            string   `json:"date"`
	DailyDifficulty string   `json:"daily_difficulty"`
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Link            string   `json:"link"`
	Difficulty      string   `json:"difficulty"`
	Tags            []string `json:"tags"`
}

type legacyChat struct {
	ChatID       int64                 `json:"chat_id"`
	NotifyTime   string                `json:"notify_time"`
	Timezone     string                `json:"timezone"`
	Members      map[string]legacyStat `json:"members"`
	Difficulties []string              `json:"difficulties,omitempty"`
	DailyPick    *legacyPick           `json:"daily_pick,omitempty"`
}

// stop cancels the app's ctx and waits for Run to return.
func (e *env) stop() {
	e.t.Helper()
	e.halt()
}

// restart stops the app after every update so far is handled and starts a
// new one on the same dir and fakes.
func (e *env) restart() {
	e.t.Helper()
	e.sync()
	e.stop()
	e.start()
}

// seed writes config.json before start.
func (e *env) seed(raw string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.dir, "config.json"), []byte(raw), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// stored decodes the chat from config.json on disk with the legacy structs.
func (e *env) stored(chatID int64) (legacyChat, bool) {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.dir, "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return legacyChat{}, false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var f legacyFile
	if err := json.Unmarshal(raw, &f); err != nil {
		e.t.Fatalf("config.json: %v\n%s", err, raw)
	}
	c, ok := f.Chats[strconv.FormatInt(chatID, 10)]
	return c, ok
}

// storedEq asserts that config.json holds exactly want for the chat.
func (e *env) storedEq(chatID int64, want legacyChat) {
	e.t.Helper()
	got, ok := e.stored(chatID)
	if !ok {
		e.t.Errorf("chat %d: not in config.json, want %s", chatID, asJSON(want))
		return
	}
	if !reflect.DeepEqual(got, want) {
		e.t.Errorf("chat %d in config.json:\n got %s\nwant %s", chatID, asJSON(got), asJSON(want))
	}
}

// notStored asserts that config.json has no entry for the chat.
func (e *env) notStored(chatID int64) {
	e.t.Helper()
	if got, ok := e.stored(chatID); ok {
		e.t.Errorf("chat %d: still in config.json: %s", chatID, asJSON(got))
	}
}

// fire runs the chat's cron entry now, synchronously, exactly as cron would
// (Recover included); false means nothing is scheduled.
func (e *env) fire(chatID int64) bool {
	return e.a.sched.RunNow(chatID)
}

// scheduledAt asserts that the chat's next firing is at hhmm in zone, within 24h.
func (e *env) scheduledAt(chatID int64, hhmm, zone string) {
	e.t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		e.t.Fatal(err)
	}
	next, ok := e.a.sched.Next(chatID)
	if !ok {
		e.t.Errorf("chat %d: not scheduled, want %s %s", chatID, hhmm, zone)
		return
	}
	if got := next.In(loc).Format("15:04"); got != hhmm {
		e.t.Errorf("chat %d: next firing at %s %s, want %s", chatID, got, zone, hhmm)
	}
	if d := time.Until(next); d <= 0 || d > 24*time.Hour {
		e.t.Errorf("chat %d: next firing %v away, want within 24h", chatID, d)
	}
}

func (e *env) notScheduled(chatID int64) {
	e.t.Helper()
	if next, ok := e.a.sched.Next(chatID); ok {
		e.t.Errorf("chat %d: still scheduled at %v", chatID, next)
	}
}

// press presses the button with data on the newest bot message showing it.
func (e *env) press(chatID int64, from tgbotapi.User, data string) string {
	e.t.Helper()
	msgID, ok := e.tg.newest(chatID, data)
	if !ok {
		e.t.Fatalf("chat %d: no message shows a %q button\n%s", chatID, data, e.tg.transcript(chatID))
	}
	return e.tg.callback(chatID, from, msgID, false, data)
}

// pressOn presses data on message msgID whatever its keyboard shows: stale
// and forged buttons.
func (e *env) pressOn(chatID int64, from tgbotapi.User, msgID int, data string) string {
	return e.tg.callback(chatID, from, msgID, false, data)
}

// pressInline presses a button whose callback has no Message.
func (e *env) pressInline(from tgbotapi.User, data string) string {
	return e.tg.callback(0, from, 0, true, data)
}

// block makes later sendMessage calls to the chat fail with 403.
func (e *env) block(chatID int64) {
	e.tg.block(chatID)
}

// edited expects an editMessageText of msgID; a nil kb means the keyboard is removed.
func (e *env) edited(chatID int64, msgID int, text string, kb keyboard) {
	e.t.Helper()
	c := e.expectMessage(chatID)
	if c.method != "editMessageText" || c.msgID != msgID || c.text != text || !kb.matches(c.kb) {
		e.t.Fatalf("chat %d:\n got %s\nwant editMessageText msg=%d text=%q kb=%s\n%s", chatID, c, msgID, text, kb, e.tg.transcript(chatID))
	}
}

// rekeyed expects an editMessageReplyMarkup of msgID.
func (e *env) rekeyed(chatID int64, msgID int, kb keyboard) {
	e.t.Helper()
	c := e.expectMessage(chatID)
	if c.method != "editMessageReplyMarkup" || c.msgID != msgID || !kb.matches(c.kb) {
		e.t.Fatalf("chat %d:\n got %s\nwant editMessageReplyMarkup msg=%d kb=%s\n%s", chatID, c, msgID, kb, e.tg.transcript(chatID))
	}
}

// blockedAttempt expects a sendMessage that got 403 and returns its text.
func (e *env) blockedAttempt(chatID int64) string {
	e.t.Helper()
	c := e.expectMessage(chatID)
	if c.method != "sendMessage" || !c.blocked {
		e.t.Fatalf("chat %d:\n got %s\nwant a sendMessage rejected with 403\n%s", chatID, c, e.tg.transcript(chatID))
	}
	return c.text
}

// expectAnswer returns the answer to callback cbID.
func (e *env) expectAnswer(cbID string) string {
	e.t.Helper()
	text, ok := e.tg.answer(cbID, waitFor)
	if !ok {
		e.t.Fatalf("callback %s: not answered within %v", cbID, waitFor)
	}
	return text
}

// answered asserts the answer to callback cbID.
func (e *env) answered(cbID, want string) {
	e.t.Helper()
	if got := e.expectAnswer(cbID); got != want {
		e.t.Errorf("callback %s answered %q, want %q", cbID, got, want)
	}
}

func asJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(raw)
}
```

- [ ] **Step 6: Run the suite against the old code**

Run:
```bash
go vet -tags integration ./internal/app/ && gofmt -l internal/app
go test -tags integration -race -count=1 -v ./internal/app/ 2>&1 | grep -c '^    --- PASS: TestIntegration/'
go test -tags integration -race -count=1 ./internal/app/
```
Expected: no output from vet and gofmt, then `18` (rows 1-16, 20 and 21; `TestNewFails` is a separate test), then `ok  	github.com/solympe/leetcode-tg-notifier/internal/app	1.9s` (about 2s; row 9 takes 0.5s).

- [ ] **Step 7: Prove that rows 12 and 16 bite, then revert**

Run:
```bash
sed -i.bak '/^func (b \*tgBot) handleUnsubscribe/,/^}/{/b.sched.Remove(chatID)/d;}' internal/bot/commands.go && rm internal/bot/commands.go.bak
go test -tags integration -race -count=1 ./internal/app/ 2>&1 | grep -E '^\s+--- FAIL|integration_test.go:[0-9]+:'
git checkout internal/bot/commands.go
sed -i.bak 's/if last > 0 {/if false \&\& last > 0 {/' internal/app/app.go && rm internal/app/app.go.bak
go test -tags integration -race -count=5 -run 'TestIntegration/16' ./internal/app/ 2>&1 | grep -c '^    --- FAIL: TestIntegration/16'
git checkout internal/app/app.go
git status --porcelain
```
Expected: the first run prints
```
    --- FAIL: TestIntegration/12_unsubscribe_stops_sends (0.05s)
        integration_test.go:414: fire(1200): still scheduled after unsubscribe
        integration_test.go:416: chat 1200: still scheduled at <the next 09:00 UTC, in the machine's zone>
        integration_test.go:419: chat 1200:
```
Without the confirming `GetUpdates`, every row-16 run handles the drained `/about` a second time after the restart (`chat 1600: unexpected sendMessage msg=3 parse_mode="HTML" text="For questions, suggestions, and bug reports — DM @solympe"`), so the count is `5`. Each `git checkout` prints `Updated 1 path from the index`, and `git status --porcelain` lists only this task's four ` M internal/app/*_test.go` files.

- [ ] **Step 8: Check for flakiness**

Run: `for i in 1 2 3; do go test -tags integration -race -count=3 ./internal/app/; done`
Expected: three lines `ok  	github.com/solympe/leetcode-tg-notifier/internal/app	3.0s` (about 3s each; one may take up to 16s longer when it starts after second 45).

- [ ] **Step 9: Write the golden-fixture row of `TestLoadLegacyConfig`**

In `internal/storage/json_test.go`, replace the import block (lines 3-10)
```go
import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)
```
with
```go
import (
	"cmp"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)
```
and replace lines 310-373, from `func TestLoadLegacyConfig(t *testing.T) {` to the end of the file, with:
```go
func TestLoadLegacyConfig(t *testing.T) {
	// legacy_config.json was written by this jsonStorage before the
	// refactor and is never regenerated: every later version must load it.
	golden, err := os.ReadFile(filepath.Join("testdata", "legacy_config.json"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		raw  string
		want []ChatConfig // sorted by ChatID
	}{
		{
			name: "config without difficulty fields",
			raw: `{
  "chats": {
    "123": {
      "chat_id": 123,
      "notify_time": "09:00",
      "timezone": "Asia/Tbilisi",
      "members": {
        "7": {"name": "Alice", "count": 5, "last_solved_date": "2026-09-20"}
      }
    }
  }
}`,
			want: []ChatConfig{{
				ChatID:     123,
				NotifyTime: "09:00",
				Timezone:   "Asia/Tbilisi",
				Members:    map[string]UserStat{"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-20"}},
			}},
		},
		{
			name: "config without members",
			raw:  `{"chats":{"5":{"chat_id":5,"notify_time":"21:15","timezone":"UTC"}}}`,
			want: []ChatConfig{{ChatID: 5, NotifyTime: "21:15", Timezone: "UTC"}},
		},
		{
			name: "golden file written by the pre-refactor storage",
			raw:  string(golden),
			want: []ChatConfig{
				{
					ChatID:     -1001234567890,
					NotifyTime: "07:00",
					Timezone:   "America/New_York",
					Members: map[string]UserStat{
						"7": {Name: "Alice", Count: 1, LastSolvedDate: "2026-09-26"},
						"8": {Name: "@bob", Count: 4, LastSolvedDate: "2026-09-27"},
					},
					Difficulties: []string{"Medium", "Hard"},
				},
				{
					ChatID:       100,
					NotifyTime:   "09:00",
					Timezone:     "Europe/Moscow",
					Members:      map[string]UserStat{"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-27"}},
					Difficulties: []string{"Easy"},
					DailyPick: &DailyPick{
						Date:            "2026-09-28",
						DailyDifficulty: "Hard",
						ID:              "1",
						Title:           "Two Sum",
						Link:            "/problems/two-sum/",
						Difficulty:      "Easy",
						Tags:            []string{"Array", "Hash Table"},
					},
				},
				{ChatID: 200, NotifyTime: "21:15", Timezone: "Asia/Tbilisi"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.raw), 0o644); err != nil {
				t.Fatal(err)
			}

			s, err := NewJSONStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			got := s.All()
			slices.SortFunc(got, func(a, b ChatConfig) int { return cmp.Compare(a.ChatID, b.ChatID) })
			// DeepEqual tells nil from empty, so absent members, difficulties
			// and pick must load as nil.
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loaded configs:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 10: Run it to see it fail**

Run: `go test ./internal/storage/ -race -count=1 -run TestLoadLegacyConfig`
Expected:
```
--- FAIL: TestLoadLegacyConfig (0.00s)
    json_test.go:317: open testdata/legacy_config.json: no such file or directory
FAIL
FAIL	github.com/solympe/leetcode-tg-notifier/internal/storage	0.3s
FAIL
```

- [ ] **Step 11: Generate the fixture with today's `jsonStorage`, then delete the generator**

Go's internal-package rule lets only code inside this module import `internal/storage`, so the throwaway program lives in `_golden/` at the repo root (the `_` keeps it out of `./...`) and is deleted right after it runs. Create `_golden/main.go`:
```go
// Command _golden writes internal/storage/testdata/legacy_config.json with
// the pre-refactor jsonStorage. It is run once and deleted, never committed.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/solympe/leetcode-tg-notifier/internal/storage"
)

func main() {
	path := filepath.Join("internal", "storage", "testdata", "legacy_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		log.Fatalf("%s exists: the golden file is never regenerated", path)
	}
	s, err := storage.NewJSONStorage(path)
	if err != nil {
		log.Fatal(err)
	}
	chats := []storage.ChatConfig{
		{
			ChatID:       100,
			NotifyTime:   "09:00",
			Timezone:     "Europe/Moscow",
			Members:      map[string]storage.UserStat{"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-27"}},
			Difficulties: []string{"Easy"},
			DailyPick: &storage.DailyPick{
				Date:            "2026-09-28",
				DailyDifficulty: "Hard",
				ID:              "1",
				Title:           "Two Sum",
				Link:            "/problems/two-sum/",
				Difficulty:      "Easy",
				Tags:            []string{"Array", "Hash Table"},
			},
		},
		{ChatID: 200, NotifyTime: "21:15", Timezone: "Asia/Tbilisi"},
		{
			ChatID:     -1001234567890,
			NotifyTime: "07:00",
			Timezone:   "America/New_York",
			Members: map[string]storage.UserStat{
				"7": {Name: "Alice", Count: 1, LastSolvedDate: "2026-09-26"},
				"8": {Name: "@bob", Count: 4, LastSolvedDate: "2026-09-27"},
			},
			Difficulties: []string{"Medium", "Hard"},
		},
	}
	for _, c := range chats {
		if err := s.Set(c); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("wrote %s", path)
}
```
Run:
```bash
go run ./_golden && rm -r _golden
shasum -a 256 internal/storage/testdata/legacy_config.json
git status --porcelain
```
Expected: `<date> <time> wrote internal/storage/testdata/legacy_config.json`, then
```
cdeb9b96faadece072cd982cb90ce465ac95ee16bc506003021b2ff8dbbdc07e  internal/storage/testdata/legacy_config.json
```
and `git status` lists the five modified test files and `?? internal/storage/testdata/`. The file is exactly the following 1187 bytes, **with no trailing newline** (`json.MarshalIndent` + `os.WriteFile`). If the hash differs, stop: the old `jsonStorage` is not what this plan was validated against.
```json
{
  "chats": {
    "-1001234567890": {
      "chat_id": -1001234567890,
      "notify_time": "07:00",
      "timezone": "America/New_York",
      "members": {
        "7": {
          "name": "Alice",
          "count": 1,
          "last_solved_date": "2026-09-26"
        },
        "8": {
          "name": "@bob",
          "count": 4,
          "last_solved_date": "2026-09-27"
        }
      },
      "difficulties": [
        "Medium",
        "Hard"
      ]
    },
    "100": {
      "chat_id": 100,
      "notify_time": "09:00",
      "timezone": "Europe/Moscow",
      "members": {
        "7": {
          "name": "Alice",
          "count": 5,
          "last_solved_date": "2026-09-27"
        }
      },
      "difficulties": [
        "Easy"
      ],
      "daily_pick": {
        "date": "2026-09-28",
        "daily_difficulty": "Hard",
        "id": "1",
        "title": "Two Sum",
        "link": "/problems/two-sum/",
        "difficulty": "Easy",
        "tags": [
          "Array",
          "Hash Table"
        ]
      }
    },
    "200": {
      "chat_id": 200,
      "notify_time": "21:15",
      "timezone": "Asia/Tbilisi",
      "members": null
    }
  }
}
```

- [ ] **Step 12: Re-run the storage test**

Run: `go test ./internal/storage/ -race -count=1 -run TestLoadLegacyConfig -v 2>&1 | grep -E '^(\s*--- |ok|FAIL)'`
Expected:
```
--- PASS: TestLoadLegacyConfig (0.00s)
    --- PASS: TestLoadLegacyConfig/config_without_difficulty_fields (0.00s)
    --- PASS: TestLoadLegacyConfig/config_without_members (0.00s)
    --- PASS: TestLoadLegacyConfig/golden_file_written_by_the_pre-refactor_storage (0.00s)
ok  	github.com/solympe/leetcode-tg-notifier/internal/storage	1.1s
```

- [ ] **Step 13: Task gate (the suite passes against the old implementation)**

Run:
```bash
go build ./... && go test ./... -race -count=1 && make lint && make test-integration \
  && go test -tags integration -race -count=3 ./internal/app/
```
Expected: `ok` for `internal/bot`, `internal/leetcode`, `internal/scheduler` and `internal/storage` (`internal/app` has `[no test files]` untagged); `0 issues.`; `make test-integration` prints `ok` for `internal/app` and the same four packages; the `-count=3` run prints `ok  	github.com/solympe/leetcode-tg-notifier/internal/app`.

- [ ] **Step 14: Commit**

```bash
git add internal/app/faketelegram_test.go internal/app/fakeleetcode_test.go internal/app/harness_test.go \
  internal/app/integration_test.go internal/storage/json_test.go internal/storage/testdata/legacy_config.json
git commit -F - <<'EOF'
test: characterize today's behaviour in integration rows 2-16

The rows pin subscribing by buttons and by typing, stale and forged
buttons, re-running /setup, the scheduled daily with Done and ratings,
the random pick of the day and its reuse, concurrent sends agreeing,
/daily, status and not-subscribed paths, unsubscribe, a blocked bot,
a LeetCode outage, restart restoring schedules and a shutdown that
handles a racing update exactly once. Rows 20-21 and TestNewFails pin
a restart mid-dialog, an unschedulable stored chat and the startup
errors. They pass against the current implementation and must pass
unchanged after the switch.

storage/testdata/legacy_config.json was written by the current
jsonStorage and is loaded by TestLoadLegacyConfig; it is never
regenerated.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [ ] **Step 15: Mocks check on the committed tree**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `CLEAN` and nothing else.

---

### Task 6: `internal/domain`

**Files:**
- Create: `internal/domain/model_test.go`
- Create: `internal/domain/rules_test.go`
- Create: `internal/domain/model.go`
- Create: `internal/domain/errors.go`
- Create: `internal/domain/rules.go`

**Interfaces:** Consumes: nothing. The package is stdlib only and imports no earlier task. Produces (package `github.com/solympe/leetcode-tg-notifier/internal/domain`; Tasks 7-10 rely on these names):
- `const Easy, Medium, Hard = "Easy", "Medium", "Hard"`
- `func Difficulties() []string`: a fresh `{Easy, Medium, Hard}` on every call (it replaces `leetcode.AllDifficulties`)
- `type Problem struct { Date, ID, Title, Link, Difficulty string; Tags []string }`, with JSON keys `date`, `id`, `title`, `link`, `difficulty`, `tags`
- `type Pick struct { Problem; DailyDifficulty string }`, with the embedded fields flattened plus `daily_difficulty`
- `type Member struct { Name string; Count int; LastSolvedDate string }`, with keys `name`, `count`, `last_solved_date`
- `type Chat struct { ChatID int64; NotifyTime, Timezone string; Members map[string]Member; Difficulties []string; DailyPick *Pick }`, with keys `chat_id`, `notify_time`, `timezone`, `members` (no omitempty), `difficulties,omitempty`, `daily_pick,omitempty`
- `func (c Chat) Wants(difficulty string) bool`
- `func (c Chat) PickFor(daily Problem) (Pick, bool)`
- `func (c *Chat) RecordSolve(userID int64, name, day string) (total int, counted bool)`
- `func (c Chat) Standings() []Member`: Count desc, then Name asc
- `var ErrNotSubscribed, ErrAlreadySolved, ErrBlocked error`, with texts `chat is not subscribed`, `already solved today` and `chat blocked the bot`

Nothing imports the package after this task.

- [ ] **Step 1: Write the model tests**

`TestChatJSON` pins the `config.json` contract: a flat legacy `daily_pick` decodes into `Pick`, `members: null` and a missing `difficulties` decode to nil, and every row round-trips. Create `internal/domain/model_test.go`:

```go
package domain

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestDifficulties(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]string)
	}{
		{name: "canonical order", mutate: func([]string) {}},
		{name: "fresh slice on every call", mutate: func(s []string) { s[0] = "Mutated" }},
	}

	want := []string{"Easy", "Medium", "Hard"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mutate(Difficulties())
			if got := Difficulties(); !slices.Equal(got, want) {
				t.Errorf("Difficulties() = %v, want %v", got, want)
			}
		})
	}
}

// TestChatJSON pins the config.json contract: raw is a chat as today's
// storage.ChatConfig writes it, and wantJSON is how Chat writes it back.
func TestChatJSON(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		want     Chat
		wantJSON string
	}{
		{
			name: "flat legacy daily_pick decodes into Pick",
			raw: `{"chat_id":-100123,"notify_time":"09:00","timezone":"Europe/Moscow",` +
				`"members":{"7":{"name":"Alice","count":2,"last_solved_date":"2026-09-27"}},` +
				`"difficulties":["Easy"],` +
				`"daily_pick":{"date":"2026-09-28","daily_difficulty":"Hard","id":"1","title":"Two Sum",` +
				`"link":"/problems/two-sum/","difficulty":"Easy","tags":["Array","Hash Table"]}}`,
			want: Chat{
				ChatID:       -100123,
				NotifyTime:   "09:00",
				Timezone:     "Europe/Moscow",
				Members:      map[string]Member{"7": {Name: "Alice", Count: 2, LastSolvedDate: "2026-09-27"}},
				Difficulties: []string{"Easy"},
				DailyPick: &Pick{
					Problem: Problem{
						Date:       "2026-09-28",
						ID:         "1",
						Title:      "Two Sum",
						Link:       "/problems/two-sum/",
						Difficulty: "Easy",
						Tags:       []string{"Array", "Hash Table"},
					},
					DailyDifficulty: "Hard",
				},
			},
			wantJSON: `{"chat_id":-100123,"notify_time":"09:00","timezone":"Europe/Moscow",` +
				`"members":{"7":{"name":"Alice","count":2,"last_solved_date":"2026-09-27"}},` +
				`"difficulties":["Easy"],` +
				`"daily_pick":{"date":"2026-09-28","id":"1","title":"Two Sum","link":"/problems/two-sum/",` +
				`"difficulty":"Easy","tags":["Array","Hash Table"],"daily_difficulty":"Hard"}}`,
		},
		{
			name:     "null members and missing difficulties decode to nil",
			raw:      `{"chat_id":5,"notify_time":"21:15","timezone":"UTC","members":null}`,
			want:     Chat{ChatID: 5, NotifyTime: "21:15", Timezone: "UTC"},
			wantJSON: `{"chat_id":5,"notify_time":"21:15","timezone":"UTC","members":null}`,
		},
		{
			name:     "missing members field decodes to nil and is written as null",
			raw:      `{"chat_id":123,"notify_time":"07:00","timezone":"Asia/Tbilisi"}`,
			want:     Chat{ChatID: 123, NotifyTime: "07:00", Timezone: "Asia/Tbilisi"},
			wantJSON: `{"chat_id":123,"notify_time":"07:00","timezone":"Asia/Tbilisi","members":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Chat
			if err := json.Unmarshal([]byte(tt.raw), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decoded:\n got %#v\nwant %#v", got, tt.want)
			}

			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.wantJSON {
				t.Errorf("encoded:\n got %s\nwant %s", data, tt.wantJSON)
			}

			var back Chat
			if err := json.Unmarshal(data, &back); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(back, tt.want) {
				t.Errorf("round trip:\n got %#v\nwant %#v", back, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Write the rule tests**

These are pure tables ported from `TestWantsDaily`, the `validPick` rows of `TestSendDailyProblem` and `TestHandleDone`. Create `internal/domain/rules_test.go`:

```go
package domain

import (
	"reflect"
	"slices"
	"testing"
)

func TestWants(t *testing.T) {
	tests := []struct {
		name       string
		set        []string
		difficulty string
		want       bool
	}{
		{name: "nil set means any", set: nil, difficulty: Hard, want: true},
		{name: "empty set means any", set: []string{}, difficulty: Hard, want: true},
		{name: "subscribed difficulty", set: []string{Easy, Hard}, difficulty: Hard, want: true},
		{name: "case-insensitive match", set: []string{"hard"}, difficulty: Hard, want: true},
		{name: "not subscribed", set: []string{Easy, Medium}, difficulty: Hard, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Chat{Difficulties: tt.set}
			if got := c.Wants(tt.difficulty); got != tt.want {
				t.Errorf("Chat{Difficulties: %v}.Wants(%q) = %v, want %v", tt.set, tt.difficulty, got, tt.want)
			}
		})
	}
}

func TestPickFor(t *testing.T) {
	daily := Problem{
		Date:       "2026-09-28",
		ID:         "4",
		Title:      "Median of Two Sorted Arrays",
		Link:       "/problems/median-of-two-sorted-arrays/",
		Difficulty: Hard,
		Tags:       []string{"Array", "Binary Search", "Divide and Conquer"},
	}
	// easyPick is a random Easy problem that replaced a Hard daily of date.
	easyPick := func(date string) *Pick {
		return &Pick{
			Problem: Problem{
				Date:       date,
				ID:         "1",
				Title:      "Two Sum",
				Link:       "/problems/two-sum/",
				Difficulty: Easy,
				Tags:       []string{"Array", "Hash Table"},
			},
			DailyDifficulty: Hard,
		}
	}

	tests := []struct {
		name   string
		chat   Chat
		want   Pick
		wantOK bool
	}{
		{
			name: "no pick",
			chat: Chat{Difficulties: []string{Easy}},
		},
		{
			name: "pick for another date",
			chat: Chat{Difficulties: []string{Easy}, DailyPick: easyPick("2026-09-27")},
		},
		{
			name: "pick of a difficulty no longer subscribed",
			chat: Chat{Difficulties: []string{Medium}, DailyPick: easyPick("2026-09-28")},
		},
		{
			name:   "valid pick",
			chat:   Chat{Difficulties: []string{Easy}, DailyPick: easyPick("2026-09-28")},
			want:   *easyPick("2026-09-28"),
			wantOK: true,
		},
		{
			name:   "valid pick under a case-insensitive set",
			chat:   Chat{Difficulties: []string{"easy"}, DailyPick: easyPick("2026-09-28")},
			want:   *easyPick("2026-09-28"),
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.chat.PickFor(daily)
			if ok != tt.wantOK || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PickFor() = %#v, %v; want %#v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRecordSolve(t *testing.T) {
	const day = "2026-09-28"

	tests := []struct {
		name        string
		members     map[string]Member
		userID      int64
		userName    string
		wantTotal   int
		wantCounted bool
		wantMembers map[string]Member
	}{
		{
			name:        "first solve with nil members is counted",
			members:     nil,
			userID:      7,
			userName:    "Alice",
			wantTotal:   1,
			wantCounted: true,
			wantMembers: map[string]Member{"7": {Name: "Alice", Count: 1, LastSolvedDate: day}},
		},
		{
			name:        "same day is rejected with the total and keeps the name",
			members:     map[string]Member{"7": {Name: "Alice", Count: 3, LastSolvedDate: day}},
			userID:      7,
			userName:    "Alicia",
			wantTotal:   3,
			wantCounted: false,
			wantMembers: map[string]Member{"7": {Name: "Alice", Count: 3, LastSolvedDate: day}},
		},
		{
			name:        "next day is counted and refreshes the name",
			members:     map[string]Member{"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-27"}},
			userID:      7,
			userName:    "Alicia",
			wantTotal:   6,
			wantCounted: true,
			wantMembers: map[string]Member{"7": {Name: "Alicia", Count: 6, LastSolvedDate: day}},
		},
		{
			name:        "another member is keyed by decimal user ID and leaves others alone",
			members:     map[string]Member{"7": {Name: "Alice", Count: 2, LastSolvedDate: day}},
			userID:      5000000008,
			userName:    "@bob",
			wantTotal:   1,
			wantCounted: true,
			wantMembers: map[string]Member{
				"7":          {Name: "Alice", Count: 2, LastSolvedDate: day},
				"5000000008": {Name: "@bob", Count: 1, LastSolvedDate: day},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Chat{ChatID: 100, Members: tt.members}
			total, counted := c.RecordSolve(tt.userID, tt.userName, day)
			if total != tt.wantTotal || counted != tt.wantCounted {
				t.Errorf("RecordSolve() = %d, %v; want %d, %v", total, counted, tt.wantTotal, tt.wantCounted)
			}
			if !reflect.DeepEqual(c.Members, tt.wantMembers) {
				t.Errorf("Members:\n got %v\nwant %v", c.Members, tt.wantMembers)
			}
		})
	}
}

func TestStandings(t *testing.T) {
	tests := []struct {
		name    string
		members map[string]Member
		want    []Member
	}{
		{
			name:    "no members",
			members: nil,
			want:    nil,
		},
		{
			name: "count descending",
			members: map[string]Member{
				"1": {Name: "Alice", Count: 5},
				"2": {Name: "Bob", Count: 12},
				"3": {Name: "Charlie", Count: 3},
			},
			want: []Member{{Name: "Bob", Count: 12}, {Name: "Alice", Count: 5}, {Name: "Charlie", Count: 3}},
		},
		{
			name: "ties ordered by name",
			members: map[string]Member{
				"1": {Name: "Carol", Count: 2},
				"2": {Name: "Alice", Count: 2},
				"3": {Name: "Bob", Count: 3},
			},
			want: []Member{{Name: "Bob", Count: 3}, {Name: "Alice", Count: 2}, {Name: "Carol", Count: 2}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Chat{Members: tt.members}).Standings(); !slices.Equal(got, tt.want) {
				t.Errorf("Standings() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/domain/`
Expected:
```
# github.com/solympe/leetcode-tg-notifier/internal/domain [github.com/solympe/leetcode-tg-notifier/internal/domain.test]
internal/domain/model_test.go:22:14: undefined: Difficulties
internal/domain/model_test.go:23:14: undefined: Difficulties
internal/domain/model_test.go:36:12: undefined: Chat
internal/domain/model_test.go:46:10: undefined: Chat
internal/domain/model_test.go:50:30: undefined: Member
internal/domain/model_test.go:52:17: undefined: Pick
internal/domain/model_test.go:53:15: undefined: Problem
internal/domain/model_test.go:73:14: undefined: Chat
internal/domain/model_test.go:79:14: undefined: Chat
internal/domain/model_test.go:86:12: undefined: Chat
internal/domain/model_test.go:86:12: too many errors
FAIL	github.com/solympe/leetcode-tg-notifier/internal/domain [build failed]
FAIL
```

- [ ] **Step 4: Create the model**

Create `internal/domain/model.go`:

```go
// Package domain holds the persisted model, its pure rules and the sentinel
// errors shared by the use cases and the adapters. It imports only the
// standard library.
//
// The JSON tags are the config.json contract: renaming one needs a migration.
package domain

// Difficulty levels, spelled as LeetCode returns them.
const Easy, Medium, Hard = "Easy", "Medium", "Hard"

// Difficulties returns every difficulty in canonical order. The slice is
// freshly allocated on each call, so callers may modify it.
func Difficulties() []string {
	return []string{Easy, Medium, Hard}
}

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
```

- [ ] **Step 5: Create the sentinel errors**

Create `internal/domain/errors.go`:

```go
package domain

import "errors"

// Sentinel errors of the use cases. Adapters wrap or map them, so callers
// test with errors.Is.
var (
	ErrNotSubscribed = errors.New("chat is not subscribed")
	ErrAlreadySolved = errors.New("already solved today")
	ErrBlocked       = errors.New("chat blocked the bot")
)
```

- [ ] **Step 6: Create the rules**

Create `internal/domain/rules.go`:

```go
package domain

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Wants reports whether the chat gets a daily of difficulty as is; an empty
// set means any difficulty.
func (c Chat) Wants(difficulty string) bool {
	return len(c.Difficulties) == 0 ||
		slices.ContainsFunc(c.Difficulties, func(d string) bool { return strings.EqualFold(d, difficulty) })
}

// PickFor returns the stored pick of the day when it can be resent in place
// of daily: it replaces that daily's date and its difficulty is still
// subscribed, which also rejects a pick made for a selection changed since.
func (c Chat) PickFor(daily Problem) (Pick, bool) {
	if c.DailyPick == nil || c.DailyPick.Date != daily.Date || !c.Wants(c.DailyPick.Difficulty) {
		return Pick{}, false
	}
	return *c.DailyPick, true
}

// RecordSolve counts userID's solve on day ("2006-01-02", UTC) and returns the
// member's total. A second solve on the same day is not counted and does not
// refresh the stored name.
func (c *Chat) RecordSolve(userID int64, name, day string) (total int, counted bool) {
	key := strconv.FormatInt(userID, 10)
	m := c.Members[key]
	if m.LastSolvedDate == day {
		return m.Count, false
	}
	m.Name = name
	m.Count++
	m.LastSolvedDate = day
	if c.Members == nil {
		c.Members = make(map[string]Member)
	}
	c.Members[key] = m
	return m.Count, true
}

// Standings returns the members by solve count, highest first; ties are
// ordered by name.
func (c Chat) Standings() []Member {
	ms := slices.Collect(maps.Values(c.Members))
	slices.SortFunc(ms, func(a, b Member) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.Name, b.Name))
	})
	return ms
}
```

- [ ] **Step 7: Re-run the domain tests**

Run: `go test ./internal/domain/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected:
```
--- PASS: TestDifficulties (0.00s)
--- PASS: TestChatJSON (0.00s)
--- PASS: TestWants (0.00s)
--- PASS: TestPickFor (0.00s)
--- PASS: TestRecordSolve (0.00s)
--- PASS: TestStandings (0.00s)
ok  	github.com/solympe/leetcode-tg-notifier/internal/domain	1.2s
```
The timing differs.

- [ ] **Step 8: Task gate**

Run:
```bash
go build ./... && go test ./... -race -count=1 && make lint && make test-integration
go list -deps . | grep -E 'internal/domain$'; echo "domain linked: exit=$?"
```
Expected: `go test` prints `ok` for `internal/bot`, `internal/domain`, `internal/leetcode`, `internal/scheduler` and `internal/storage`, and `[no test files]` for the root, `internal/app` (its suite is tagged) and the two `mocks` packages. `make lint` prints `0 issues.` `make test-integration` prints the same, plus `ok` for `internal/app`: rows 1-16, 20-21 and `TestNewFails` pass unchanged. The last line prints `domain linked: exit=1`, because the binary does not link the package yet.

- [ ] **Step 9: Commit**

```bash
git add internal/domain/model.go internal/domain/rules.go internal/domain/errors.go internal/domain/model_test.go internal/domain/rules_test.go
git commit -F - <<'EOF'
refactor(domain): add the persisted model, its rules and sentinel errors

internal/domain is stdlib only. Chat, Member, Problem and Pick carry
today's config.json tags, so a flat legacy daily_pick decodes into
Pick{Problem, DailyDifficulty}. Wants, PickFor, RecordSolve and
Standings are the pure rules of wantsDaily, validPick and handleDone;
Standings orders ties by name. Nothing imports the package yet.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [ ] **Step 10: Mocks check on the committed tree**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `CLEAN` and nothing else.

---

### Task 7: `internal/notifier`

**Files:**
- Create: `internal/notifier/deps.go`
- Create (generated): `internal/notifier/mocks/mock_deps.go`
- Create: `internal/notifier/delivery_test.go`
- Create: `internal/notifier/service_test.go`
- Create: `internal/notifier/service.go`
- Create: `internal/notifier/delivery.go`

**Interfaces:** Consumes (Task 6): `domain.Chat`, `domain.Problem`, `domain.Pick`, `domain.Member`, `(domain.Chat).Wants(string) bool`, `(domain.Chat).PickFor(domain.Problem) (domain.Pick, bool)`, `(*domain.Chat).RecordSolve(userID int64, name, day string) (int, bool)`, `domain.ErrNotSubscribed`, `domain.ErrAlreadySolved`, `domain.ErrBlocked`, and `domain.Easy`/`Medium`/`Hard` in tests. It also uses `go.uber.org/mock` v0.6.0, which is already in `go.mod`, and `mockgen` v0.6.0 from Task 1. Produces (package `github.com/solympe/leetcode-tg-notifier/internal/notifier`):
- `func New(jobs context.Context, store chatStore, lc problemSource, sched dailyScheduler, out messenger, now func() time.Time) *service`. `jobs` is the parent of every scheduled job's ctx. It is owned and cancelled by `app` (spec §6.4).
- Methods on `*service`, the exact method set of Task 9's `telegram.service` interface plus `Restore`:
  - `Restore(ctx context.Context) error`
  - `Subscribe(ctx context.Context, chatID int64, notifyTime, timezone string, difficulties []string) error`
  - `SetDifficulties(ctx context.Context, chatID int64, difficulties []string) error`
  - `Unsubscribe(ctx context.Context, chatID int64) error`
  - `Subscription(ctx context.Context, chatID int64) (domain.Chat, bool, error)`
  - `Solve(ctx context.Context, chatID, userID int64, name string) (int, error)`
  - `SendToday(ctx context.Context, chatID int64)`
  - `SendDaily(ctx context.Context, chatID int64)`
- The private ports in `deps.go`, which Task 10 satisfies with storage's `*jsonStorage`, leetcode's `*httpClient`, scheduler's `*cronScheduler` and telegram's `*sender`:
  - `chatStore`: `Get(ctx, chatID int64) (domain.Chat, bool, error)`, `Upsert(ctx, chatID int64, fn func(*domain.Chat)) error`, `Update(ctx, chatID int64, fn func(*domain.Chat) bool) (found bool, err error)`, `Delete(ctx, chatID int64) error` and `All(ctx) ([]domain.Chat, error)`
  - `problemSource`: `FetchDaily(ctx) (domain.Problem, error)` and `FetchRandom(ctx, difficulties []string) (domain.Problem, error)`
  - `dailyScheduler`: `Schedule(chatID int64, notifyTime, timezone string, job func()) error` and `Remove(chatID int64)`
  - `messenger`: `SendProblem(ctx, chatID int64, p domain.Pick) error` and `SendFetchFailed(ctx, chatID int64) error`
- `const jobTimeout = 2 * time.Minute`
- Generated `internal/notifier/mocks`: `NewMockchatStore`, `NewMockproblemSource`, `NewMockdailyScheduler` and `NewMockmessenger`, returning `*MockchatStore`, `*MockproblemSource`, `*MockdailyScheduler` and `*Mockmessenger`. They import `context`, `domain` and gomock, never `notifier`.

Task 10 wires it as `notifier.New(jobs, store, leetcode.NewHTTPClient(cfg.LeetCodeEndpoint, nil), sched, telegram.NewSender(api), time.Now)`, then calls `svc.Restore(ctx)`. Nothing imports the package after this task.

- [ ] **Step 1: Declare the ports**

Create `internal/notifier/deps.go`:

```go
package notifier

//go:generate mockgen -source=deps.go -destination=mocks/mock_deps.go -package=mocks

import (
	"context"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

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

- [ ] **Step 2: Generate the mocks**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./internal/notifier/... && go build ./internal/notifier/... && git status --porcelain
```
Expected: `?? internal/notifier/`. `internal/notifier/mocks/mock_deps.go` starts with `// Code generated by MockGen. DO NOT EDIT.` and `// Source: deps.go`, and it defines `MockchatStore` (`All`, `Delete`, `Get`, `Update`, `Upsert`), `MockproblemSource` (`FetchDaily`, `FetchRandom`), `MockdailyScheduler` (`Remove`, `Schedule`) and `Mockmessenger` (`SendFetchFailed`, `SendProblem`).

- [ ] **Step 3: Write the delivery tests and the shared fixtures**

This file holds the fixtures that both test files use: `dailyDate`, `errDisk`, the fixed `clock`, problems without a `Date` as `FetchRandom` returns them, the chat builders, `cloneChat` and `applyUpdate`. It covers `TestSendToday` (the 16 decision rows of today's `TestSendDailyProblem` re-expressed on the ports, plus the Get-error, save-error and delete rows), `TestSendTodayConcurrent`, `TestSendDaily` and `TestJob`. Calls made from inside a job closure get a ctx derived from `jobs`, so they match ctx with `gomock.Any()`; everything else matches `gomock.Eq(ctx)`. Create `internal/notifier/delivery_test.go`:

```go
package notifier

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/notifier/mocks"
)

// dailyDate is the date of every daily below and the UTC day of clock.
const dailyDate = "2026-09-28"

var errDisk = errors.New("disk full")

// clock is the fixed now: 01:30 on September 29 in UTC+3, which is still
// September 28 in UTC.
func clock() time.Time {
	return time.Date(2026, time.September, 29, 1, 30, 0, 0, time.FixedZone("UTC+3", 3*60*60))
}

// dailyOf is today's official daily with difficulty d.
func dailyOf(d string) domain.Problem {
	return domain.Problem{
		Date:       dailyDate,
		ID:         "4",
		Title:      "Median of Two Sorted Arrays",
		Link:       "/problems/median-of-two-sorted-arrays/",
		Difficulty: d,
		Tags:       []string{"Array", "Binary Search", "Divide and Conquer"},
	}
}

// officialPick is how the daily of difficulty d is sent.
func officialPick(d string) domain.Pick {
	return domain.Pick{Problem: dailyOf(d)}
}

// twoSum, climbingStairs and addTwoNumbers are random draws as FetchRandom
// returns them: without a Date.
func twoSum() domain.Problem {
	return domain.Problem{
		ID:         "1",
		Title:      "Two Sum",
		Link:       "/problems/two-sum/",
		Difficulty: domain.Easy,
		Tags:       []string{"Array", "Hash Table"},
	}
}

func climbingStairs() domain.Problem {
	return domain.Problem{
		ID:         "70",
		Title:      "Climbing Stairs",
		Link:       "/problems/climbing-stairs/",
		Difficulty: domain.Easy,
		Tags:       []string{"Math", "Dynamic Programming"},
	}
}

func addTwoNumbers() domain.Problem {
	return domain.Problem{
		ID:         "2",
		Title:      "Add Two Numbers",
		Link:       "/problems/add-two-numbers/",
		Difficulty: domain.Medium,
		Tags:       []string{"Linked List", "Math"},
	}
}

// randomPick is the pick of the day made of draw r for a Hard daily of date.
func randomPick(r domain.Problem, date string) domain.Pick {
	r.Date = date
	return domain.Pick{Problem: r, DailyDifficulty: domain.Hard}
}

// subscribed is chat 100, notified at 09:00 UTC, subscribed to ds.
func subscribed(ds ...string) domain.Chat {
	return domain.Chat{ChatID: 100, NotifyTime: "09:00", Timezone: "UTC", Difficulties: ds}
}

func withPick(c domain.Chat, p domain.Pick) domain.Chat {
	c.DailyPick = &p
	return c
}

func withMembers(c domain.Chat, ms map[string]domain.Member) domain.Chat {
	c.Members = ms
	return c
}

// cloneChat deep-copies c's reference fields, as the store does for every
// chat going in or out.
func cloneChat(c domain.Chat) domain.Chat {
	c.Members = maps.Clone(c.Members)
	c.Difficulties = slices.Clone(c.Difficulties)
	if c.DailyPick != nil {
		p := *c.DailyPick
		p.Tags = slices.Clone(p.Tags)
		c.DailyPick = &p
	}
	return c
}

// applyUpdate stands in for chatStore.Update of a stored chat: it applies fn
// to a copy of stored, as the store does, and fails the test unless fn
// leaves want and reports wantWrite.
func applyUpdate(ctrl *gomock.Controller, stored, want domain.Chat, wantWrite bool) func(context.Context, int64, func(*domain.Chat) bool) (bool, error) {
	return func(_ context.Context, _ int64, fn func(*domain.Chat) bool) (bool, error) {
		c := cloneChat(stored)
		if write := fn(&c); write != wantWrite {
			ctrl.T.Errorf("Update fn reported write=%v, want %v", write, wantWrite)
		}
		if !reflect.DeepEqual(c, want) {
			ctrl.T.Errorf("Update left\n %+v (pick %+v)\nwant\n %+v (pick %+v)", c, c.DailyPick, want, want.DailyPick)
		}
		return true, nil
	}
}

func TestSendToday(t *testing.T) {
	ctx := t.Context()
	members := map[string]domain.Member{"7": {Name: "Alice", Count: 3, LastSolvedDate: dailyDate}}

	hardDaily := func(ctrl *gomock.Controller) *mocks.MockproblemSource {
		m := mocks.NewMockproblemSource(ctrl)
		m.EXPECT().FetchDaily(gomock.Eq(ctx)).Return(dailyOf(domain.Hard), nil)
		return m
	}
	// draws serves a Hard daily, then one FetchRandom(set) returning r, err.
	draws := func(set []string, r domain.Problem, err error) func(*gomock.Controller) *mocks.MockproblemSource {
		return func(ctrl *gomock.Controller) *mocks.MockproblemSource {
			m := hardDaily(ctrl)
			m.EXPECT().FetchRandom(gomock.Eq(ctx), gomock.Eq(set)).Return(r, err)
			return m
		}
	}
	// gets serves Get(100) = c, ok and nothing else.
	gets := func(c domain.Chat, ok bool) func(*gomock.Controller) *mocks.MockchatStore {
		return func(ctrl *gomock.Controller) *mocks.MockchatStore {
			m := mocks.NewMockchatStore(ctrl)
			m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(c, ok, nil)
			return m
		}
	}
	// picks serves Get(100) = got, then one Update that finds cur, the chat
	// as it is at save time, and must leave want and report wantWrite.
	picks := func(got, cur, want domain.Chat, wantWrite bool) func(*gomock.Controller) *mocks.MockchatStore {
		return func(ctrl *gomock.Controller) *mocks.MockchatStore {
			m := mocks.NewMockchatStore(ctrl)
			gomock.InOrder(
				m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(got, true, nil),
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).
					DoAndReturn(applyUpdate(ctrl, cur, want, wantWrite)),
			)
			return m
		}
	}
	// sends expects SendProblem(100, p), which returns err.
	sends := func(p domain.Pick, err error) func(*gomock.Controller) *mocks.Mockmessenger {
		return func(ctrl *gomock.Controller) *mocks.Mockmessenger {
			m := mocks.NewMockmessenger(ctrl)
			m.EXPECT().SendProblem(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Eq(p)).Return(err)
			return m
		}
	}
	fetchFailed := func(ctrl *gomock.Controller) *mocks.Mockmessenger {
		m := mocks.NewMockmessenger(ctrl)
		m.EXPECT().SendFetchFailed(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(nil)
		return m
	}

	tests := []struct {
		name      string
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		lcMock    func(*gomock.Controller) *mocks.MockproblemSource
		schedMock func(*gomock.Controller) *mocks.MockdailyScheduler
		outMock   func(*gomock.Controller) *mocks.Mockmessenger
	}{
		{
			name:      "daily fetch error sends fetch-failed",
			storeMock: mocks.NewMockchatStore,
			lcMock: func(ctrl *gomock.Controller) *mocks.MockproblemSource {
				m := mocks.NewMockproblemSource(ctrl)
				m.EXPECT().FetchDaily(gomock.Eq(ctx)).Return(domain.Problem{}, errors.New("timeout"))
				return m
			},
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   fetchFailed,
		},
		{
			name: "store read error sends fetch-failed",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(domain.Chat{}, false, context.DeadlineExceeded)
				return m
			},
			lcMock:    hardDaily,
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   fetchFailed,
		},
		{
			name:      "unsubscribed chat gets the daily",
			storeMock: gets(domain.Chat{}, false),
			lcMock:    hardDaily,
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(officialPick(domain.Hard), nil),
		},
		{
			name:      "empty set gets the daily without a random fetch",
			storeMock: gets(subscribed(), true),
			lcMock:    hardDaily,
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(officialPick(domain.Hard), nil),
		},
		{
			name:      "subscribed daily is sent as is",
			storeMock: gets(withPick(subscribed(domain.Easy, domain.Hard), randomPick(twoSum(), "2026-09-27")), true),
			lcMock:    hardDaily,
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(officialPick(domain.Hard), nil),
		},
		{
			name:      "subscribed daily matches case-insensitively",
			storeMock: gets(subscribed("easy", "hard"), true),
			lcMock:    hardDaily,
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(officialPick(domain.Hard), nil),
		},
		{
			name:      "subscribed daily beats a same-day pick",
			storeMock: gets(withPick(subscribed(domain.Easy, domain.Hard), randomPick(twoSum(), dailyDate)), true),
			lcMock:    hardDaily,
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(officialPick(domain.Hard), nil),
		},
		{
			name:      "valid pick is resent without a random fetch or a save",
			storeMock: gets(withPick(subscribed(domain.Easy), randomPick(twoSum(), dailyDate)), true),
			lcMock:    hardDaily,
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(twoSum(), dailyDate), nil),
		},
		{
			name: "no pick: a random problem is sent and saved",
			storeMock: picks(
				subscribed(domain.Easy),
				subscribed(domain.Easy),
				withPick(subscribed(domain.Easy), randomPick(twoSum(), dailyDate)),
				true,
			),
			lcMock:    draws([]string{domain.Easy}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(twoSum(), dailyDate), nil),
		},
		{
			name: "random fetch gets every subscribed difficulty",
			storeMock: picks(
				subscribed(domain.Easy, domain.Medium),
				subscribed(domain.Easy, domain.Medium),
				withPick(subscribed(domain.Easy, domain.Medium), randomPick(twoSum(), dailyDate)),
				true,
			),
			lcMock:    draws([]string{domain.Easy, domain.Medium}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(twoSum(), dailyDate), nil),
		},
		{
			name: "pick is saved onto the current chat so concurrent changes survive",
			storeMock: picks(
				subscribed(domain.Easy),
				withMembers(subscribed(domain.Easy), members),
				withPick(withMembers(subscribed(domain.Easy), members), randomPick(twoSum(), dailyDate)),
				true,
			),
			lcMock:    draws([]string{domain.Easy}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(twoSum(), dailyDate), nil),
		},
		{
			// The chat switched from Easy to Medium after today's Easy pick was saved.
			name: "same-day pick of a difficulty no longer subscribed is replaced",
			storeMock: picks(
				withPick(subscribed(domain.Medium), randomPick(twoSum(), dailyDate)),
				withPick(subscribed(domain.Medium), randomPick(twoSum(), dailyDate)),
				withPick(subscribed(domain.Medium), randomPick(addTwoNumbers(), dailyDate)),
				true,
			),
			lcMock:    draws([]string{domain.Medium}, addTwoNumbers(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(addTwoNumbers(), dailyDate), nil),
		},
		{
			name: "stale pick from another day is replaced",
			storeMock: picks(
				withPick(subscribed(domain.Easy), randomPick(climbingStairs(), "2026-09-27")),
				withPick(subscribed(domain.Easy), randomPick(climbingStairs(), "2026-09-27")),
				withPick(subscribed(domain.Easy), randomPick(twoSum(), dailyDate)),
				true,
			),
			lcMock:    draws([]string{domain.Easy}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(twoSum(), dailyDate), nil),
		},
		{
			// Another send committed Climbing Stairs between our Get and our Update.
			name: "lost race: the pick committed first is sent and ours is dropped",
			storeMock: picks(
				subscribed(domain.Easy),
				withPick(subscribed(domain.Easy), randomPick(climbingStairs(), dailyDate)),
				withPick(subscribed(domain.Easy), randomPick(climbingStairs(), dailyDate)),
				false,
			),
			lcMock:    draws([]string{domain.Easy}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(climbingStairs(), dailyDate), nil),
		},
		{
			name:      "random fetch error sends fetch-failed and saves nothing",
			storeMock: gets(subscribed(domain.Easy), true),
			lcMock:    draws([]string{domain.Easy}, domain.Problem{}, errors.New("timeout")),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   fetchFailed,
		},
		{
			name: "chat deleted before the pick is saved still gets it",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(subscribed(domain.Easy), true, nil),
					m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(false, nil),
				)
				return m
			},
			lcMock:    draws([]string{domain.Easy}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(twoSum(), dailyDate), nil),
		},
		{
			name: "pick save error still sends it",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(subscribed(domain.Easy), true, nil),
					m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(true, errDisk),
				)
				return m
			},
			lcMock:    draws([]string{domain.Easy}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(twoSum(), dailyDate), nil),
		},
		{
			name: "blocked chat is unsubscribed",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(domain.Chat{}, false, nil),
					m.EXPECT().Delete(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(nil),
				)
				return m
			},
			lcMock: hardDaily,
			schedMock: func(ctrl *gomock.Controller) *mocks.MockdailyScheduler {
				m := mocks.NewMockdailyScheduler(ctrl)
				m.EXPECT().Remove(gomock.Eq(int64(100)))
				return m
			},
			outMock: sends(officialPick(domain.Hard), fmt.Errorf("x: %w", domain.ErrBlocked)),
		},
		{
			// No Remove or Delete expected: gomock fails the test if they are called.
			name:      "other send error keeps the subscription",
			storeMock: gets(domain.Chat{}, false),
			lcMock:    hardDaily,
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(officialPick(domain.Hard), errors.New("Too Many Requests: retry after 5")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(ctx, tt.storeMock(ctrl), tt.lcMock(ctrl), tt.schedMock(ctrl), tt.outMock(ctrl), clock)

			s.SendToday(ctx, 100)
		})
	}
}

// TestSendTodayConcurrent runs concurrent sends to one chat whose daily is not
// subscribed, as a cron send and /today can be, against a store that applies
// Update atomically and a slow FetchRandom that draws a new problem per call:
// whoever commits first wins, and every send carries that pick.
func TestSendTodayConcurrent(t *testing.T) {
	ctx := t.Context()
	const callers = 8

	var (
		mu     sync.Mutex
		stored domain.Chat   // the store's chat; guarded by mu
		sent   []domain.Pick // every SendProblem pick; guarded by mu
	)

	tests := []struct {
		name      string
		initial   domain.Chat
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		lcMock    func(*gomock.Controller) *mocks.MockproblemSource
		outMock   func(*gomock.Controller) *mocks.Mockmessenger
	}{
		{
			name:    "every caller sends the first committed pick",
			initial: subscribed(domain.Easy),
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).
					DoAndReturn(func(context.Context, int64) (domain.Chat, bool, error) {
						mu.Lock()
						defer mu.Unlock()
						return cloneChat(stored), true, nil
					}).Times(callers)
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ int64, fn func(*domain.Chat) bool) (bool, error) {
						mu.Lock()
						defer mu.Unlock()
						c := cloneChat(stored)
						if fn(&c) {
							stored = cloneChat(c)
						}
						return true, nil
					}).MinTimes(1).MaxTimes(callers)
				return m
			},
			lcMock: func(ctrl *gomock.Controller) *mocks.MockproblemSource {
				m := mocks.NewMockproblemSource(ctrl)
				m.EXPECT().FetchDaily(gomock.Eq(ctx)).Return(dailyOf(domain.Hard), nil).Times(callers)
				var draws atomic.Int64
				m.EXPECT().FetchRandom(gomock.Eq(ctx), gomock.Eq([]string{domain.Easy})).
					DoAndReturn(func(context.Context, []string) (domain.Problem, error) {
						time.Sleep(50 * time.Millisecond) // lets the other callers read the chat before anyone saves
						n := strconv.FormatInt(draws.Add(1), 10)
						return domain.Problem{ID: n, Title: "Random " + n, Link: "/problems/random-" + n + "/", Difficulty: domain.Easy}, nil
					}).MinTimes(1).MaxTimes(callers)
				return m
			},
			outMock: func(ctrl *gomock.Controller) *mocks.Mockmessenger {
				m := mocks.NewMockmessenger(ctrl)
				m.EXPECT().SendProblem(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ int64, p domain.Pick) error {
						mu.Lock()
						defer mu.Unlock()
						sent = append(sent, p)
						return nil
					}).Times(callers)
				return m
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			stored, sent = tt.initial, nil
			s := New(ctx, tt.storeMock(ctrl), tt.lcMock(ctrl), mocks.NewMockdailyScheduler(ctrl), tt.outMock(ctrl), clock)

			var wg sync.WaitGroup
			for range callers {
				wg.Go(func() { s.SendToday(ctx, 100) })
			}
			wg.Wait()

			if stored.DailyPick == nil {
				t.Fatal("no pick was saved")
			}
			for i, p := range sent {
				if !reflect.DeepEqual(p, *stored.DailyPick) {
					t.Errorf("send %d: got %+v, want the saved pick %+v", i, p, *stored.DailyPick)
				}
			}
		})
	}
}

func TestSendDaily(t *testing.T) {
	ctx := t.Context()

	hardDaily := func(ctrl *gomock.Controller) *mocks.MockproblemSource {
		m := mocks.NewMockproblemSource(ctrl)
		m.EXPECT().FetchDaily(gomock.Eq(ctx)).Return(dailyOf(domain.Hard), nil)
		return m
	}
	fetchError := func(ctrl *gomock.Controller) *mocks.MockproblemSource {
		m := mocks.NewMockproblemSource(ctrl)
		m.EXPECT().FetchDaily(gomock.Eq(ctx)).Return(domain.Problem{}, errors.New("timeout"))
		return m
	}

	tests := []struct {
		name      string
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		lcMock    func(*gomock.Controller) *mocks.MockproblemSource
		schedMock func(*gomock.Controller) *mocks.MockdailyScheduler
		outMock   func(*gomock.Controller) *mocks.Mockmessenger
	}{
		{
			// The bare store proves the pick is never read or written.
			name:      "official daily is sent without touching the store",
			storeMock: mocks.NewMockchatStore,
			lcMock:    hardDaily,
			schedMock: mocks.NewMockdailyScheduler,
			outMock: func(ctrl *gomock.Controller) *mocks.Mockmessenger {
				m := mocks.NewMockmessenger(ctrl)
				m.EXPECT().SendProblem(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Eq(domain.Pick{Problem: dailyOf(domain.Hard)})).
					Return(nil)
				return m
			},
		},
		{
			name:      "fetch failure sends fetch-failed",
			storeMock: mocks.NewMockchatStore,
			lcMock:    fetchError,
			schedMock: mocks.NewMockdailyScheduler,
			outMock: func(ctrl *gomock.Controller) *mocks.Mockmessenger {
				m := mocks.NewMockmessenger(ctrl)
				m.EXPECT().SendFetchFailed(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(nil)
				return m
			},
		},
		{
			name:      "fetch-failed send error is only logged",
			storeMock: mocks.NewMockchatStore,
			lcMock:    fetchError,
			schedMock: mocks.NewMockdailyScheduler,
			outMock: func(ctrl *gomock.Controller) *mocks.Mockmessenger {
				m := mocks.NewMockmessenger(ctrl)
				m.EXPECT().SendFetchFailed(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(errors.New("Bad Gateway"))
				return m
			},
		},
		{
			name: "blocked chat is unsubscribed",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Delete(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(nil)
				return m
			},
			lcMock: hardDaily,
			schedMock: func(ctrl *gomock.Controller) *mocks.MockdailyScheduler {
				m := mocks.NewMockdailyScheduler(ctrl)
				m.EXPECT().Remove(gomock.Eq(int64(100)))
				return m
			},
			outMock: func(ctrl *gomock.Controller) *mocks.Mockmessenger {
				m := mocks.NewMockmessenger(ctrl)
				m.EXPECT().SendProblem(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Eq(domain.Pick{Problem: dailyOf(domain.Hard)})).
					Return(fmt.Errorf("x: %w", domain.ErrBlocked))
				return m
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(ctx, tt.storeMock(ctrl), tt.lcMock(ctrl), tt.schedMock(ctrl), tt.outMock(ctrl), clock)

			s.SendDaily(ctx, 100)
		})
	}
}

// rootKey marks the jobs root, so a port can tell a job's ctx derives from it.
type rootKey struct{}

func TestJob(t *testing.T) {
	ctx := t.Context()
	live := context.WithValue(ctx, rootKey{}, "jobs")
	cancelled, cancel := context.WithCancel(live)
	cancel()

	tests := []struct {
		name      string
		jobs      context.Context
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		lcMock    func(*gomock.Controller) *mocks.MockproblemSource
		outMock   func(*gomock.Controller) *mocks.Mockmessenger
	}{
		{
			name: "job sends today's problem under the jobs root with a jobTimeout deadline",
			jobs: live,
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(gomock.Any(), gomock.Eq(int64(100))).Return(domain.Chat{}, false, nil)
				return m
			},
			lcMock: func(ctrl *gomock.Controller) *mocks.MockproblemSource {
				m := mocks.NewMockproblemSource(ctrl)
				m.EXPECT().FetchDaily(gomock.Any()).DoAndReturn(func(jctx context.Context) (domain.Problem, error) {
					if jctx.Value(rootKey{}) != "jobs" {
						ctrl.T.Errorf("job ctx does not derive from the jobs root")
					}
					deadline, ok := jctx.Deadline()
					if left := time.Until(deadline); !ok || left <= 0 || left > jobTimeout {
						ctrl.T.Errorf("job ctx deadline: set=%v, %v left; want within %v", ok, left, jobTimeout)
					}
					return dailyOf(domain.Hard), nil
				})
				return m
			},
			outMock: func(ctrl *gomock.Controller) *mocks.Mockmessenger {
				m := mocks.NewMockmessenger(ctrl)
				m.EXPECT().SendProblem(gomock.Any(), gomock.Eq(int64(100)), gomock.Eq(officialPick(domain.Hard))).Return(nil)
				return m
			},
		},
		{
			name:      "cancelled jobs root reaches the ports as context.Canceled",
			jobs:      cancelled,
			storeMock: mocks.NewMockchatStore,
			lcMock: func(ctrl *gomock.Controller) *mocks.MockproblemSource {
				m := mocks.NewMockproblemSource(ctrl)
				m.EXPECT().FetchDaily(gomock.Any()).DoAndReturn(func(jctx context.Context) (domain.Problem, error) {
					if !errors.Is(jctx.Err(), context.Canceled) {
						ctrl.T.Errorf("FetchDaily ctx.Err() = %v, want %v", jctx.Err(), context.Canceled)
					}
					return domain.Problem{}, jctx.Err()
				})
				return m
			},
			outMock: func(ctrl *gomock.Controller) *mocks.Mockmessenger {
				m := mocks.NewMockmessenger(ctrl)
				m.EXPECT().SendFetchFailed(gomock.Any(), gomock.Eq(int64(100))).DoAndReturn(func(jctx context.Context, _ int64) error {
					if !errors.Is(jctx.Err(), context.Canceled) {
						ctrl.T.Errorf("SendFetchFailed ctx.Err() = %v, want %v", jctx.Err(), context.Canceled)
					}
					return jctx.Err()
				})
				return m
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(tt.jobs, tt.storeMock(ctrl), tt.lcMock(ctrl), mocks.NewMockdailyScheduler(ctrl), tt.outMock(ctrl), clock)

			s.job(100)()
		})
	}
}
```

- [ ] **Step 4: Write the service tests**

This covers `TestRestore`, `TestSubscribe` (the captured job runs in the loop body and performs a SendToday), `TestSetDifficulties`, `TestUnsubscribe` (Remove before Delete, chained with `.After`; the loop body builds the scheduler mock first), `TestSubscription` and `TestSolve`. Create `internal/notifier/service_test.go`:

```go
package notifier

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/notifier/mocks"
)

// applyUpsert stands in for chatStore.Upsert of a stored chat (Chat{ChatID}
// for a new one): it applies fn to a copy of stored, as the store does, and
// fails the test unless fn leaves want.
func applyUpsert(ctrl *gomock.Controller, stored, want domain.Chat) func(context.Context, int64, func(*domain.Chat)) error {
	return func(_ context.Context, _ int64, fn func(*domain.Chat)) error {
		c := cloneChat(stored)
		fn(&c)
		if !reflect.DeepEqual(c, want) {
			ctrl.T.Errorf("Upsert left\n %+v (pick %+v)\nwant\n %+v (pick %+v)", c, c.DailyPick, want, want.DailyPick)
		}
		return nil
	}
}

func TestRestore(t *testing.T) {
	ctx := t.Context()
	chats := []domain.Chat{
		{ChatID: -100500, NotifyTime: "21:15", Timezone: "Asia/Tbilisi"},
		{ChatID: 100, NotifyTime: "09:00", Timezone: "UTC"},
	}

	all := func(ctrl *gomock.Controller) *mocks.MockchatStore {
		m := mocks.NewMockchatStore(ctrl)
		m.EXPECT().All(gomock.Eq(ctx)).Return(chats, nil)
		return m
	}
	// schedules expects both chats in All's order, returning groupErr and privateErr.
	schedules := func(groupErr, privateErr error) func(*gomock.Controller) *mocks.MockdailyScheduler {
		return func(ctrl *gomock.Controller) *mocks.MockdailyScheduler {
			m := mocks.NewMockdailyScheduler(ctrl)
			gomock.InOrder(
				m.EXPECT().Schedule(gomock.Eq(int64(-100500)), gomock.Eq("21:15"), gomock.Eq("Asia/Tbilisi"), gomock.Any()).Return(groupErr),
				m.EXPECT().Schedule(gomock.Eq(int64(100)), gomock.Eq("09:00"), gomock.Eq("UTC"), gomock.Any()).Return(privateErr),
			)
			return m
		}
	}

	tests := []struct {
		name      string
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		schedMock func(*gomock.Controller) *mocks.MockdailyScheduler
		wantErr   string
	}{
		{
			name:      "every chat is scheduled",
			storeMock: all,
			schedMock: schedules(nil, nil),
		},
		{
			name:      "a failed chat is reported and the next one still scheduled",
			storeMock: all,
			schedMock: schedules(errors.New(`invalid time "9am"`), nil),
			wantErr:   `restore schedule for -100500: invalid time "9am"`,
		},
		{
			name:      "every failure is joined",
			storeMock: all,
			schedMock: schedules(errors.New("bad group spec"), errors.New("bad private spec")),
			wantErr:   "restore schedule for -100500: bad group spec\nrestore schedule for 100: bad private spec",
		},
		{
			name: "All error is returned",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().All(gomock.Eq(ctx)).Return(nil, context.Canceled)
				return m
			},
			schedMock: mocks.NewMockdailyScheduler,
			wantErr:   "context canceled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(ctx, tt.storeMock(ctrl), mocks.NewMockproblemSource(ctrl), tt.schedMock(ctrl), mocks.NewMockmessenger(ctrl), clock)

			err := s.Restore(ctx)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tt.wantErr {
				t.Errorf("Restore() error = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

func TestSubscribe(t *testing.T) {
	ctx := t.Context()
	var job func() // the job handed to Schedule, captured by the sched factory
	all := []string{domain.Easy, domain.Medium, domain.Hard}
	members := map[string]domain.Member{"7": {Name: "Alice", Count: 3, LastSolvedDate: "2026-09-27"}}
	pick := randomPick(twoSum(), dailyDate)
	errSched := errors.New(`invalid time "0900"`)

	schedules := func(err error) func(*gomock.Controller) *mocks.MockdailyScheduler {
		return func(ctrl *gomock.Controller) *mocks.MockdailyScheduler {
			m := mocks.NewMockdailyScheduler(ctrl)
			m.EXPECT().Schedule(gomock.Eq(int64(100)), gomock.Eq("09:00"), gomock.Eq("UTC"), gomock.Any()).Return(err)
			return m
		}
	}

	tests := []struct {
		name         string
		difficulties []string
		storeMock    func(*gomock.Controller) *mocks.MockchatStore
		lcMock       func(*gomock.Controller) *mocks.MockproblemSource
		schedMock    func(*gomock.Controller) *mocks.MockdailyScheduler
		outMock      func(*gomock.Controller) *mocks.Mockmessenger
		wantErrs     []error
	}{
		{
			name:         "new chat is saved and scheduled, and its job sends today's problem",
			difficulties: all,
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Upsert(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).
						DoAndReturn(applyUpsert(ctrl, domain.Chat{ChatID: 100}, subscribed(all...))),
					// The job's ctx derives from the jobs root, so it is matched with Any.
					m.EXPECT().Get(gomock.Any(), gomock.Eq(int64(100))).Return(subscribed(all...), true, nil),
				)
				return m
			},
			lcMock: func(ctrl *gomock.Controller) *mocks.MockproblemSource {
				m := mocks.NewMockproblemSource(ctrl)
				m.EXPECT().FetchDaily(gomock.Any()).Return(dailyOf(domain.Hard), nil)
				return m
			},
			schedMock: func(ctrl *gomock.Controller) *mocks.MockdailyScheduler {
				m := mocks.NewMockdailyScheduler(ctrl)
				m.EXPECT().Schedule(gomock.Eq(int64(100)), gomock.Eq("09:00"), gomock.Eq("UTC"), gomock.Any()).
					DoAndReturn(func(_ int64, _, _ string, j func()) error {
						job = j
						return nil
					})
				return m
			},
			outMock: func(ctrl *gomock.Controller) *mocks.Mockmessenger {
				m := mocks.NewMockmessenger(ctrl)
				m.EXPECT().SendProblem(gomock.Any(), gomock.Eq(int64(100)), gomock.Eq(officialPick(domain.Hard))).Return(nil)
				return m
			},
		},
		{
			name:         "existing chat keeps its members and pick",
			difficulties: []string{domain.Easy},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				stored := domain.Chat{ChatID: 100, NotifyTime: "07:00", Timezone: "Asia/Tbilisi", Difficulties: all}
				m.EXPECT().Upsert(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(applyUpsert(ctrl,
					withPick(withMembers(stored, members), pick),
					withPick(withMembers(subscribed(domain.Easy), members), pick),
				))
				return m
			},
			lcMock:    mocks.NewMockproblemSource,
			schedMock: schedules(nil),
			outMock:   mocks.NewMockmessenger,
		},
		{
			name:         "store error still schedules and is returned",
			difficulties: []string{domain.Easy},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Upsert(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(errDisk)
				return m
			},
			lcMock:    mocks.NewMockproblemSource,
			schedMock: schedules(nil),
			outMock:   mocks.NewMockmessenger,
			wantErrs:  []error{errDisk},
		},
		{
			name:         "store and schedule errors are joined",
			difficulties: []string{domain.Easy},
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Upsert(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(errDisk)
				return m
			},
			lcMock:    mocks.NewMockproblemSource,
			schedMock: schedules(errSched),
			outMock:   mocks.NewMockmessenger,
			wantErrs:  []error{errDisk, errSched},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			job = nil
			s := New(ctx, tt.storeMock(ctrl), tt.lcMock(ctrl), tt.schedMock(ctrl), tt.outMock(ctrl), clock)

			err := s.Subscribe(ctx, 100, "09:00", "UTC", tt.difficulties)
			if len(tt.wantErrs) == 0 && err != nil {
				t.Errorf("Subscribe() error = %v, want nil", err)
			}
			for _, want := range tt.wantErrs {
				if !errors.Is(err, want) {
					t.Errorf("Subscribe() error = %v, want it to wrap %v", err, want)
				}
			}
			if job != nil {
				job()
			}
		})
	}
}

func TestSetDifficulties(t *testing.T) {
	ctx := t.Context()
	pick := randomPick(twoSum(), dailyDate)

	tests := []struct {
		name      string
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		wantErr   error
	}{
		{
			name: "subscribed chat gets the set and keeps its pick",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(applyUpdate(ctrl,
					withPick(subscribed(domain.Easy), pick),
					withPick(subscribed(domain.Medium, domain.Hard), pick),
					true,
				))
				return m
			},
		},
		{
			name: "missing chat is ErrNotSubscribed",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(false, nil)
				return m
			},
			wantErr: domain.ErrNotSubscribed,
		},
		{
			name: "store error is returned as is",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(true, errDisk)
				return m
			},
			wantErr: errDisk,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(ctx, tt.storeMock(ctrl), mocks.NewMockproblemSource(ctrl), mocks.NewMockdailyScheduler(ctrl), mocks.NewMockmessenger(ctrl), clock)

			if err := s.SetDifficulties(ctx, 100, []string{domain.Medium, domain.Hard}); !errors.Is(err, tt.wantErr) {
				t.Errorf("SetDifficulties() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestUnsubscribe(t *testing.T) {
	ctx := t.Context()
	var removed *gomock.Call // Remove's expectation; Delete must come after it

	removes := func(ctrl *gomock.Controller) *mocks.MockdailyScheduler {
		m := mocks.NewMockdailyScheduler(ctrl)
		removed = m.EXPECT().Remove(gomock.Eq(int64(100)))
		return m
	}

	tests := []struct {
		name      string
		schedMock func(*gomock.Controller) *mocks.MockdailyScheduler
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		wantErr   error
	}{
		{
			name:      "entry is removed, then the chat deleted",
			schedMock: removes,
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Delete(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(nil).After(removed)
				return m
			},
		},
		{
			name:      "delete error is returned",
			schedMock: removes,
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Delete(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(errDisk).After(removed)
				return m
			},
			wantErr: errDisk,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			sched := tt.schedMock(ctrl) // first: the store factory chains after removed
			store := tt.storeMock(ctrl)
			s := New(ctx, store, mocks.NewMockproblemSource(ctrl), sched, mocks.NewMockmessenger(ctrl), clock)

			if err := s.Unsubscribe(ctx, 100); !errors.Is(err, tt.wantErr) {
				t.Errorf("Unsubscribe() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSubscription(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name      string
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		wantChat  domain.Chat
		wantOK    bool
		wantErr   error
	}{
		{
			name: "subscribed chat",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(subscribed(domain.Easy), true, nil)
				return m
			},
			wantChat: subscribed(domain.Easy),
			wantOK:   true,
		},
		{
			name: "missing chat",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(domain.Chat{}, false, nil)
				return m
			},
		},
		{
			name: "store error",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(domain.Chat{}, false, context.Canceled)
				return m
			},
			wantErr: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(ctx, tt.storeMock(ctrl), mocks.NewMockproblemSource(ctrl), mocks.NewMockdailyScheduler(ctrl), mocks.NewMockmessenger(ctrl), clock)

			c, ok, err := s.Subscription(ctx, 100)
			if !reflect.DeepEqual(c, tt.wantChat) || ok != tt.wantOK || !errors.Is(err, tt.wantErr) {
				t.Errorf("Subscription() = %+v, %v, %v; want %+v, %v, %v", c, ok, err, tt.wantChat, tt.wantOK, tt.wantErr)
			}
		})
	}
}

func TestSolve(t *testing.T) {
	ctx := t.Context()
	// alice is Alice's (user 7) stats; dailyDate is clock's UTC day.
	alice := func(count int, day string) map[string]domain.Member {
		return map[string]domain.Member{"7": {Name: "Alice", Count: count, LastSolvedDate: day}}
	}

	tests := []struct {
		name      string
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		wantTotal int
		wantErr   error
	}{
		{
			name: "first solve is counted on the UTC day",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(applyUpdate(ctrl,
					subscribed(),
					withMembers(subscribed(), alice(1, dailyDate)),
					true,
				))
				return m
			},
			wantTotal: 1,
		},
		{
			name: "next day is counted",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(applyUpdate(ctrl,
					withMembers(subscribed(), alice(5, "2026-09-27")),
					withMembers(subscribed(), alice(6, dailyDate)),
					true,
				))
				return m
			},
			wantTotal: 6,
		},
		{
			name: "second solve the same day is refused with the total",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(applyUpdate(ctrl,
					withMembers(subscribed(), alice(3, dailyDate)),
					withMembers(subscribed(), alice(3, dailyDate)),
					false,
				))
				return m
			},
			wantTotal: 3,
			wantErr:   domain.ErrAlreadySolved,
		},
		{
			name: "missing chat is ErrNotSubscribed",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(false, nil)
				return m
			},
			wantErr: domain.ErrNotSubscribed,
		},
		{
			name: "write error returns the applied total",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ int64, fn func(*domain.Chat) bool) (bool, error) {
						c := subscribed()
						fn(&c)
						return true, errDisk
					})
				return m
			},
			wantTotal: 1,
			wantErr:   errDisk,
		},
		{
			name: "refused call returns 0",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(false, context.Canceled)
				return m
			},
			wantErr: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(ctx, tt.storeMock(ctrl), mocks.NewMockproblemSource(ctrl), mocks.NewMockdailyScheduler(ctrl), mocks.NewMockmessenger(ctrl), clock)

			total, err := s.Solve(ctx, 100, 7, "Alice")
			if total != tt.wantTotal || !errors.Is(err, tt.wantErr) {
				t.Errorf("Solve() = %d, %v; want %d, %v", total, err, tt.wantTotal, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 5: Run the tests to see them fail**

Run: `go test ./internal/notifier/`
Expected:
```
# github.com/solympe/leetcode-tg-notifier/internal/notifier [github.com/solympe/leetcode-tg-notifier/internal/notifier.test]
internal/notifier/delivery_test.go:395:9: undefined: New
internal/notifier/delivery_test.go:476:9: undefined: New
internal/notifier/delivery_test.go:577:9: undefined: New
internal/notifier/delivery_test.go:615:66: undefined: jobTimeout
internal/notifier/delivery_test.go:616:84: undefined: jobTimeout
internal/notifier/delivery_test.go:658:9: undefined: New
internal/notifier/service_test.go:91:9: undefined: New
internal/notifier/service_test.go:211:9: undefined: New
internal/notifier/service_test.go:273:9: undefined: New
internal/notifier/service_test.go:324:9: undefined: New
internal/notifier/service_test.go:324:9: too many errors
FAIL	github.com/solympe/leetcode-tg-notifier/internal/notifier [build failed]
FAIL
```

- [ ] **Step 6: Create the service**

Create `internal/notifier/service.go`:

```go
// Package notifier holds the use cases: subscriptions, solves and the problem
// of the day. It talks to the outside world only through the ports in deps.go.
package notifier

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// jobTimeout bounds one scheduled send.
const jobTimeout = 2 * time.Minute

type service struct {
	jobs  context.Context // parent of every scheduled job's ctx; owned and cancelled by app
	store chatStore
	lc    problemSource
	sched dailyScheduler
	out   messenger
	now   func() time.Time
}

func New(jobs context.Context, store chatStore, lc problemSource, sched dailyScheduler, out messenger, now func() time.Time) *service {
	return &service{jobs: jobs, store: store, lc: lc, sched: sched, out: out, now: now}
}

// Restore schedules every stored chat. A chat that fails to schedule is
// reported in the joined error and does not stop the others.
func (s *service) Restore(ctx context.Context) error {
	chats, err := s.store.All(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range chats {
		if err := s.sched.Schedule(c.ChatID, c.NotifyTime, c.Timezone, s.job(c.ChatID)); err != nil {
			errs = append(errs, fmt.Errorf("restore schedule for %d: %w", c.ChatID, err))
		}
	}
	return errors.Join(errs...)
}

// Subscribe saves chatID's time, zone and difficulties, keeping its members
// and pick of the day, and (re)schedules its daily job even if the save
// failed.
func (s *service) Subscribe(ctx context.Context, chatID int64, notifyTime, timezone string, difficulties []string) error {
	saveErr := s.store.Upsert(ctx, chatID, func(c *domain.Chat) {
		c.NotifyTime = notifyTime
		c.Timezone = timezone
		c.Difficulties = difficulties
	})
	schedErr := s.sched.Schedule(chatID, notifyTime, timezone, s.job(chatID))
	return errors.Join(saveErr, schedErr)
}

// SetDifficulties replaces a subscribed chat's difficulties and never creates
// a chat. The pick of the day is kept: the next send re-validates it.
func (s *service) SetDifficulties(ctx context.Context, chatID int64, difficulties []string) error {
	found, err := s.store.Update(ctx, chatID, func(c *domain.Chat) bool {
		c.Difficulties = difficulties
		return true
	})
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrNotSubscribed
	}
	return nil
}

// Unsubscribe removes chatID's daily job, then its stored chat.
func (s *service) Unsubscribe(ctx context.Context, chatID int64) error {
	s.sched.Remove(chatID)
	return s.store.Delete(ctx, chatID)
}

// Subscription returns chatID's stored chat; false means it is not subscribed.
func (s *service) Subscription(ctx context.Context, chatID int64) (domain.Chat, bool, error) {
	return s.store.Get(ctx, chatID)
}

// Solve counts userID's solve for today (UTC) in one atomic update and
// returns the member's total. On a storage error the total is at least 1 when
// the count was applied before a failed write, and 0 when the call was
// refused.
func (s *service) Solve(ctx context.Context, chatID, userID int64, name string) (int, error) {
	day := s.now().UTC().Format(time.DateOnly)
	var (
		total   int
		counted bool
	)
	found, err := s.store.Update(ctx, chatID, func(c *domain.Chat) bool {
		total, counted = c.RecordSolve(userID, name, day)
		return counted
	})
	switch {
	case err != nil:
		return total, err
	case !found:
		return 0, domain.ErrNotSubscribed
	case !counted:
		return total, domain.ErrAlreadySolved
	}
	return total, nil
}

// SendToday sends chatID today's problem: the cron job, /today and the 📅 button.
func (s *service) SendToday(ctx context.Context, chatID int64) {
	p, err := s.today(ctx, chatID)
	s.deliver(ctx, chatID, p, err)
}

// SendDaily sends chatID the official daily whatever its difficulties: /daily
// and the 🗓 button. It never reads or writes the store, so it also works for
// an unsubscribed chat.
func (s *service) SendDaily(ctx context.Context, chatID int64) {
	daily, err := s.lc.FetchDaily(ctx)
	if err != nil {
		err = fmt.Errorf("FetchDaily: %w", err)
	}
	s.deliver(ctx, chatID, domain.Pick{Problem: daily}, err)
}
```

- [ ] **Step 7: Create the delivery policy and the compare-and-set pick**

`today` is spec §6.2 verbatim. `deliver` sends the fetch-failed notice on any fetch or store-read error, and unsubscribes on `domain.ErrBlocked`. Create `internal/notifier/delivery.go`:

```go
package notifier

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// job is chatID's scheduled send. Its ctx derives from the jobs root, so it is
// bounded by jobTimeout and cancelled with the root.
func (s *service) job(chatID int64) func() {
	return func() {
		ctx, cancel := context.WithTimeout(s.jobs, jobTimeout)
		defer cancel()
		s.SendToday(ctx, chatID)
	}
}

// today returns what chatID gets today: the daily when the chat is not
// subscribed or takes its difficulty, otherwise a random problem of a
// subscribed difficulty, picked once per daily date. The pick is saved by
// compare-and-set: the first committed pick wins and every concurrent loser
// sends it, so all sends to a chat agree. An error means there is nothing to
// send.
func (s *service) today(ctx context.Context, chatID int64) (domain.Pick, error) {
	daily, err := s.lc.FetchDaily(ctx)
	if err != nil {
		return domain.Pick{}, fmt.Errorf("FetchDaily: %w", err)
	}
	c, ok, err := s.store.Get(ctx, chatID)
	if err != nil {
		return domain.Pick{}, fmt.Errorf("store.Get: %w", err)
	}
	if !ok || c.Wants(daily.Difficulty) {
		return domain.Pick{Problem: daily}, nil
	}
	if p, ok := c.PickFor(daily); ok {
		return p, nil
	}

	r, err := s.lc.FetchRandom(ctx, c.Difficulties)
	if err != nil {
		return domain.Pick{}, fmt.Errorf("FetchRandom: %w", err)
	}
	r.Date = daily.Date
	pick := domain.Pick{Problem: r, DailyDifficulty: daily.Difficulty}
	if _, err := s.store.Update(ctx, chatID, func(cur *domain.Chat) bool {
		if won, ok := cur.PickFor(daily); ok { // someone committed first: adopt theirs
			pick = won
			return false
		}
		saved := pick
		cur.DailyPick = &saved
		return true
	}); err != nil {
		log.Printf("save pick for %d: %v", chatID, err) // the pick is still sent
	}
	return pick, nil
}

// deliver sends p, or the fetch-failed notice when err is set. A chat that
// blocked the bot loses its subscription; any other send error is only logged.
func (s *service) deliver(ctx context.Context, chatID int64, p domain.Pick, err error) {
	if err != nil {
		log.Printf("problem for %d: %v", chatID, err)
		if err := s.out.SendFetchFailed(ctx, chatID); err != nil {
			log.Printf("send fetch-failed to %d: %v", chatID, err)
		}
		return
	}
	if err := s.out.SendProblem(ctx, chatID, p); err != nil {
		log.Printf("send problem to %d: %v", chatID, err)
		if errors.Is(err, domain.ErrBlocked) {
			s.dropBlocked(ctx, chatID)
		}
	}
}

// dropBlocked removes the subscription of a chat that blocked the bot.
func (s *service) dropBlocked(ctx context.Context, chatID int64) {
	log.Printf("bot blocked by %d: removing subscription", chatID)
	if err := s.Unsubscribe(ctx, chatID); err != nil {
		log.Printf("unsubscribe %d: %v", chatID, err)
	}
}
```

- [ ] **Step 8: Re-run the notifier tests**

Run: `go test ./internal/notifier/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected:
```
--- PASS: TestSendToday (0.00s)
--- PASS: TestSendTodayConcurrent (0.05s)
--- PASS: TestSendDaily (0.00s)
--- PASS: TestJob (0.00s)
--- PASS: TestRestore (0.00s)
--- PASS: TestSubscribe (0.00s)
--- PASS: TestSetDifficulties (0.00s)
--- PASS: TestUnsubscribe (0.00s)
--- PASS: TestSubscription (0.00s)
--- PASS: TestSolve (0.00s)
ok  	github.com/solympe/leetcode-tg-notifier/internal/notifier	1.3s
```
The timings differ. `TestSendToday` has 19 rows, `TestSendDaily` 4, `TestJob` 2, `TestRestore` 4, `TestSubscribe` 4, `TestSetDifficulties` 3, `TestUnsubscribe` 2, `TestSubscription` 3 and `TestSolve` 6.

- [ ] **Step 9: Check that the concurrency row is stable under `-race`**

Run: `go test ./internal/notifier/ -race -count=5 -run 'TestSendToday'`
Expected: `ok  	github.com/solympe/leetcode-tg-notifier/internal/notifier`

- [ ] **Step 10: Task gate**

Run:
```bash
go build ./... && go test ./... -race -count=1 && make lint && make test-integration
go list -deps . | grep -E 'internal/(domain|notifier)$'; echo "linked: exit=$?"
go list -f '{{join .Imports " "}}' ./internal/notifier ./internal/notifier/mocks
```
Expected: `go test` prints `ok` for `internal/bot`, `internal/domain`, `internal/leetcode`, `internal/notifier`, `internal/scheduler` and `internal/storage`, and `[no test files]` for the root, `internal/app` (its suite is tagged) and the three `mocks` packages. `make lint` prints `0 issues.` `make test-integration` prints the same, plus `ok` for `internal/app`: rows 1-16, 20-21 and `TestNewFails` pass unchanged. Then comes `linked: exit=1`, and then the import lists
```
context errors fmt github.com/solympe/leetcode-tg-notifier/internal/domain log time
context github.com/solympe/leetcode-tg-notifier/internal/domain go.uber.org/mock/gomock reflect
```
This shows that the mocks do not import `notifier`, so there is no test import cycle (spec §3.2).

- [ ] **Step 11: Commit**

```bash
git add internal/notifier/deps.go internal/notifier/service.go internal/notifier/delivery.go internal/notifier/mocks/mock_deps.go internal/notifier/service_test.go internal/notifier/delivery_test.go
git commit -F - <<'EOF'
refactor(notifier): add the use-case core behind ctx-first ports

internal/notifier declares its ports in deps.go (chatStore,
problemSource, dailyScheduler, messenger) and mocks them with mockgen.
The service subscribes, restores, counts solves and sends the problem
of the day. The pick of the day is saved by compare-and-set in one
store Update, which replaces the per-chat lock: the first committed
pick wins and every concurrent send carries it. Scheduled jobs derive
their ctx from the jobs root with a 2-minute budget, and a 403
(domain.ErrBlocked) unsubscribes the chat. Not wired yet.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [ ] **Step 12: Mocks check on the committed tree**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `CLEAN` and nothing else. If `internal/notifier/mocks/mock_deps.go` is listed, `git add` it and `git commit --amend --no-edit`.

---

### Task 8: `internal/telegram`: view, sender and the Bot API port

**Files:**
- Create: `internal/telegram/view_test.go`
- Create: `internal/telegram/view.go`
- Create: `internal/telegram/deps.go`
- Create (generated): `internal/telegram/mocks/mock_deps.go`
- Create: `internal/telegram/sender_test.go`
- Create: `internal/telegram/sender.go`

**Interfaces:** Consumes (Task 6): `domain.Pick` (`Problem`, `DailyDifficulty`), `domain.Problem` (`Date`, `ID`, `Title`, `Link`, `Difficulty`, `Tags`), `domain.Member` (`Name`, `Count`), `domain.Difficulties() []string` and `domain.ErrBlocked`. tgbotapi v5.5.1: `Chattable`, `Message`, `APIResponse`, `*Error` (`Code`), `NewMessage`, `NewEditMessageText`, `NewEditMessageReplyMarkup`, `NewCallback`, `NewInlineKeyboardMarkup`, `NewInlineKeyboardRow`, `NewInlineKeyboardButtonData`. `go.uber.org/mock` v0.6.0 is already in `go.mod`, and mockgen v0.6.0 comes from Task 1. Produces (package `github.com/solympe/leetcode-tg-notifier/internal/telegram`):
- `deps.go` with `//go:generate mockgen -source=deps.go -destination=mocks/mock_deps.go -package=mocks` and `type botAPI interface { Send(c tgbotapi.Chattable) (tgbotapi.Message, error); Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) }`. `*tgbotapi.BotAPI` satisfies it. The generated `mocks.NewMockbotAPI(ctrl) *mocks.MockbotAPI` imports tgbotapi and gomock, never `telegram`.
- `func NewSender(api botAPI) *sender` with `SendProblem(ctx context.Context, chatID int64, p domain.Pick) error` and `SendFetchFailed(ctx context.Context, chatID int64) error`. This is the method set of notifier's `messenger` (Task 7). On a 403, `SendProblem` returns `fmt.Errorf("%w: %w", domain.ErrBlocked, err)`.
- The private sender helpers that Task 9 uses: `send(ctx, chatID int64, text string, kb *tgbotapi.InlineKeyboardMarkup) (int, error)`, `text(ctx, chatID int64, text string)`, `edit(ctx, chatID int64, msgID int, text string, kb *tgbotapi.InlineKeyboardMarkup)` (a nil kb removes the keyboard), `editKeyboard(ctx, chatID int64, msgID int, kb *tgbotapi.InlineKeyboardMarkup)`, `answer(ctx, cbID, text string)` and `isBotBlocked(err error) bool`. Each one returns or logs `ctx.Err()` without calling the API once ctx is done.
- `view.go`: the constants `cmdSetup`, `cmdToday`, `cmdDaily`, `cmdStatus`, `cmdUnsubscribe`, `cmdRating`, `cmdDifficulty`, `cbDone`, `cbDiffSave`, `cbPrefixTime`, `cbPrefixTz`, `cbPrefixDiff`, `parseMode`, `msgFetchFailed` and `msgRatingEmpty`; the keyboards `startKeyboard()`, `doneKeyboard()`, `timeKeyboard()`, `tzKeyboard()` and `difficultyKeyboard(selected []string)`, each returning `*tgbotapi.InlineKeyboardMarkup`; `tzLabel(city, zone string) string`; `formatPick(domain.Pick) string`, `formatRating([]domain.Member) string` and `formatDifficulties([]string) string`; `validTime(string) bool` and `validTimezone(string) bool`; and `canonical`, `initialDifficulties` and `toggle`, which take and return `[]string`.
- Test fixtures in `sender_test.go` that Task 9's tests use: `const testChat = int64(100)`, `okResp`, `msgCfg(text string, kb *tgbotapi.InlineKeyboardMarkup) tgbotapi.MessageConfig` and `editCfg(msgID int, text string, kb *tgbotapi.InlineKeyboardMarkup) tgbotapi.EditMessageTextConfig`.

`cmdStart`, `cmdAbout` and the other texts are added in Task 9, together with the code that uses them: the `unused` linter rejects a constant that nothing reads. Nothing imports the package after this task.

- [ ] **Step 1: Write the view tests**

The `TestFormatPick` rows are the rows of `internal/leetcode/format_test.go`, copied verbatim. `FormatProblem(p)` becomes `formatPick(Pick{Problem: p})`, and `FormatRandomProblem(p, d)` becomes `formatPick(Pick{Problem: p, DailyDifficulty: d})`. Task 10 deletes `format_test.go`. The tz keyboard row spells out the labels of the three zones that have no DST and takes the other three from `tzLabel`. Create `internal/telegram/view_test.go`:

```go
package telegram

import (
	"reflect"
	"slices"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// The rows are copied from the old leetcode/format_test.go: FormatProblem(p)
// is formatPick(Pick{Problem: p}), FormatRandomProblem(p, d) is
// formatPick(Pick{Problem: p, DailyDifficulty: d}).
func TestFormatPick(t *testing.T) {
	tests := []struct {
		name string
		p    domain.Pick
		want string
	}{
		{
			name: "daily: valid date",
			p: domain.Pick{Problem: domain.Problem{
				Date:       "2026-03-03",
				Link:       "/problems/two-sum/",
				ID:         "1",
				Title:      "Two Sum",
				Difficulty: "Easy",
				Tags:       []string{"Array", "Hash Table"},
			}},
			want: "📅 LeetCode Daily — March 3, 2026\n\n" +
				"🔢 1. Two Sum\n💪 Difficulty: Easy\n🏷 Array, Hash Table\n\n" +
				"🔗 https://leetcode.com/problems/two-sum/",
		},
		{
			name: "daily: unparseable date falls back to raw string",
			p: domain.Pick{Problem: domain.Problem{
				Date:       "someday",
				Link:       "/problems/lru-cache/",
				ID:         "146",
				Title:      "LRU Cache",
				Difficulty: "Medium",
				Tags:       []string{"Design"},
			}},
			want: "📅 LeetCode Daily — someday\n\n" +
				"🔢 146. LRU Cache\n💪 Difficulty: Medium\n🏷 Design\n\n" +
				"🔗 https://leetcode.com/problems/lru-cache/",
		},
		{
			name: "random: valid date",
			p: domain.Pick{
				Problem: domain.Problem{
					Date:       "2026-09-27",
					Link:       "/problems/trapping-rain-water/",
					ID:         "42",
					Title:      "Trapping Rain Water",
					Difficulty: "Hard",
					Tags:       []string{"Array", "Two Pointers"},
				},
				DailyDifficulty: "Easy",
			},
			want: "🎲 LeetCode Random — September 27, 2026\n" +
				"Today's daily is Easy, so here's a random Hard problem for you.\n\n" +
				"🔢 42. Trapping Rain Water\n💪 Difficulty: Hard\n🏷 Array, Two Pointers\n\n" +
				"🔗 https://leetcode.com/problems/trapping-rain-water/",
		},
		{
			name: "random: unparseable date falls back to raw string",
			p: domain.Pick{
				Problem: domain.Problem{
					Date:       "27.09.2026",
					Link:       "/problems/add-two-numbers/",
					ID:         "2",
					Title:      "Add Two Numbers",
					Difficulty: "Medium",
					Tags:       []string{"Linked List", "Math"},
				},
				DailyDifficulty: "Hard",
			},
			want: "🎲 LeetCode Random — 27.09.2026\n" +
				"Today's daily is Hard, so here's a random Medium problem for you.\n\n" +
				"🔢 2. Add Two Numbers\n💪 Difficulty: Medium\n🏷 Linked List, Math\n\n" +
				"🔗 https://leetcode.com/problems/add-two-numbers/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPick(tt.p); got != tt.want {
				t.Errorf("formatPick():\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestFormatRating(t *testing.T) {
	tests := []struct {
		name      string
		standings []domain.Member
		want      string
	}{
		{
			name: "no members",
			want: "No solves yet. Be the first to press ✅ Done!",
		},
		{
			name:      "one member",
			standings: []domain.Member{{Name: "Alice", Count: 3}},
			want:      "🏆 Solved: <b>3</b>",
		},
		{
			name:      "medals",
			standings: []domain.Member{{Name: "Bob", Count: 3}, {Name: "Alice", Count: 2}, {Name: "Carol", Count: 2}},
			want:      "🏆 <b>Rating</b>\n\n🥇 Bob — 3\n🥈 Alice — 2\n🥉 Carol — 2\n",
		},
		{
			name: "4th place and later are numbered",
			standings: []domain.Member{
				{Name: "Bob", Count: 5}, {Name: "Alice", Count: 4}, {Name: "Carol", Count: 3},
				{Name: "Dave", Count: 2}, {Name: "@eve", Count: 1},
			},
			want: "🏆 <b>Rating</b>\n\n🥇 Bob — 5\n🥈 Alice — 4\n🥉 Carol — 3\n4. Dave — 2\n5. @eve — 1\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRating(tt.standings); got != tt.want {
				t.Errorf("formatRating():\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestFormatDifficulties(t *testing.T) {
	tests := []struct {
		name string
		ds   []string
		want string
	}{
		{name: "nil means any", ds: nil, want: "Any"},
		{name: "empty means any", ds: []string{}, want: "Any"},
		{name: "one", ds: []string{"Easy"}, want: "Easy"},
		{name: "several", ds: []string{"Easy", "Medium", "Hard"}, want: "Easy, Medium, Hard"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDifficulties(tt.ds); got != tt.want {
				t.Errorf("formatDifficulties(%v) = %q, want %q", tt.ds, got, tt.want)
			}
		})
	}
}

func TestKeyboards(t *testing.T) {
	type button struct{ label, data string }
	tests := []struct {
		name string
		kb   *tgbotapi.InlineKeyboardMarkup
		want [][]button
	}{
		{
			name: "start",
			kb:   startKeyboard(),
			want: [][]button{
				{{"📅 Today's problem", "/today"}, {"🗓 LeetCode daily", "/daily"}, {"⚙️ Setup", "/setup"}},
				{{"ℹ️ Status", "/status"}, {"🏆 Rating", "/rating"}},
				{{"🎚 Difficulty", "/difficulty"}, {"🛑 Unsubscribe", "/unsubscribe"}},
			},
		},
		{
			name: "done",
			kb:   doneKeyboard(),
			want: [][]button{{{"✅ Done", "done"}}},
		},
		{
			name: "time",
			kb:   timeKeyboard(),
			want: [][]button{
				{{"7:00", "time:07:00"}, {"8:00", "time:08:00"}, {"9:00", "time:09:00"}, {"10:00", "time:10:00"}},
				{{"18:00", "time:18:00"}, {"19:00", "time:19:00"}, {"20:00", "time:20:00"}, {"21:00", "time:21:00"}},
			},
		},
		{
			name: "timezone: DST zones are labelled by tzLabel",
			kb:   tzKeyboard(),
			want: [][]button{
				{{tzLabel("New York", "America/New_York"), "tz:America/New_York"}, {tzLabel("London", "Europe/London"), "tz:Europe/London"}},
				{{tzLabel("Lisbon", "Europe/Lisbon"), "tz:Europe/Lisbon"}, {"UTC+0", "tz:UTC"}},
				{{"UTC+3 Moscow", "tz:Europe/Moscow"}, {"UTC+4 Dubai", "tz:Asia/Dubai"}},
				{{"UTC+7 Bangkok", "tz:Asia/Bangkok"}},
			},
		},
		{
			name: "difficulty: nothing selected",
			kb:   difficultyKeyboard(nil),
			want: [][]button{
				{{"⬜ Easy", "diff:Easy"}, {"⬜ Medium", "diff:Medium"}, {"⬜ Hard", "diff:Hard"}},
				{{"💾 Save", "diffsave"}},
			},
		},
		{
			name: "difficulty: all selected",
			kb:   difficultyKeyboard([]string{"Easy", "Medium", "Hard"}),
			want: [][]button{
				{{"✅ Easy", "diff:Easy"}, {"✅ Medium", "diff:Medium"}, {"✅ Hard", "diff:Hard"}},
				{{"💾 Save", "diffsave"}},
			},
		},
		{
			name: "difficulty: canonical button order, unknown values ignored",
			kb:   difficultyKeyboard([]string{"Hard", "Extreme", "Easy"}),
			want: [][]button{
				{{"✅ Easy", "diff:Easy"}, {"⬜ Medium", "diff:Medium"}, {"✅ Hard", "diff:Hard"}},
				{{"💾 Save", "diffsave"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make([][]button, len(tt.kb.InlineKeyboard))
			for i, row := range tt.kb.InlineKeyboard {
				for _, b := range row {
					if b.CallbackData == nil {
						t.Fatalf("button %q has no callback data", b.Text)
					}
					got[i] = append(got[i], button{b.Text, *b.CallbackData})
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("keyboard:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestTzLabel(t *testing.T) {
	tests := []struct {
		name, city, zone, want string
	}{
		{name: "whole hours east", city: "Dubai", zone: "Asia/Dubai", want: "UTC+4 Dubai"},
		{name: "half-hour zone", city: "Kolkata", zone: "Asia/Kolkata", want: "UTC+5:30 Kolkata"},
		{name: "west of UTC", city: "Bogota", zone: "America/Bogota", want: "UTC-5 Bogota"},
		{name: "UTC", city: "UTC", zone: "UTC", want: "UTC+0 UTC"},
		{name: "unknown zone falls back to the city", city: "Mars", zone: "Mars/Base", want: "Mars"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tzLabel(tt.city, tt.zone); got != tt.want {
				t.Errorf("tzLabel(%q, %q) = %q, want %q", tt.city, tt.zone, got, tt.want)
			}
		})
	}
}

func TestValidTime(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"00:00", true},
		{"09:00", true},
		{"23:59", true},
		{"9:00", false},
		{"24:00", false},
		{"12:60", false},
		{"0900", false},
		{" 09:00", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := validTime(tt.in); got != tt.want {
				t.Errorf("validTime(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidTimezone(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"Europe/Moscow", true},
		{"America/New_York", true},
		{"UTC", true},
		{"", false},
		{"Local", false},
		{"Mars/Base", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := validTimezone(tt.in); got != tt.want {
				t.Errorf("validTimezone(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDifficultySets(t *testing.T) {
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{name: "canonical orders and drops unknown values", got: canonical([]string{"Hard", "Extreme", "Easy"}), want: []string{"Easy", "Hard"}},
		{name: "canonical of nil is empty", got: canonical(nil), want: nil},
		{name: "initial: nothing saved means all", got: initialDifficulties(nil), want: []string{"Easy", "Medium", "Hard"}},
		{name: "initial: the saved set, canonical", got: initialDifficulties([]string{"Hard", "Easy"}), want: []string{"Easy", "Hard"}},
		{name: "toggle adds in canonical order", got: toggle([]string{"Hard"}, "Easy"), want: []string{"Easy", "Hard"}},
		{name: "toggle adds to an empty set", got: toggle(nil, "Medium"), want: []string{"Medium"}},
		{name: "toggle removes", got: toggle([]string{"Easy", "Hard"}, "Hard"), want: []string{"Easy"}},
		{name: "toggle removes the last one", got: toggle([]string{"Easy"}, "Easy"), want: nil},
		{
			name: "toggle leaves its input alone",
			got: func() []string {
				in := []string{"Easy", "Hard"}
				toggle(in, "Easy")
				return in
			}(),
			want: []string{"Easy", "Hard"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Equal(tt.got, tt.want) {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the view tests to see them fail**

Run: `go test ./internal/telegram/`
Expected:
```
# github.com/solympe/leetcode-tg-notifier/internal/telegram [github.com/solympe/leetcode-tg-notifier/internal/telegram.test]
internal/telegram/view_test.go:90:14: undefined: formatPick
internal/telegram/view_test.go:129:14: undefined: formatRating
internal/telegram/view_test.go:150:14: undefined: formatDifficulties
internal/telegram/view_test.go:166:10: undefined: startKeyboard
internal/telegram/view_test.go:175:10: undefined: doneKeyboard
internal/telegram/view_test.go:180:10: undefined: timeKeyboard
internal/telegram/view_test.go:188:10: undefined: tzKeyboard
internal/telegram/view_test.go:190:7: undefined: tzLabel
internal/telegram/view_test.go:191:7: undefined: tzLabel
internal/telegram/view_test.go:198:10: undefined: difficultyKeyboard
internal/telegram/view_test.go:198:10: too many errors
FAIL	github.com/solympe/leetcode-tg-notifier/internal/telegram [build failed]
FAIL
```

- [ ] **Step 3: Create the view**

The keyboards, `tzLabel`, the problem format and the rating lines are moved from `internal/bot/keyboard.go`, `internal/bot/commands.go` and `leetcode.FormatProblem`/`FormatRandomProblem`, with the same labels, callback data and format strings. Keyboards are returned as pointers, so `edit` can take nil for "no keyboard". Create `internal/telegram/view.go`:

```go
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
```

- [ ] **Step 4: Re-run the view tests**

Run: `go test ./internal/telegram/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected:
```
--- PASS: TestFormatPick (0.00s)
--- PASS: TestFormatRating (0.00s)
--- PASS: TestFormatDifficulties (0.00s)
--- PASS: TestKeyboards (0.00s)
--- PASS: TestTzLabel (0.00s)
--- PASS: TestValidTime (0.00s)
--- PASS: TestValidTimezone (0.00s)
--- PASS: TestDifficultySets (0.00s)
ok  	github.com/solympe/leetcode-tg-notifier/internal/telegram	1.2s
```
The timing differs.

- [ ] **Step 5: Check the button labels against the old keyboards**

Run:
```bash
diff <(grep -oE 'ButtonData\("[^"]+"' internal/bot/keyboard.go | sed 's/ButtonData(//') \
     <(grep -oE 'btn\("[^"]+"' internal/telegram/view.go | sed 's/btn(//') && echo LABELS-IDENTICAL
```
Expected: `LABELS-IDENTICAL`. All 18 literal labels match, in order. The six `tzLabel` zones and the `⬜ `/`✅ ` difficulty toggles are pinned by `TestKeyboards`.

- [ ] **Step 6: Declare the Bot API port**

Create `internal/telegram/deps.go`:

```go
// Package telegram is the Bot API adapter: it routes updates, runs the
// /setup and /difficulty dialogs, renders texts and keyboards and sends
// messages. Apart from app, it is the only package that imports tgbotapi.
package telegram

//go:generate mockgen -source=deps.go -destination=mocks/mock_deps.go -package=mocks

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// botAPI is the part of the Bot API the adapter calls. *tgbotapi.BotAPI
// satisfies it; tgbotapi v5.5.1 has no ctx API.
type botAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}
```

- [ ] **Step 7: Generate the mock**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./internal/telegram/... && go build ./internal/telegram/... && git status --porcelain
```
Expected: `?? internal/telegram/`. `internal/telegram/mocks/mock_deps.go` starts with `// Code generated by MockGen. DO NOT EDIT.` and `// Source: deps.go`, and it defines `MockbotAPI` with `Request` and `Send`.

- [ ] **Step 8: Write the sender tests**

These tests pin the exact `MessageConfig`, `EditMessageTextConfig`, `EditMessageReplyMarkupConfig` and `CallbackConfig` values: HTML on every send and text edit, and a nil keyboard that removes the keyboard. They also check the 403 mapping, which satisfies both `errors.Is(err, domain.ErrBlocked)` and `errors.As(err, *tgbotapi.Error)`, and a 500 or a `SendFetchFailed` 403, which pass through unmapped. A done ctx makes no call, which the bare `mocks.NewMockbotAPI` proves. The fixtures at the top are shared with Task 9's tests. Create `internal/telegram/sender_test.go`:

```go
package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/telegram/mocks"
)

const testChat = int64(100)

// okResp is a successful answerCallbackQuery response.
var okResp = &tgbotapi.APIResponse{Ok: true}

// msgCfg is the sendMessage request for testChat: HTML, with kb when not nil.
func msgCfg(text string, kb *tgbotapi.InlineKeyboardMarkup) tgbotapi.MessageConfig {
	msg := tgbotapi.NewMessage(testChat, text)
	msg.ParseMode = "HTML"
	if kb != nil {
		msg.ReplyMarkup = *kb
	}
	return msg
}

// editCfg is the editMessageText request for testChat: HTML, and a nil kb
// removes the keyboard.
func editCfg(msgID int, text string, kb *tgbotapi.InlineKeyboardMarkup) tgbotapi.EditMessageTextConfig {
	edit := tgbotapi.NewEditMessageText(testChat, msgID, text)
	edit.ParseMode = "HTML"
	edit.ReplyMarkup = kb
	return edit
}

func TestSender(t *testing.T) {
	ctx := t.Context()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	daily := domain.Pick{Problem: domain.Problem{
		Date:       "2026-09-28",
		ID:         "4",
		Title:      "Median of Two Sorted Arrays",
		Link:       "/problems/median-of-two-sorted-arrays/",
		Difficulty: "Hard",
		Tags:       []string{"Array", "Binary Search", "Divide and Conquer"},
	}}
	dailyText := "📅 LeetCode Daily — September 28, 2026\n\n" +
		"🔢 4. Median of Two Sorted Arrays\n💪 Difficulty: Hard\n🏷 Array, Binary Search, Divide and Conquer\n\n" +
		"🔗 https://leetcode.com/problems/median-of-two-sorted-arrays/"
	forbidden := &tgbotapi.Error{Code: http.StatusForbidden, Message: "Forbidden: bot was blocked by the user"}
	serverErr := &tgbotapi.Error{Code: http.StatusInternalServerError, Message: "Internal Server Error"}

	sendProblem := func(ctx context.Context, s *sender) error { return s.SendProblem(ctx, testChat, daily) }
	sendFetchFailed := func(ctx context.Context, s *sender) error { return s.SendFetchFailed(ctx, testChat) }

	tests := []struct {
		name     string
		ctx      context.Context
		call     func(context.Context, *sender) error
		apiMock  func(*gomock.Controller) *mocks.MockbotAPI
		wantErr  error // matched with errors.Is; nil means success
		wantCode int   // when not 0: errors.As finds a *tgbotapi.Error with this code
		blocked  bool  // errors.Is(err, domain.ErrBlocked)
	}{
		{
			name: "problem: HTML message with the Done button",
			ctx:  ctx,
			call: sendProblem,
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(msgCfg(dailyText, doneKeyboard()))).Return(tgbotapi.Message{MessageID: 10}, nil)
				return m
			},
		},
		{
			name: "problem: 403 wraps ErrBlocked and keeps the API error",
			ctx:  ctx,
			call: sendProblem,
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, forbidden)
				return m
			},
			wantErr:  domain.ErrBlocked,
			wantCode: http.StatusForbidden,
			blocked:  true,
		},
		{
			name: "problem: another API error passes through",
			ctx:  ctx,
			call: sendProblem,
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, serverErr)
				return m
			},
			wantErr:  serverErr,
			wantCode: http.StatusInternalServerError,
		},
		{
			name:    "problem: a done ctx makes no call",
			ctx:     cancelled,
			call:    sendProblem,
			apiMock: mocks.NewMockbotAPI,
			wantErr: context.Canceled,
		},
		{
			name: "fetch failed: HTML message without a keyboard",
			ctx:  ctx,
			call: sendFetchFailed,
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(msgCfg("⚠️ Failed to fetch the problem from LeetCode. Try /today later.", nil))).
					Return(tgbotapi.Message{MessageID: 11}, nil)
				return m
			},
		},
		{
			name: "fetch failed: a 403 is not mapped",
			ctx:  ctx,
			call: sendFetchFailed,
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, forbidden)
				return m
			},
			wantErr:  forbidden,
			wantCode: http.StatusForbidden,
		},
		{
			name:    "fetch failed: a done ctx makes no call",
			ctx:     cancelled,
			call:    sendFetchFailed,
			apiMock: mocks.NewMockbotAPI,
			wantErr: context.Canceled,
		},
		{
			name: "text: HTML message without a keyboard",
			ctx:  ctx,
			call: func(ctx context.Context, s *sender) error { s.text(ctx, testChat, "hi"); return nil },
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(msgCfg("hi", nil))).Return(tgbotapi.Message{MessageID: 12}, nil)
				return m
			},
		},
		{
			name: "edit: HTML text with a keyboard",
			ctx:  ctx,
			call: func(ctx context.Context, s *sender) error { s.edit(ctx, testChat, 5, "hi", doneKeyboard()); return nil },
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(editCfg(5, "hi", doneKeyboard()))).Return(tgbotapi.Message{MessageID: 5}, nil)
				return m
			},
		},
		{
			name: "edit: a nil keyboard removes it",
			ctx:  ctx,
			call: func(ctx context.Context, s *sender) error { s.edit(ctx, testChat, 5, "hi", nil); return nil },
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(editCfg(5, "hi", nil))).Return(tgbotapi.Message{MessageID: 5}, nil)
				return m
			},
		},
		{
			name: "editKeyboard: replaces the keyboard only",
			ctx:  ctx,
			call: func(ctx context.Context, s *sender) error {
				s.editKeyboard(ctx, testChat, 5, doneKeyboard())
				return nil
			},
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Send(gomock.Eq(tgbotapi.NewEditMessageReplyMarkup(testChat, 5, *doneKeyboard()))).
					Return(tgbotapi.Message{MessageID: 5}, nil)
				return m
			},
		},
		{
			name: "answer: callback with a toast",
			ctx:  ctx,
			call: func(ctx context.Context, s *sender) error { s.answer(ctx, "cb", "hi"); return nil },
			apiMock: func(ctrl *gomock.Controller) *mocks.MockbotAPI {
				m := mocks.NewMockbotAPI(ctrl)
				m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb", "hi"))).Return(okResp, nil)
				return m
			},
		},
		{
			name: "helpers: a done ctx makes no call",
			ctx:  cancelled,
			call: func(ctx context.Context, s *sender) error {
				s.text(ctx, testChat, "hi")
				s.edit(ctx, testChat, 5, "hi", nil)
				s.editKeyboard(ctx, testChat, 5, doneKeyboard())
				s.answer(ctx, "cb", "hi")
				return nil
			},
			apiMock: mocks.NewMockbotAPI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			err := tt.call(tt.ctx, NewSender(tt.apiMock(ctrl)))
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if got := errors.Is(err, domain.ErrBlocked); got != tt.blocked {
				t.Errorf("errors.Is(err, domain.ErrBlocked) = %v, want %v", got, tt.blocked)
			}
			if tt.wantCode != 0 {
				var tgErr *tgbotapi.Error
				if !errors.As(err, &tgErr) || tgErr.Code != tt.wantCode {
					t.Errorf("errors.As(err, *tgbotapi.Error) = %v, want code %d", tgErr, tt.wantCode)
				}
			}
		})
	}
}

func TestIsBotBlocked(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "generic error", err: errors.New("something"), want: false},
		{name: "tg error 400", err: &tgbotapi.Error{Code: http.StatusBadRequest}, want: false},
		{name: "tg error 403", err: &tgbotapi.Error{Code: http.StatusForbidden}, want: true},
		{name: "wrapped tg error 403", err: fmt.Errorf("wrap: %w", &tgbotapi.Error{Code: http.StatusForbidden}), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBotBlocked(tt.err); got != tt.want {
				t.Errorf("isBotBlocked(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 9: Run the sender tests to see them fail**

Run: `go test ./internal/telegram/`
Expected:
```
# github.com/solympe/leetcode-tg-notifier/internal/telegram [github.com/solympe/leetcode-tg-notifier/internal/telegram.test]
internal/telegram/sender_test.go:60:46: undefined: sender
internal/telegram/sender_test.go:61:50: undefined: sender
internal/telegram/sender_test.go:66:35: undefined: sender
internal/telegram/sender_test.go:147:39: undefined: sender
internal/telegram/sender_test.go:157:39: undefined: sender
internal/telegram/sender_test.go:167:39: undefined: sender
internal/telegram/sender_test.go:177:39: undefined: sender
internal/telegram/sender_test.go:191:39: undefined: sender
internal/telegram/sender_test.go:201:39: undefined: sender
internal/telegram/sender_test.go:215:27: undefined: NewSender
internal/telegram/sender_test.go:215:27: too many errors
FAIL	github.com/solympe/leetcode-tg-notifier/internal/telegram [build failed]
FAIL
```

- [ ] **Step 10: Create the sender**

Create `internal/telegram/sender.go`:

```go
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// sender renders and sends through the Bot API. Every call refuses to start
// once ctx is done. tgbotapi v5.5.1 builds its requests without a ctx, so a
// call already in flight cannot be interrupted; it is bounded by the 75s
// http.Client timeout set in app.New.
type sender struct {
	api botAPI
}

func NewSender(api botAPI) *sender {
	return &sender{api: api}
}

// SendProblem sends p with the Done button. When the chat blocked the bot the
// error wraps both domain.ErrBlocked and the *tgbotapi.Error.
func (s *sender) SendProblem(ctx context.Context, chatID int64, p domain.Pick) error {
	_, err := s.send(ctx, chatID, formatPick(p), doneKeyboard())
	if isBotBlocked(err) {
		return fmt.Errorf("%w: %w", domain.ErrBlocked, err)
	}
	return err
}

func (s *sender) SendFetchFailed(ctx context.Context, chatID int64) error {
	_, err := s.send(ctx, chatID, msgFetchFailed, nil)
	return err
}

// send sends an HTML message, with kb when it is not nil, and returns the
// sent message's ID.
func (s *sender) send(ctx context.Context, chatID int64, text string, kb *tgbotapi.InlineKeyboardMarkup) (int, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = parseMode
	if kb != nil {
		msg.ReplyMarkup = *kb
	}
	sent, err := s.call(ctx, msg)
	return sent.MessageID, err
}

// text sends an HTML message without a keyboard, logging a failure.
func (s *sender) text(ctx context.Context, chatID int64, text string) {
	if _, err := s.send(ctx, chatID, text, nil); err != nil {
		log.Printf("send to %d: %v", chatID, err)
	}
}

// edit replaces message msgID's text; a nil kb removes its keyboard.
func (s *sender) edit(ctx context.Context, chatID int64, msgID int, text string, kb *tgbotapi.InlineKeyboardMarkup) {
	e := tgbotapi.NewEditMessageText(chatID, msgID, text)
	e.ParseMode = parseMode
	e.ReplyMarkup = kb
	if _, err := s.call(ctx, e); err != nil {
		log.Printf("edit %d in %d: %v", msgID, chatID, err)
	}
}

// editKeyboard replaces message msgID's keyboard and keeps its text.
func (s *sender) editKeyboard(ctx context.Context, chatID int64, msgID int, kb *tgbotapi.InlineKeyboardMarkup) {
	if _, err := s.call(ctx, tgbotapi.NewEditMessageReplyMarkup(chatID, msgID, *kb)); err != nil {
		log.Printf("edit keyboard %d in %d: %v", msgID, chatID, err)
	}
}

// answer answers callback cbID with a toast; "" only stops the spinner.
func (s *sender) answer(ctx context.Context, cbID, text string) {
	err := ctx.Err()
	if err == nil {
		_, err = s.api.Request(tgbotapi.NewCallback(cbID, text))
	}
	if err != nil {
		log.Printf("answer callback %s: %v", cbID, err)
	}
}

func (s *sender) call(ctx context.Context, c tgbotapi.Chattable) (tgbotapi.Message, error) {
	if err := ctx.Err(); err != nil {
		return tgbotapi.Message{}, err
	}
	return s.api.Send(c)
}

// isBotBlocked reports whether err is the Bot API's 403 Forbidden, which it
// returns when the chat blocked the bot.
func isBotBlocked(err error) bool {
	var tgErr *tgbotapi.Error
	return errors.As(err, &tgErr) && tgErr.Code == http.StatusForbidden
}
```

- [ ] **Step 11: Re-run the telegram tests**

Run: `go test ./internal/telegram/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected:
```
--- PASS: TestSender (0.00s)
--- PASS: TestIsBotBlocked (0.00s)
--- PASS: TestFormatPick (0.00s)
--- PASS: TestFormatRating (0.00s)
--- PASS: TestFormatDifficulties (0.00s)
--- PASS: TestKeyboards (0.00s)
--- PASS: TestTzLabel (0.00s)
--- PASS: TestValidTime (0.00s)
--- PASS: TestValidTimezone (0.00s)
--- PASS: TestDifficultySets (0.00s)
ok  	github.com/solympe/leetcode-tg-notifier/internal/telegram	1.2s
```
The timing differs. `TestSender` has 13 rows and `TestIsBotBlocked` has 5.

- [ ] **Step 12: Task gate**

Run:
```bash
go build ./... && go test ./... -race -count=1 && make lint && make test-integration
go list -deps . | grep -E 'internal/(domain|notifier|telegram)$'; echo "linked: exit=$?"
go list -f '{{join .Imports " "}}' ./internal/telegram ./internal/telegram/mocks
```
Expected: `go test` prints `ok` for `internal/bot`, `internal/domain`, `internal/leetcode`, `internal/notifier`, `internal/scheduler`, `internal/storage` and `internal/telegram`, and `[no test files]` for the root, `internal/app` (its suite is tagged) and the four `mocks` packages. `make lint` prints `0 issues.` `make test-integration` prints the same, plus `ok` for `internal/app`: rows 1-16, 20-21 and `TestNewFails` pass unchanged. Next comes `linked: exit=1`, because the binary does not link the new packages yet. Then the import lists:
```
context errors fmt github.com/go-telegram-bot-api/telegram-bot-api/v5 github.com/solympe/leetcode-tg-notifier/internal/domain log net/http regexp slices strings time
github.com/go-telegram-bot-api/telegram-bot-api/v5 go.uber.org/mock/gomock reflect
```
telegram imports only `domain` and tgbotapi (spec §4.3), and the mock does not import `telegram`.

- [ ] **Step 13: Commit**

```bash
git add internal/telegram/deps.go internal/telegram/view.go internal/telegram/sender.go internal/telegram/mocks/mock_deps.go internal/telegram/view_test.go internal/telegram/sender_test.go
git commit -F - <<'EOF'
refactor(telegram): add the view, the sender and the botAPI port

internal/telegram starts with view.go (keyboards, the problem and
rating formats and the input rules, byte for byte as in internal/bot
and leetcode's Format*) and sender.go (HTML sends, edits and callback
answers over the botAPI port in deps.go). Every call refuses a done
ctx. SendProblem wraps a 403 as domain.ErrBlocked and keeps the
*tgbotapi.Error. Not wired yet.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [ ] **Step 14: Mocks check on the committed tree**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `CLEAN` and nothing else. If `internal/telegram/mocks/mock_deps.go` is listed, `git add` it and `git commit --amend --no-edit`.

---

### Task 9: `internal/telegram`: handler, dialogs and the service port

**Files:**
- Modify: `internal/telegram/deps.go` (whole file: adds `service`)
- Modify (generated): `internal/telegram/mocks/mock_deps.go` (adds `Mockservice`)
- Create: `internal/telegram/handler_test.go`
- Create: `internal/telegram/dialog_test.go`
- Modify: `internal/telegram/view.go:15-41` (the two const blocks: adds `cmdStart`, `cmdAbout` and the other texts)
- Create: `internal/telegram/dialog.go`
- Create: `internal/telegram/handler.go`

**Interfaces:** Consumes (Task 8): `botAPI`, `mocks.NewMockbotAPI`, `NewSender`, the sender helpers `send`, `text`, `edit`, `editKeyboard` and `answer`, everything in `view.go`, and the test fixtures `testChat`, `okResp`, `msgCfg` and `editCfg`. (Task 6): `domain.Chat` (`NotifyTime`, `Timezone`, `Difficulties`, `Members`), `domain.Member`, `(domain.Chat).Standings() []domain.Member`, `domain.Difficulties()`, `domain.ErrNotSubscribed` and `domain.ErrAlreadySolved`. tgbotapi v5.5.1: `Update` (`UpdateID`, `Message`, `CallbackQuery`), `Message` (`MessageID`, `Chat`, `Text`), `CallbackQuery` (`ID`, `From`, `Message`, `Data`), `User` (`ID`, `FirstName`, `UserName`) and `Chat` (`ID`). Produces:
- In `deps.go`, `type service interface` with exactly these methods: `Subscribe(ctx context.Context, chatID int64, notifyTime, timezone string, difficulties []string) error`, `SetDifficulties(ctx context.Context, chatID int64, difficulties []string) error`, `Unsubscribe(ctx context.Context, chatID int64) error`, `Subscription(ctx context.Context, chatID int64) (domain.Chat, bool, error)`, `Solve(ctx context.Context, chatID, userID int64, name string) (int, error)`, `SendToday(ctx context.Context, chatID int64)` and `SendDaily(ctx context.Context, chatID int64)`. Notifier's `*service` (Task 7) satisfies it. The generated `mocks.NewMockservice(ctrl) *mocks.Mockservice` imports `context`, `domain`, tgbotapi and gomock, never `telegram`.
- `func NewHandler(api botAPI, svc service, botName string) *handler`. It builds its own sender, the session store and the commands table (a field, not a package var).
- `func (h *handler) Handle(ctx context.Context, u tgbotapi.Update)`. It satisfies `app.updateHandler` (Task 3) and recovers a panic with `log.Printf("panic in update %d: %v\n%s", u.UpdateID, r, debug.Stack())`.
- Task 10 wires `telegram.NewSender(api)` into `notifier.New` and `telegram.NewHandler(api, svc, api.Self.UserName)` into `application.bot`.
- Private: `command{run func(ctx context.Context, chatID int64); inMenu bool}`; `step` with `stepTime`, `stepTimezone`, `stepSetupDifficulty` and `stepEditDifficulty`; `session{step; msgID int; notifyTime, timezone string; selected []string}` with `onDifficulty(msgID int) bool`; and `sessions` with `get`, `set` and `drop`.

- [ ] **Step 1: Declare the service port**

Replace `internal/telegram/deps.go` with:

```go
// Package telegram is the Bot API adapter: it routes updates, runs the
// /setup and /difficulty dialogs, renders texts and keyboards and sends
// messages. Apart from app, it is the only package that imports tgbotapi.
package telegram

//go:generate mockgen -source=deps.go -destination=mocks/mock_deps.go -package=mocks

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// botAPI is the part of the Bot API the adapter calls. *tgbotapi.BotAPI
// satisfies it; tgbotapi v5.5.1 has no ctx API.
type botAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}

// service is the use-case core the handler drives, in domain types only.
// notifier's *service satisfies it.
type service interface {
	Subscribe(ctx context.Context, chatID int64, notifyTime, timezone string, difficulties []string) error
	SetDifficulties(ctx context.Context, chatID int64, difficulties []string) error
	Unsubscribe(ctx context.Context, chatID int64) error
	Subscription(ctx context.Context, chatID int64) (domain.Chat, bool, error)
	Solve(ctx context.Context, chatID, userID int64, name string) (int, error)
	SendToday(ctx context.Context, chatID int64)
	SendDaily(ctx context.Context, chatID int64)
}
```

- [ ] **Step 2: Regenerate the mocks**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./internal/telegram/... && go build ./internal/telegram/... && git status --porcelain
```
Expected:
```
 M internal/telegram/deps.go
 M internal/telegram/mocks/mock_deps.go
```
`mock_deps.go` now also defines `Mockservice` with `SendDaily`, `SendToday`, `SetDifficulties`, `Solve`, `Subscribe`, `Subscription` and `Unsubscribe`. `make lint` would flag `service` as unused until Step 9. The gate runs after that.

- [ ] **Step 3: Write the handler tests**

`TestHandleRouting` sends an update in and expects either the service call or the sent config:
- The command forms, with the unknown forms ignored.
- The seven menu callbacks. Each is answered `""` first. When the action is also an API call, `gomock.InOrder` in the api factory checks the order. When it is a service call, the svc factory chains `.After(api.answered)`.
- A callback without its message, and unknown callback data such as `bogus`, `/start` and `done:x` (deviation a).
- `/status`, `/rating`, `/unsubscribe`, `/start` and `/about`.
- A recovered panic.

`TestDone` checks the toast for each `Solve` result, and the `@username` fallback through `Eq` on `Solve`'s arguments. `apiCalls` builds the MockbotAPI factories. The loop body builds the api mock before the svc mock, so `api.answered` belongs to the same row. Create `internal/telegram/handler_test.go`:

```go
package telegram

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/telegram/mocks"
)

var alice = &tgbotapi.User{ID: 7, FirstName: "Alice"}

// message is alice's text message in testChat.
func message(text string) tgbotapi.Update {
	return tgbotapi.Update{UpdateID: 1, Message: &tgbotapi.Message{
		MessageID: 1,
		From:      alice,
		Chat:      &tgbotapi.Chat{ID: testChat},
		Text:      text,
	}}
}

// press is alice pressing the button with data on message msgID in testChat,
// as callback "cb".
func press(msgID int, data string) tgbotapi.Update {
	return tgbotapi.Update{UpdateID: 1, CallbackQuery: &tgbotapi.CallbackQuery{
		ID:      "cb",
		From:    alice,
		Message: &tgbotapi.Message{MessageID: msgID, Chat: &tgbotapi.Chat{ID: testChat}},
		Data:    data,
	}}
}

// apiCalls builds MockbotAPI factories. A factory that answers callback "cb"
// stores that expectation in answered, so the svc factory of the same row,
// built after it, can chain .After(answered).
type apiCalls struct {
	answered *gomock.Call
}

// answer expects only the answer to "cb" with toast.
func (a *apiCalls) answer(toast string) func(*gomock.Controller) *mocks.MockbotAPI {
	return func(ctrl *gomock.Controller) *mocks.MockbotAPI {
		m := mocks.NewMockbotAPI(ctrl)
		a.answered = m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb", toast))).Return(okResp, nil)
		return m
	}
}

// answerThen expects the "" answer to "cb", then want.
func (a *apiCalls) answerThen(want tgbotapi.Chattable) func(*gomock.Controller) *mocks.MockbotAPI {
	return func(ctrl *gomock.Controller) *mocks.MockbotAPI {
		m := mocks.NewMockbotAPI(ctrl)
		a.answered = m.EXPECT().Request(gomock.Eq(tgbotapi.NewCallback("cb", ""))).Return(okResp, nil)
		gomock.InOrder(a.answered, m.EXPECT().Send(gomock.Eq(want)).Return(tgbotapi.Message{MessageID: 50}, nil))
		return m
	}
}

// sends expects only want, which is sent as message 50.
func (a *apiCalls) sends(want tgbotapi.Chattable) func(*gomock.Controller) *mocks.MockbotAPI {
	return func(ctrl *gomock.Controller) *mocks.MockbotAPI {
		m := mocks.NewMockbotAPI(ctrl)
		m.EXPECT().Send(gomock.Eq(want)).Return(tgbotapi.Message{MessageID: 50}, nil)
		return m
	}
}

func TestHandleRouting(t *testing.T) {
	ctx := t.Context()
	var api apiCalls
	subscribed := domain.Chat{
		ChatID:       testChat,
		NotifyTime:   "09:00",
		Timezone:     "Europe/Moscow",
		Difficulties: []string{"Easy", "Hard"},
		Members: map[string]domain.Member{
			"7": {Name: "Alice", Count: 3},
			"8": {Name: "@bob", Count: 1},
		},
	}
	storeDown := errors.New("store down")
	inactive := msgCfg(msgStatusInactive, nil)

	sendToday := func(ctrl *gomock.Controller) *mocks.Mockservice {
		m := mocks.NewMockservice(ctrl)
		m.EXPECT().SendToday(gomock.Eq(ctx), gomock.Eq(testChat))
		return m
	}
	subscription := func(c domain.Chat, ok bool, err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).Return(c, ok, err)
			return m
		}
	}

	tests := []struct {
		name    string
		update  tgbotapi.Update
		apiMock func(*gomock.Controller) *mocks.MockbotAPI
		svcMock func(*gomock.Controller) *mocks.Mockservice
	}{
		{name: "/today", update: message("/today"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "/today@TestBot", update: message("/today@TestBot"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "/today@Other, as before", update: message("/today@Other"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "surrounding spaces are trimmed", update: message("  /today \n"), apiMock: mocks.NewMockbotAPI, svcMock: sendToday},
		{name: "/today extra is not a command", update: message("/today extra"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{name: "an unknown command is ignored", update: message("/foo"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{name: "plain text outside a dialog is ignored", update: message("hello"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{name: "an update without message or callback is ignored", update: tgbotapi.Update{UpdateID: 1}, apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice},
		{
			name:    "/daily",
			update:  message("/daily"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().SendDaily(gomock.Eq(ctx), gomock.Eq(testChat))
				return m
			},
		},
		{
			name:    "/start: the welcome names the bot, with the menu",
			update:  message("/start"),
			apiMock: api.sends(msgCfg(fmt.Sprintf(msgWelcome, "TestBot"), startKeyboard())),
			svcMock: mocks.NewMockservice,
		},
		{name: "/about", update: message("/about"), apiMock: api.sends(msgCfg(msgAbout, nil)), svcMock: mocks.NewMockservice},
		{
			name:    "/status: subscribed",
			update:  message("/status"),
			apiMock: api.sends(msgCfg(fmt.Sprintf(msgStatusActive, "09:00", "Europe/Moscow", "Easy, Hard"), nil)),
			svcMock: subscription(subscribed, true, nil),
		},
		{
			name:    "/status: no difficulties means Any",
			update:  message("/status"),
			apiMock: api.sends(msgCfg(fmt.Sprintf(msgStatusActive, "09:00", "Europe/Moscow", "Any"), nil)),
			svcMock: subscription(domain.Chat{ChatID: testChat, NotifyTime: "09:00", Timezone: "Europe/Moscow"}, true, nil),
		},
		{name: "/status: unsubscribed", update: message("/status"), apiMock: api.sends(inactive), svcMock: subscription(domain.Chat{}, false, nil)},
		{name: "/status: a Subscription error sends nothing", update: message("/status"), apiMock: mocks.NewMockbotAPI, svcMock: subscription(domain.Chat{}, false, storeDown)},
		{
			name:    "/rating: standings",
			update:  message("/rating"),
			apiMock: api.sends(msgCfg(formatRating(subscribed.Standings()), nil)),
			svcMock: subscription(subscribed, true, nil),
		},
		{name: "/rating: unsubscribed", update: message("/rating"), apiMock: api.sends(msgCfg(msgRatingEmpty, nil)), svcMock: subscription(domain.Chat{}, false, nil)},
		{name: "/rating: no members", update: message("/rating"), apiMock: api.sends(msgCfg(msgRatingEmpty, nil)), svcMock: subscription(domain.Chat{ChatID: testChat}, true, nil)},
		{name: "/rating: a Subscription error sends nothing", update: message("/rating"), apiMock: mocks.NewMockbotAPI, svcMock: subscription(domain.Chat{}, false, storeDown)},
		{
			name:    "/unsubscribe",
			update:  message("/unsubscribe"),
			apiMock: api.sends(msgCfg(msgDisabled, nil)),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Unsubscribe(gomock.Eq(ctx), gomock.Eq(testChat)).Return(nil)
				return m
			},
		},
		{
			name:    "/unsubscribe: an error is logged and still confirmed",
			update:  message("/unsubscribe"),
			apiMock: api.sends(msgCfg(msgDisabled, nil)),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Unsubscribe(gomock.Eq(ctx), gomock.Eq(testChat)).Return(storeDown)
				return m
			},
		},
		{
			name:    "menu /setup: answered, then the time prompt",
			update:  press(10, "/setup"),
			apiMock: api.answerThen(msgCfg(msgChooseTime, timeKeyboard())),
			svcMock: mocks.NewMockservice,
		},
		{
			name:    "menu /difficulty: answered, then Subscription",
			update:  press(10, "/difficulty"),
			apiMock: api.answerThen(msgCfg(msgNotSubscribed, nil)),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(domain.Chat{}, false, nil)
				return m
			},
		},
		{
			name:    "menu /today: answered, then SendToday",
			update:  press(10, "/today"),
			apiMock: api.answer(""),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().SendToday(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered)
				return m
			},
		},
		{
			name:    "menu /daily: answered, then SendDaily",
			update:  press(10, "/daily"),
			apiMock: api.answer(""),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().SendDaily(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered)
				return m
			},
		},
		{
			name:    "menu /status: answered, then the status",
			update:  press(10, "/status"),
			apiMock: api.answerThen(inactive),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(domain.Chat{}, false, nil)
				return m
			},
		},
		{
			name:    "menu /rating: answered, then the rating",
			update:  press(10, "/rating"),
			apiMock: api.answerThen(msgCfg(msgRatingEmpty, nil)),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(domain.Chat{}, false, nil)
				return m
			},
		},
		{
			name:    "menu /unsubscribe: answered, then Unsubscribe",
			update:  press(10, "/unsubscribe"),
			apiMock: api.answerThen(msgCfg(msgDisabled, nil)),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Unsubscribe(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(nil)
				return m
			},
		},
		{
			name: "a callback without its message is too old",
			update: tgbotapi.Update{UpdateID: 1, CallbackQuery: &tgbotapi.CallbackQuery{
				ID: "cb", From: alice, Data: "/today",
			}},
			apiMock: api.answer(msgMessageExpired),
			svcMock: mocks.NewMockservice,
		},
		{name: "unknown data has expired", update: press(10, "bogus"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice},
		{name: "/start is not a menu button", update: press(10, "/start"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice},
		{name: "done:x is not done", update: press(10, "done:x"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice},
		{
			name:    "a panic is recovered",
			update:  message("/today"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().SendToday(gomock.Eq(ctx), gomock.Eq(testChat)).Do(func(context.Context, int64) { panic("boom") })
				return m
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			apiMock := tt.apiMock(ctrl) // before svcMock, which may chain .After(api.answered)
			h := NewHandler(apiMock, tt.svcMock(ctrl), "TestBot")
			h.Handle(ctx, tt.update)
		})
	}
}

func TestDone(t *testing.T) {
	ctx := t.Context()
	var api apiCalls
	bob := &tgbotapi.User{ID: 8, UserName: "bob"}

	solve := func(userID int64, name string, total int, err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().Solve(gomock.Eq(ctx), gomock.Eq(testChat), gomock.Eq(userID), gomock.Eq(name)).Return(total, err)
			return m
		}
	}

	tests := []struct {
		name    string
		from    *tgbotapi.User
		svcMock func(*gomock.Controller) *mocks.Mockservice
		apiMock func(*gomock.Controller) *mocks.MockbotAPI
	}{
		{
			name:    "counted",
			from:    alice,
			svcMock: solve(7, "Alice", 3, nil),
			apiMock: api.answer(fmt.Sprintf(msgCounted, 3)),
		},
		{
			name:    "no first name: @username",
			from:    bob,
			svcMock: solve(8, "@bob", 1, nil),
			apiMock: api.answer(fmt.Sprintf(msgCounted, 1)),
		},
		{
			name:    "already counted today",
			from:    alice,
			svcMock: solve(7, "Alice", 3, domain.ErrAlreadySolved),
			apiMock: api.answer(msgAlreadyCounted),
		},
		{
			name:    "not subscribed",
			from:    alice,
			svcMock: solve(7, "Alice", 0, domain.ErrNotSubscribed),
			apiMock: api.answer(msgNotSubscribed),
		},
		{
			name:    "a failed write after counting still reports the total",
			from:    alice,
			svcMock: solve(7, "Alice", 1, errors.New("disk full")),
			apiMock: api.answer(fmt.Sprintf(msgCounted, 1)),
		},
		{
			name:    "a refused call only stops the spinner",
			from:    alice,
			svcMock: solve(7, "Alice", 0, context.Canceled),
			apiMock: api.answer(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			u := press(10, cbDone)
			u.CallbackQuery.From = tt.from
			h := NewHandler(tt.apiMock(ctrl), tt.svcMock(ctrl), "TestBot")
			h.Handle(ctx, u)
		})
	}
}
```

- [ ] **Step 4: Write the dialog tests**

Each row seeds `before` as the chat's session, handles one update, and compares the session afterwards with `after`. The zero value means no session. The rows cover:
- Every transition of spec §7.2, for both typed text and buttons.
- The stale and forged inputs: another message, another step, `time:25:00`, `tz:Local`, and no session.
- `diff:Insane`, and an empty Save, which keeps the session.
- Both Saves. A `Subscribe` error still shows All set, and `ErrNotSubscribed` edits the prompt to `msgNotSubscribed`.
- The session rules for `/setup` and `/difficulty`. A failed send leaves no session. An unsubscribed chat or a `Subscription` error keeps the current session.

Create `internal/telegram/dialog_test.go`:

```go
package telegram

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/telegram/mocks"
)

func TestDialog(t *testing.T) {
	ctx := t.Context()
	var api apiCalls
	const prompt = 42 // the dialog message whose buttons are live
	all := []string{"Easy", "Medium", "Hard"}
	diskFull := errors.New("disk full")

	timeStep := session{step: stepTime, msgID: prompt}
	tzStep := session{step: stepTimezone, msgID: prompt, notifyTime: "09:00"}
	setupDiff := session{step: stepSetupDifficulty, msgID: prompt, notifyTime: "09:00", timezone: "Europe/Moscow", selected: []string{"Easy", "Medium"}}
	editDiff := session{step: stepEditDifficulty, msgID: prompt, selected: []string{"Easy"}}
	tzPrompt := editCfg(prompt, fmt.Sprintf(msgChooseTz, "09:00"), tzKeyboard())
	allSet := editCfg(prompt, fmt.Sprintf(msgAllSet, "09:00", "Europe/Moscow", "Easy, Medium"), nil)
	updated := editCfg(prompt, fmt.Sprintf(msgDifficultyUpdated, "Easy"), nil)

	subscription := func(c domain.Chat, ok bool, err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).Return(c, ok, err)
			return m
		}
	}
	subscribe := func(err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().Subscribe(gomock.Eq(ctx), gomock.Eq(testChat), gomock.Eq("09:00"), gomock.Eq("Europe/Moscow"),
				gomock.Eq([]string{"Easy", "Medium"})).After(api.answered).Return(err)
			return m
		}
	}
	setDifficulties := func(err error) func(*gomock.Controller) *mocks.Mockservice {
		return func(ctrl *gomock.Controller) *mocks.Mockservice {
			m := mocks.NewMockservice(ctrl)
			m.EXPECT().SetDifficulties(gomock.Eq(ctx), gomock.Eq(testChat), gomock.Eq([]string{"Easy"})).After(api.answered).Return(err)
			return m
		}
	}
	sendFails := func(ctrl *gomock.Controller) *mocks.MockbotAPI {
		m := mocks.NewMockbotAPI(ctrl)
		m.EXPECT().Send(gomock.Any()).Return(tgbotapi.Message{}, diskFull)
		return m
	}

	tests := []struct {
		name    string
		before  session // stored for testChat when its step is set
		update  tgbotapi.Update
		apiMock func(*gomock.Controller) *mocks.MockbotAPI
		svcMock func(*gomock.Controller) *mocks.Mockservice
		after   session // testChat's session afterwards; the zero value means none
	}{
		// the time step
		{
			name:    "typed time: the timezone step on the same message",
			before:  timeStep,
			update:  message("09:00"),
			apiMock: api.sends(tzPrompt),
			svcMock: mocks.NewMockservice,
			after:   tzStep,
		},
		{
			name:    "typed invalid time: asked again",
			before:  timeStep,
			update:  message("9:00"),
			apiMock: api.sends(editCfg(prompt, msgInvalidTime, timeKeyboard())),
			svcMock: mocks.NewMockservice,
			after:   timeStep,
		},
		{
			name:    "time button: answered, then the timezone step",
			before:  timeStep,
			update:  press(prompt, "time:09:00"),
			apiMock: api.answerThen(tzPrompt),
			svcMock: mocks.NewMockservice,
			after:   tzStep,
		},
		{name: "time button on another message has expired", before: timeStep, update: press(prompt-1, "time:09:00"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: timeStep},
		{name: "time button in another step has expired", before: tzStep, update: press(prompt, "time:07:00"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: tzStep},
		{name: "forged time:25:00 has expired", before: timeStep, update: press(prompt, "time:25:00"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: timeStep},
		{name: "time button without a session has expired", update: press(prompt, "time:09:00"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice},

		// the timezone step
		{
			name:    "typed timezone: the saved difficulties are ticked",
			before:  tzStep,
			update:  message("Europe/Moscow"),
			apiMock: api.sends(editCfg(prompt, msgChooseDifficulty, difficultyKeyboard([]string{"Easy", "Hard"}))),
			svcMock: subscription(domain.Chat{ChatID: testChat, Difficulties: []string{"Hard", "Easy"}}, true, nil),
			after:   session{step: stepSetupDifficulty, msgID: prompt, notifyTime: "09:00", timezone: "Europe/Moscow", selected: []string{"Easy", "Hard"}},
		},
		{name: "typed unknown timezone: asked again", before: tzStep, update: message("Mars/Base"), apiMock: api.sends(editCfg(prompt, msgInvalidTz, tzKeyboard())), svcMock: mocks.NewMockservice, after: tzStep},
		{name: "typed Local is not a timezone", before: tzStep, update: message("Local"), apiMock: api.sends(editCfg(prompt, msgInvalidTz, tzKeyboard())), svcMock: mocks.NewMockservice, after: tzStep},
		{
			name:    "timezone button: answered, then a new chat gets all three",
			before:  tzStep,
			update:  press(prompt, "tz:Europe/Moscow"),
			apiMock: api.answerThen(editCfg(prompt, msgChooseDifficulty, difficultyKeyboard(all))),
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().Subscription(gomock.Eq(ctx), gomock.Eq(testChat)).After(api.answered).Return(domain.Chat{}, false, nil)
				return m
			},
			after: session{step: stepSetupDifficulty, msgID: prompt, notifyTime: "09:00", timezone: "Europe/Moscow", selected: all},
		},
		{name: "tz:Local has expired", before: tzStep, update: press(prompt, "tz:Local"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: tzStep},
		{name: "timezone button on another message has expired", before: tzStep, update: press(prompt-1, "tz:UTC"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: tzStep},
		{
			name:    "a Subscription error keeps the timezone step",
			before:  tzStep,
			update:  message("Europe/Moscow"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: subscription(domain.Chat{}, false, diskFull),
			after:   tzStep,
		},

		// difficulty buttons
		{
			name:    "difficulty button: answered, toggled, rekeyed",
			before:  editDiff,
			update:  press(prompt, "diff:Hard"),
			apiMock: api.answerThen(tgbotapi.NewEditMessageReplyMarkup(testChat, prompt, *difficultyKeyboard([]string{"Easy", "Hard"}))),
			svcMock: mocks.NewMockservice,
			after:   session{step: stepEditDifficulty, msgID: prompt, selected: []string{"Easy", "Hard"}},
		},
		{name: "diff:Insane is answered and changes nothing", before: editDiff, update: press(prompt, "diff:Insane"), apiMock: api.answer(""), svcMock: mocks.NewMockservice, after: editDiff},
		{name: "difficulty button on another message has expired", before: editDiff, update: press(prompt-1, "diff:Hard"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: editDiff},
		{name: "difficulty button in the time step has expired", before: timeStep, update: press(prompt, "diff:Hard"), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: timeStep},
		{
			name:    "an empty Save asks for one and keeps the session",
			before:  session{step: stepEditDifficulty, msgID: prompt},
			update:  press(prompt, cbDiffSave),
			apiMock: api.answer(msgPickAtLeastOne),
			svcMock: mocks.NewMockservice,
			after:   session{step: stepEditDifficulty, msgID: prompt},
		},
		{name: "Save on another message has expired", before: editDiff, update: press(prompt-1, cbDiffSave), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice, after: editDiff},
		{name: "Save without a session has expired", update: press(prompt, cbDiffSave), apiMock: api.answer(msgMenuExpired), svcMock: mocks.NewMockservice},

		// saves
		{name: "setup Save: answered, subscribed, All set", before: setupDiff, update: press(prompt, cbDiffSave), apiMock: api.answerThen(allSet), svcMock: subscribe(nil)},
		{name: "setup Save: a Subscribe error still shows All set", before: setupDiff, update: press(prompt, cbDiffSave), apiMock: api.answerThen(allSet), svcMock: subscribe(diskFull)},
		{name: "difficulty Save: answered, saved, updated", before: editDiff, update: press(prompt, cbDiffSave), apiMock: api.answerThen(updated), svcMock: setDifficulties(nil)},
		{
			name:    "difficulty Save: no longer subscribed",
			before:  editDiff,
			update:  press(prompt, cbDiffSave),
			apiMock: api.answerThen(editCfg(prompt, msgNotSubscribed, nil)),
			svcMock: setDifficulties(domain.ErrNotSubscribed),
		},
		{name: "difficulty Save: another error still shows updated", before: editDiff, update: press(prompt, cbDiffSave), apiMock: api.answerThen(updated), svcMock: setDifficulties(diskFull)},

		// sessions
		{
			name:    "/setup: the prompt starts a session",
			update:  message("/setup"),
			apiMock: api.sends(msgCfg(msgChooseTime, timeKeyboard())),
			svcMock: mocks.NewMockservice,
			after:   session{step: stepTime, msgID: 50},
		},
		{
			name:    "/setup discards another session",
			before:  editDiff,
			update:  message("/setup"),
			apiMock: api.sends(msgCfg(msgChooseTime, timeKeyboard())),
			svcMock: mocks.NewMockservice,
			after:   session{step: stepTime, msgID: 50},
		},
		{name: "/setup: a failed send leaves no session", before: editDiff, update: message("/setup"), apiMock: sendFails, svcMock: mocks.NewMockservice},
		{
			name:    "/difficulty: the saved set, in a new session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: api.sends(msgCfg(msgChooseDifficulty, difficultyKeyboard([]string{"Hard"}))),
			svcMock: subscription(domain.Chat{ChatID: testChat, Difficulties: []string{"Hard"}}, true, nil),
			after:   session{step: stepEditDifficulty, msgID: 50, selected: []string{"Hard"}},
		},
		{
			name:    "/difficulty: nothing saved ticks all three",
			update:  message("/difficulty"),
			apiMock: api.sends(msgCfg(msgChooseDifficulty, difficultyKeyboard(all))),
			svcMock: subscription(domain.Chat{ChatID: testChat}, true, nil),
			after:   session{step: stepEditDifficulty, msgID: 50, selected: all},
		},
		{
			name:    "/difficulty: unsubscribed keeps the existing session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: api.sends(msgCfg(msgNotSubscribed, nil)),
			svcMock: subscription(domain.Chat{}, false, nil),
			after:   timeStep,
		},
		{
			name:    "/difficulty: a failed send leaves no session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: sendFails,
			svcMock: subscription(domain.Chat{ChatID: testChat}, true, nil),
		},
		{
			name:    "/difficulty: a Subscription error sends nothing and keeps the session",
			before:  timeStep,
			update:  message("/difficulty"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: subscription(domain.Chat{}, false, diskFull),
			after:   timeStep,
		},
		{name: "text in a difficulty step is ignored", before: editDiff, update: message("hello"), apiMock: mocks.NewMockbotAPI, svcMock: mocks.NewMockservice, after: editDiff},
		{
			name:    "a command in the time step is a command",
			before:  timeStep,
			update:  message("/today"),
			apiMock: mocks.NewMockbotAPI,
			svcMock: func(ctrl *gomock.Controller) *mocks.Mockservice {
				m := mocks.NewMockservice(ctrl)
				m.EXPECT().SendToday(gomock.Eq(ctx), gomock.Eq(testChat))
				return m
			},
			after: timeStep,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			apiMock := tt.apiMock(ctrl) // before svcMock, which may chain .After(api.answered)
			h := NewHandler(apiMock, tt.svcMock(ctrl), "TestBot")
			if tt.before.step != 0 {
				h.sessions.set(testChat, tt.before)
			}
			h.Handle(ctx, tt.update)
			if got := h.sessions.get(testChat); !reflect.DeepEqual(got, tt.after) {
				t.Errorf("session = %+v, want %+v", got, tt.after)
			}
		})
	}
}
```

- [ ] **Step 5: Run the tests to see them fail**

Run: `go test ./internal/telegram/`
Expected:
```
# github.com/solympe/leetcode-tg-notifier/internal/telegram [github.com/solympe/leetcode-tg-notifier/internal/telegram.test]
internal/telegram/dialog_test.go:23:14: undefined: session
internal/telegram/dialog_test.go:23:28: undefined: stepTime
internal/telegram/dialog_test.go:24:12: undefined: session
internal/telegram/dialog_test.go:24:26: undefined: stepTimezone
internal/telegram/dialog_test.go:25:15: undefined: session
internal/telegram/dialog_test.go:25:29: undefined: stepSetupDifficulty
internal/telegram/dialog_test.go:26:14: undefined: session
internal/telegram/dialog_test.go:26:28: undefined: stepEditDifficulty
internal/telegram/dialog_test.go:27:42: undefined: msgChooseTz
internal/telegram/dialog_test.go:28:40: undefined: msgAllSet
internal/telegram/dialog_test.go:28:40: too many errors
FAIL	github.com/solympe/leetcode-tg-notifier/internal/telegram [build failed]
FAIL
```

- [ ] **Step 6: Add the remaining commands and texts to the view**

In `internal/telegram/view.go`, replace lines 15-41 (the two `const` blocks, from `// Commands, the menu buttons' callback data among them, and the other` to the `)` after `msgRatingEmpty`) with:

```go
// Commands, the menu buttons' callback data among them, and the other
// callback data. Buttons on messages sent by earlier versions carry these
// strings, so they must never change.
const (
	cmdStart       = "/start"
	cmdAbout       = "/about"
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
	msgWelcome           = "Hi! I'm <b>%s</b>. I help you subscribe to a daily LeetCode challenge newsletter and get the problem of the day anytime.\n\nCommands:\n• /setup — create your daily subscription\n• /difficulty — choose problem difficulty\n• /today — get today's problem (your difficulty)\n• /daily — get the official LeetCode daily (any difficulty)\n• /rating — show solve leaderboard\n• /status — check your subscription status\n• /about — learn more about this bot"
	msgAbout             = "For questions, suggestions, and bug reports — DM @solympe"
	msgChooseTime        = "Choose notification time or type your own (<b>HH:MM</b>, 24h):"
	msgChooseTz          = "Time: <b>%s</b>\n\nChoose your timezone or type it manually (e.g. <code>Europe/Moscow</code>)\nhttps://en.wikipedia.org/wiki/List_of_tz_database_time_zones"
	msgChooseDifficulty  = "Choose difficulty (tap to toggle, then Save).\nIf today's daily doesn't match, I'll send a random problem of your level instead."
	msgPickAtLeastOne    = "Pick at least one difficulty"
	msgAllSet            = "✅ All set! I'll send you the daily problem at <b>%s</b> (%s)\nDifficulty: <b>%s</b>"
	msgDifficultyUpdated = "✅ Difficulty updated: <b>%s</b>"
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
	msgAlreadyCounted    = "Already counted today!"
	msgCounted           = "✅ Counted! Your total: %d"
)
```

The rest of `view.go` is unchanged.

- [ ] **Step 7: Check the texts against the old constants**

Run:
```bash
texts() { grep -E '^[[:space:]]+msg[A-Za-z]+ += "' "$1" | sed -E 's/^[[:space:]]+(msg[A-Za-z]+) += /\1 /' | LC_ALL=C sort; }
diff <(texts internal/bot/const.go) <(texts internal/telegram/view.go); echo "exit=$?"
grep -n '"Already counted today!"\|Counted! Your total' internal/bot/commands.go
```
Expected:
```
2a3
> msgAlreadyCounted "Already counted today!"
5a7
> msgCounted "✅ Counted! Your total: %d"
16d17
< msgSessionExpired "⚠️ Session expired. Please use /setup to start over."
exit=1
148:		b.answerCB(cb.ID, "Already counted today!")
150:		b.answerCB(cb.ID, fmt.Sprintf("✅ Counted! Your total: %d", stat.Count))
```
Every other text is byte-identical. The two new constants are the inline toasts from `commands.go`. `msgSessionExpired` is deleted, because a timezone step without a time can no longer be represented (spec §7.2).

- [ ] **Step 8: Create the dialogs**

`sessions` replaces `stateStore`, which had 5 maps and 11 accessors. Each button function only validates its input and returns `(toast, act)`. Create `internal/telegram/dialog.go`:

```go
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// step is where a chat's /setup or /difficulty dialog is; 0 means none.
type step int

const (
	stepTime step = iota + 1
	stepTimezone
	stepSetupDifficulty
	stepEditDifficulty
)

type session struct {
	step                 step
	msgID                int // the one message whose buttons are live
	notifyTime, timezone string
	selected             []string // canonical order
}

// onDifficulty reports whether msgID is the difficulty keyboard of the session.
func (s session) onDifficulty(msgID int) bool {
	return (s.step == stepSetupDifficulty || s.step == stepEditDifficulty) && s.msgID == msgID
}

// sessions holds the dialogs in progress, in memory: a restart loses them.
type sessions struct {
	mu sync.Mutex
	m  map[int64]session
}

// get returns a copy of chatID's session; the zero step means none.
func (ss *sessions) get(chatID int64) session {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s := ss.m[chatID]
	s.selected = slices.Clone(s.selected)
	return s
}

func (ss *sessions) set(chatID int64, s session) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s.selected = slices.Clone(s.selected)
	ss.m[chatID] = s
}

func (ss *sessions) drop(chatID int64) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.m, chatID)
}

// startSetup starts /setup over with a fresh prompt, discarding any other
// session, so an abandoned one never blocks it.
func (h *handler) startSetup(ctx context.Context, chatID int64) {
	h.sessions.drop(chatID)
	msgID, err := h.send(ctx, chatID, msgChooseTime, timeKeyboard())
	if err != nil {
		log.Printf("setup prompt to %d: %v", chatID, err)
		return
	}
	h.sessions.set(chatID, session{step: stepTime, msgID: msgID})
}

// startDifficulty starts a /difficulty session for a subscribed chat,
// discarding any other session. An unsubscribed chat keeps its session.
func (h *handler) startDifficulty(ctx context.Context, chatID int64) {
	c, ok, err := h.svc.Subscription(ctx, chatID)
	if err != nil {
		log.Printf("subscription of %d: %v", chatID, err)
		return
	}
	if !ok {
		h.text(ctx, chatID, msgNotSubscribed)
		return
	}
	h.sessions.drop(chatID)
	selected := initialDifficulties(c.Difficulties)
	msgID, err := h.send(ctx, chatID, msgChooseDifficulty, difficultyKeyboard(selected))
	if err != nil {
		log.Printf("difficulty prompt to %d: %v", chatID, err)
		return
	}
	h.sessions.set(chatID, session{step: stepEditDifficulty, msgID: msgID, selected: selected})
}

// onText handles typed text in the time and timezone steps; other text is
// ignored.
func (h *handler) onText(ctx context.Context, chatID int64, text string) {
	s := h.sessions.get(chatID)
	switch {
	case s.step == stepTime && validTime(text):
		h.chooseTime(ctx, chatID, s, text)
	case s.step == stepTime:
		h.edit(ctx, chatID, s.msgID, msgInvalidTime, timeKeyboard())
	case s.step == stepTimezone && validTimezone(text):
		h.chooseTimezone(ctx, chatID, s, text)
	case s.step == stepTimezone:
		h.edit(ctx, chatID, s.msgID, msgInvalidTz, tzKeyboard())
	}
}

func (h *handler) timeButton(ctx context.Context, chatID int64, msgID int, t string) (string, func()) {
	s := h.sessions.get(chatID)
	if s.step != stepTime || s.msgID != msgID || !validTime(t) {
		return msgMenuExpired, nil
	}
	return "", func() { h.chooseTime(ctx, chatID, s, t) }
}

func (h *handler) tzButton(ctx context.Context, chatID int64, msgID int, zone string) (string, func()) {
	s := h.sessions.get(chatID)
	if s.step != stepTimezone || s.msgID != msgID || !validTimezone(zone) {
		return msgMenuExpired, nil
	}
	return "", func() { h.chooseTimezone(ctx, chatID, s, zone) }
}

func (h *handler) diffButton(ctx context.Context, chatID int64, msgID int, d string) (string, func()) {
	s := h.sessions.get(chatID)
	if !s.onDifficulty(msgID) {
		return msgMenuExpired, nil
	}
	if !slices.Contains(domain.Difficulties(), d) {
		return "", nil
	}
	return "", func() {
		s.selected = toggle(s.selected, d)
		h.sessions.set(chatID, s)
		h.editKeyboard(ctx, chatID, msgID, difficultyKeyboard(s.selected))
	}
}

func (h *handler) saveButton(ctx context.Context, chatID int64, msgID int) (string, func()) {
	s := h.sessions.get(chatID)
	if !s.onDifficulty(msgID) {
		return msgMenuExpired, nil
	}
	if len(s.selected) == 0 {
		return msgPickAtLeastOne, nil
	}
	return "", func() {
		h.sessions.drop(chatID)
		if s.step == stepSetupDifficulty {
			h.finishSetup(ctx, chatID, s)
			return
		}
		h.saveDifficulties(ctx, chatID, s)
	}
}

// chooseTime records the time and turns the prompt into the timezone step.
func (h *handler) chooseTime(ctx context.Context, chatID int64, s session, t string) {
	s.step, s.notifyTime = stepTimezone, t
	h.sessions.set(chatID, s)
	h.edit(ctx, chatID, s.msgID, fmt.Sprintf(msgChooseTz, t), tzKeyboard())
}

// chooseTimezone records the zone and turns the prompt into the difficulty
// step, with the chat's saved difficulties ticked. When the subscription
// cannot be read, the session stays at the timezone step.
func (h *handler) chooseTimezone(ctx context.Context, chatID int64, s session, zone string) {
	c, _, err := h.svc.Subscription(ctx, chatID)
	if err != nil {
		log.Printf("subscription of %d: %v", chatID, err)
		return
	}
	s.step, s.timezone, s.selected = stepSetupDifficulty, zone, initialDifficulties(c.Difficulties)
	h.sessions.set(chatID, s)
	h.edit(ctx, chatID, s.msgID, msgChooseDifficulty, difficultyKeyboard(s.selected))
}

// finishSetup saves and schedules the subscription. A failed save is logged
// and the user still sees All set, as before the refactor.
func (h *handler) finishSetup(ctx context.Context, chatID int64, s session) {
	if err := h.svc.Subscribe(ctx, chatID, s.notifyTime, s.timezone, s.selected); err != nil {
		log.Printf("subscribe %d: %v", chatID, err)
	}
	h.edit(ctx, chatID, s.msgID, fmt.Sprintf(msgAllSet, s.notifyTime, s.timezone, formatDifficulties(s.selected)), nil)
}

func (h *handler) saveDifficulties(ctx context.Context, chatID int64, s session) {
	text := fmt.Sprintf(msgDifficultyUpdated, formatDifficulties(s.selected))
	err := h.svc.SetDifficulties(ctx, chatID, s.selected)
	switch {
	case errors.Is(err, domain.ErrNotSubscribed):
		text = msgNotSubscribed
	case err != nil:
		log.Printf("set difficulties of %d: %v", chatID, err)
	}
	h.edit(ctx, chatID, s.msgID, text, nil)
}
```

- [ ] **Step 9: Create the handler**

Create `internal/telegram/handler.go`:

```go
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// command is a slash command; inMenu commands are also menu buttons, whose
// callback data is the command itself.
type command struct {
	run    func(ctx context.Context, chatID int64)
	inMenu bool
}

// handler routes updates. It never creates a ctx: every call uses the one
// Handle receives, and callback actions capture it.
type handler struct {
	*sender
	svc      service
	botName  string
	sessions sessions
	commands map[string]command // a field, not a package var, which avoids an init cycle
}

func NewHandler(api botAPI, svc service, botName string) *handler {
	h := &handler{
		sender:   NewSender(api),
		svc:      svc,
		botName:  botName,
		sessions: sessions{m: make(map[int64]session)},
	}
	h.commands = map[string]command{
		cmdStart:       {run: h.start},
		cmdAbout:       {run: h.about},
		cmdSetup:       {run: h.startSetup, inMenu: true},
		cmdDifficulty:  {run: h.startDifficulty, inMenu: true},
		cmdToday:       {run: svc.SendToday, inMenu: true},
		cmdDaily:       {run: svc.SendDaily, inMenu: true},
		cmdStatus:      {run: h.status, inMenu: true},
		cmdRating:      {run: h.rating, inMenu: true},
		cmdUnsubscribe: {run: h.unsubscribe, inMenu: true},
	}
	return h
}

// Handle handles one update. A panic is logged and the update dropped, so it
// never takes the process down.
func (h *handler) Handle(ctx context.Context, u tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in update %d: %v\n%s", u.UpdateID, r, debug.Stack())
		}
	}()
	switch {
	case u.CallbackQuery != nil:
		h.onCallback(ctx, u.CallbackQuery)
	case u.Message != nil:
		h.onMessage(ctx, u.Message)
	}
}

// onMessage runs a command, "/today" and "/today@AnyBot" alike, or passes
// other text to the dialog. Unknown commands are ignored.
func (h *handler) onMessage(ctx context.Context, m *tgbotapi.Message) {
	text := strings.TrimSpace(m.Text)
	if !strings.HasPrefix(text, "/") {
		h.onText(ctx, m.Chat.ID, text)
		return
	}
	name, _, _ := strings.Cut(text, "@")
	if c, ok := h.commands[name]; ok {
		c.run(ctx, m.Chat.ID)
	}
}

// onCallback answers every callback exactly once, before its action runs.
func (h *handler) onCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	if cb.Message == nil {
		h.answer(ctx, cb.ID, msgMessageExpired)
		return
	}
	toast, act := h.button(ctx, cb.Message.Chat.ID, cb.Message.MessageID, cb.From, cb.Data)
	h.answer(ctx, cb.ID, toast)
	if act != nil {
		act()
	}
}

// button validates a press on message msgID and returns its toast and the
// action to run after the answer, if any. Only done has a side effect here:
// its toast reports the count.
func (h *handler) button(ctx context.Context, chatID int64, msgID int, from *tgbotapi.User, data string) (string, func()) {
	if c, ok := h.commands[data]; ok && c.inMenu {
		return "", func() { c.run(ctx, chatID) }
	}
	if data == cbDone {
		return h.done(ctx, chatID, from), nil
	}
	if data == cbDiffSave {
		return h.saveButton(ctx, chatID, msgID)
	}
	if t, ok := strings.CutPrefix(data, cbPrefixTime); ok {
		return h.timeButton(ctx, chatID, msgID, t)
	}
	if zone, ok := strings.CutPrefix(data, cbPrefixTz); ok {
		return h.tzButton(ctx, chatID, msgID, zone)
	}
	if d, ok := strings.CutPrefix(data, cbPrefixDiff); ok {
		return h.diffButton(ctx, chatID, msgID, d)
	}
	return msgMenuExpired, nil
}

// done counts the presser's solve for today and returns the toast.
func (h *handler) done(ctx context.Context, chatID int64, from *tgbotapi.User) string {
	name := from.FirstName
	if name == "" {
		name = "@" + from.UserName
	}
	total, err := h.svc.Solve(ctx, chatID, from.ID, name)
	switch {
	case errors.Is(err, domain.ErrNotSubscribed):
		return msgNotSubscribed
	case errors.Is(err, domain.ErrAlreadySolved):
		return msgAlreadyCounted
	case err != nil:
		log.Printf("solve in %d: %v", chatID, err)
		if total == 0 {
			return ""
		}
	}
	return fmt.Sprintf(msgCounted, total)
}

func (h *handler) start(ctx context.Context, chatID int64) {
	if _, err := h.send(ctx, chatID, fmt.Sprintf(msgWelcome, h.botName), startKeyboard()); err != nil {
		log.Printf("start to %d: %v", chatID, err)
	}
}

func (h *handler) about(ctx context.Context, chatID int64) {
	h.text(ctx, chatID, msgAbout)
}

func (h *handler) status(ctx context.Context, chatID int64) {
	c, ok, err := h.svc.Subscription(ctx, chatID)
	switch {
	case err != nil:
		log.Printf("subscription of %d: %v", chatID, err)
	case !ok:
		h.text(ctx, chatID, msgStatusInactive)
	default:
		h.text(ctx, chatID, fmt.Sprintf(msgStatusActive, c.NotifyTime, c.Timezone, formatDifficulties(c.Difficulties)))
	}
}

func (h *handler) rating(ctx context.Context, chatID int64) {
	c, ok, err := h.svc.Subscription(ctx, chatID)
	switch {
	case err != nil:
		log.Printf("subscription of %d: %v", chatID, err)
	case !ok:
		h.text(ctx, chatID, msgRatingEmpty)
	default:
		h.text(ctx, chatID, formatRating(c.Standings()))
	}
}

func (h *handler) unsubscribe(ctx context.Context, chatID int64) {
	if err := h.svc.Unsubscribe(ctx, chatID); err != nil {
		log.Printf("unsubscribe %d: %v", chatID, err)
	}
	h.text(ctx, chatID, msgDisabled)
}
```

- [ ] **Step 10: Re-run the telegram tests**

Run: `go test ./internal/telegram/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected:
```
--- PASS: TestDialog (0.01s)
--- PASS: TestHandleRouting (0.00s)
--- PASS: TestDone (0.00s)
--- PASS: TestSender (0.00s)
--- PASS: TestIsBotBlocked (0.00s)
--- PASS: TestFormatPick (0.00s)
--- PASS: TestFormatRating (0.00s)
--- PASS: TestFormatDifficulties (0.00s)
--- PASS: TestKeyboards (0.00s)
--- PASS: TestTzLabel (0.00s)
--- PASS: TestValidTime (0.00s)
--- PASS: TestValidTimezone (0.00s)
--- PASS: TestDifficultySets (0.00s)
ok  	github.com/solympe/leetcode-tg-notifier/internal/telegram	1.2s
```
The timings differ. `TestDialog` has 36 rows, `TestHandleRouting` 33 and `TestDone` 6.

- [ ] **Step 11: Task gate**

Run:
```bash
go build ./... && go test ./... -race -count=1 && make lint && make test-integration
go list -deps . | grep -E 'internal/(domain|notifier|telegram)$'; echo "linked: exit=$?"
go list -f '{{join .Imports " "}}' ./internal/telegram ./internal/telegram/mocks
```
Expected: `go test` prints `ok` for `internal/bot`, `internal/domain`, `internal/leetcode`, `internal/notifier`, `internal/scheduler`, `internal/storage` and `internal/telegram`, and `[no test files]` for the root, `internal/app` (its suite is tagged) and the four `mocks` packages. `make lint` prints `0 issues.` `make test-integration` prints the same, plus `ok` for `internal/app`: rows 1-16, 20-21 and `TestNewFails` pass unchanged. Next comes `linked: exit=1`, and then the import lists:
```
context errors fmt github.com/go-telegram-bot-api/telegram-bot-api/v5 github.com/solympe/leetcode-tg-notifier/internal/domain log net/http regexp runtime/debug slices strings sync time
context github.com/go-telegram-bot-api/telegram-bot-api/v5 github.com/solympe/leetcode-tg-notifier/internal/domain go.uber.org/mock/gomock reflect
```
telegram still imports only `domain` and tgbotapi. The mocks import `domain` but not `telegram`, so there is no test import cycle (spec §3.2).

- [ ] **Step 12: Commit**

```bash
git add internal/telegram/deps.go internal/telegram/view.go internal/telegram/dialog.go internal/telegram/handler.go internal/telegram/mocks/mock_deps.go internal/telegram/handler_test.go internal/telegram/dialog_test.go
git commit -F - <<'EOF'
refactor(telegram): add the handler, the dialogs and the service port

handler.go routes updates through a commands table and answers every
callback exactly once through the (toast, act) protocol, before its
action runs. Unknown callback data gets the menu-expired toast
(deviation a), and a panic is recovered and logged (deviation c).
dialog.go replaces the five-map state store with one session per chat.
The use cases are reached through the service port in deps.go, in
domain types only. Not wired yet.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [ ] **Step 13: Mocks check on the committed tree**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `CLEAN` and nothing else. If `internal/telegram/mocks/mock_deps.go` is listed, `git add` it and `git commit --amend --no-edit`.

---

### Task 10: The switch (storage, leetcode, scheduler, app wiring; delete `internal/bot`)

One commit (spec §10 step 7). Steps (a)–(f) below keep the tree unbuildable between Step 1 and Step 15; each package is test-first on its own (`go test ./internal/<pkg>/` compiles only that package and `internal/domain`).

**Files:**
- Delete: `internal/storage/storage.go`
- Modify: `internal/storage/json.go` (whole file), `internal/storage/json_test.go` (whole file; drops Task 5's old-code golden row, which is re-expressed below, and adds the Review Focus pins `TestSaveInPlace` and `TestLoadDamagedFile`)
- Delete: `internal/leetcode/client.go`, `internal/leetcode/client_test.go`, `internal/leetcode/format_test.go`
- Modify: `internal/leetcode/http.go:5-16` (imports), `:115-263` (ctx-first `query`, `FetchDaily`, `FetchRandom`, `fetchQuestionList`; `FormatProblem`, `FormatRandomProblem`, `formatDate`, `formatBody` deleted)
- Modify: `internal/leetcode/http_test.go` (mechanical renames; `:3-18` imports; `:125`, `:392-394`, `:442`, `:476-478`, `:516-522`; `TestContext` appended)
- Modify: `internal/scheduler/cron.go` (whole file), `internal/scheduler/cron_test.go` (whole file)
- Delete: `internal/scheduler/scheduler.go`
- Modify: `internal/app/app.go:13-16` (imports), `:45-53` (struct, `New` signature), `:65-73` (wiring), `:76-80` (`Run` doc, `defer a.cancelJobs()`)
- Delete: `internal/bot/` (10 production files, 10 test files including Task 2's `bot_test.go`, `mocks/mock_deps.go`)
- Must stay unchanged: `internal/app/*_test.go`, `internal/leetcode/mocks/mock_deps.go`, `go.mod`, `go.sum`

**Interfaces:**
Consumes:
- Task 3: `app.Config{Token, StoragePath, TelegramEndpoint, LeetCodeEndpoint string}`, `New(_ context.Context, cfg Config) (*application, error)`, `(*application).Run(ctx)`, `handle(base, u)`, `type application struct{ api *tgbotapi.BotAPI; sched jobRunner; bot updateHandler }`, `jobRunner{Start(); Stop(); RunNow(int64) bool; Next(int64) (time.Time, bool)}`, `updateHandler{Handle(ctx, tgbotapi.Update)}`.
- Task 2 (with its review fix-ups at `af23735`/`c3666fc`): `leetcode.NewHTTPClient(endpoint string, c *http.Client) *httpClient` (fields `http`, `endpoint`, `rnd randSource`), `TestNewHTTPClient` with its three rows; the old `cronScheduler` seams; `.golangci.yml` with revive's default rules enabled (`unexported-return` disabled), which all code below passes.
- Tasks 4-5: `make test-integration`; the suite (rows 1–16, 20–21 and `TestNewFails`) in `internal/app/*_test.go`; (Task 5) `internal/storage/testdata/legacy_config.json`, written by the old `jsonStorage` and holding a negative group ID, a chat with `"members": null` and no `difficulties`, and a chat with members and a full flat `daily_pick`.
- Task 6: `domain.Chat`, `domain.Member`, `domain.Problem`, `domain.Pick{Problem; DailyDifficulty}`, `domain.Easy`/`Medium`/`Hard`, `domain.Difficulties() []string`.
- Task 7: `notifier.New(jobs context.Context, store chatStore, lc problemSource, sched dailyScheduler, out messenger, now func() time.Time) *service`, `(*service).Restore(ctx context.Context) error`, and the port signatures in `notifier/deps.go` (spec §4.4) that storage, leetcode and scheduler must now satisfy.
- Tasks 8–9: `telegram.NewSender(api botAPI) *sender`, `telegram.NewHandler(api botAPI, svc service, botName string) *handler`, `(*handler).Handle(ctx context.Context, u tgbotapi.Update)`.

Produces:
- `storage.NewJSONStorage(path string) (*jsonStorage, error)` with `Get(ctx, chatID int64) (domain.Chat, bool, error)`, `Upsert(ctx, chatID int64, fn func(*domain.Chat)) error`, `Update(ctx, chatID int64, fn func(*domain.Chat) bool) (bool, error)`, `Delete(ctx, chatID int64) error`, `All(ctx) ([]domain.Chat, error)` (sorted by `ChatID`).
- `(*httpClient).FetchDaily(ctx context.Context) (domain.Problem, error)`, `FetchRandom(ctx context.Context, difficulties []string) (domain.Problem, error)`.
- `scheduler.New() *cronScheduler` with `Schedule(chatID int64, notifyTime, timezone string, job func()) error`, `Remove(int64)`, `Start()`, `Stop()`, `RunNow(int64) bool`, `Next(int64) (time.Time, bool)`.
- `app.New(ctx context.Context, cfg Config)`; `application.cancelJobs context.CancelFunc`, cancelled when `Run` returns.
- Deleted: `internal/bot`, `storage.ChatConfig`/`UserStat`/`DailyPick`/`Set`, `leetcode.Problem`/`AllDifficulties`/`Difficulty*`/`Format*`, `scheduler.SendFunc`/`NewCronScheduler`.

#### (a) storage

- [ ] **Step 1: Replace `internal/storage/json_test.go`**

```go
package storage

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// legacyUserStat, legacyDailyPick and legacyChatConfig are verbatim copies of
// the structs the pre-refactor binary persisted (internal/storage/storage.go
// at 8879c84). Decoding with them shows what a rolled-back binary reads.
type legacyUserStat struct {
	Name           string `json:"name"`
	Count          int    `json:"count"`
	LastSolvedDate string `json:"last_solved_date"`
}

type legacyDailyPick struct {
	Date            string   `json:"date"`
	DailyDifficulty string   `json:"daily_difficulty"`
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Link            string   `json:"link"`
	Difficulty      string   `json:"difficulty"`
	Tags            []string `json:"tags"`
}

type legacyChatConfig struct {
	ChatID       int64                     `json:"chat_id"`
	NotifyTime   string                    `json:"notify_time"`
	Timezone     string                    `json:"timezone"`
	Members      map[string]legacyUserStat `json:"members"`
	Difficulties []string                  `json:"difficulties,omitempty"`
	DailyPick    *legacyDailyPick          `json:"daily_pick,omitempty"`
}

type legacyFile struct {
	Chats map[string]legacyChatConfig `json:"chats"`
}

func decodeLegacy(t *testing.T, raw []byte) legacyFile {
	t.Helper()
	var f legacyFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode with the legacy structs: %v", err)
	}
	return f
}

// fromLegacy maps what the old binary reads from raw onto domain chats, field
// by field, sorted by ChatID like All.
func fromLegacy(t *testing.T, raw []byte) []domain.Chat {
	t.Helper()
	f := decodeLegacy(t, raw)
	chats := make([]domain.Chat, 0, len(f.Chats))
	for _, c := range f.Chats {
		chat := domain.Chat{
			ChatID:       c.ChatID,
			NotifyTime:   c.NotifyTime,
			Timezone:     c.Timezone,
			Difficulties: c.Difficulties,
		}
		if c.Members != nil {
			chat.Members = make(map[string]domain.Member, len(c.Members))
			for k, m := range c.Members {
				chat.Members[k] = domain.Member{Name: m.Name, Count: m.Count, LastSolvedDate: m.LastSolvedDate}
			}
		}
		if p := c.DailyPick; p != nil {
			chat.DailyPick = &domain.Pick{
				Problem: domain.Problem{
					Date:       p.Date,
					ID:         p.ID,
					Title:      p.Title,
					Link:       p.Link,
					Difficulty: p.Difficulty,
					Tags:       p.Tags,
				},
				DailyDifficulty: p.DailyDifficulty,
			}
		}
		chats = append(chats, chat)
	}
	slices.SortFunc(chats, func(a, b domain.Chat) int { return cmp.Compare(a.ChatID, b.ChatID) })
	return chats
}

// fullChat returns a fresh chat with every reference field set.
func fullChat() domain.Chat {
	return domain.Chat{
		ChatID:       42,
		NotifyTime:   "10:30",
		Timezone:     "Europe/Moscow",
		Members:      map[string]domain.Member{"7": {Name: "Alice", Count: 3, LastSolvedDate: "2026-09-26"}},
		Difficulties: []string{"Medium", "Hard"},
		DailyPick: &domain.Pick{
			Problem: domain.Problem{
				Date:       "2026-09-27",
				ID:         "42",
				Title:      "Trapping Rain Water",
				Link:       "/problems/trapping-rain-water/",
				Difficulty: "Hard",
				Tags:       []string{"Array", "Two Pointers"},
			},
			DailyDifficulty: "Easy",
		},
	}
}

// minimalChat returns a fresh chat whose reference fields are all nil.
func minimalChat() domain.Chat {
	return domain.Chat{ChatID: 43, NotifyTime: "08:00", Timezone: "UTC"}
}

// mutateChat writes into every reference field of c.
func mutateChat(c domain.Chat) {
	c.Members["8"] = domain.Member{Name: "Bob", Count: 1}
	c.Difficulties[0] = "Easy"
	c.DailyPick.Title = "Mutated"
	c.DailyPick.Tags[0] = "Mutated"
}

func newStore(t *testing.T, path string) *jsonStorage {
	t.Helper()
	s, err := NewJSONStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// put stores c as is through Upsert.
func put(t *testing.T, s *jsonStorage, c domain.Chat) {
	t.Helper()
	if err := s.Upsert(t.Context(), c.ChatID, func(stored *domain.Chat) { *stored = c }); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTrip(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name string
		chat func() domain.Chat
	}{
		{name: "every field survives a reload", chat: fullChat},
		{name: "nil reference fields stay nil after a reload", chat: minimalChat},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			put(t, newStore(t, path), tt.chat())

			got, ok, err := newStore(t, path).Get(ctx, tt.chat().ChatID)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("chat should survive a reload")
			}
			if want := tt.chat(); !reflect.DeepEqual(got, want) {
				t.Errorf("reloaded chat:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestStoredChatIsIndependent(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name   string
		chat   func() domain.Chat
		mutate func(s *jsonStorage)
	}{
		{
			name: "mutating a Get result leaves the store unchanged",
			chat: fullChat,
			mutate: func(s *jsonStorage) {
				got, _, _ := s.Get(ctx, 42)
				mutateChat(got)
			},
		},
		{
			name: "mutating an All result leaves the store unchanged",
			chat: fullChat,
			mutate: func(s *jsonStorage) {
				all, _ := s.All(ctx)
				mutateChat(all[0])
			},
		},
		{
			name: "mutating the chat an Upsert saved leaves the store unchanged",
			chat: fullChat,
			mutate: func(s *jsonStorage) {
				var saved domain.Chat
				_ = s.Upsert(ctx, 42, func(c *domain.Chat) { saved = *c })
				mutateChat(saved)
			},
		},
		{
			name: "mutating the chat inside a discarded Update leaves the store unchanged",
			chat: fullChat,
			mutate: func(s *jsonStorage) {
				_, _ = s.Update(ctx, 42, func(c *domain.Chat) bool { mutateChat(*c); return false })
			},
		},
		{
			name: "mutating the chat an Update saved leaves the store unchanged",
			chat: fullChat,
			mutate: func(s *jsonStorage) {
				var saved domain.Chat
				_, _ = s.Update(ctx, 42, func(c *domain.Chat) bool { saved = *c; return true })
				mutateChat(saved)
			},
		},
		{
			name:   "nil reference fields stay nil",
			chat:   func() domain.Chat { c := minimalChat(); c.ChatID = 42; return c },
			mutate: func(*jsonStorage) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t, filepath.Join(t.TempDir(), "config.json"))
			put(t, s, tt.chat())

			tt.mutate(s)

			got, ok, err := s.Get(ctx, 42)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("chat should be stored")
			}
			if want := tt.chat(); !reflect.DeepEqual(got, want) {
				t.Errorf("stored chat:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestUpsert(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name     string
		before   []domain.Chat
		chatID   int64
		fn       func(c *domain.Chat)
		wantSeen domain.Chat // what fn is given
		want     domain.Chat
	}{
		{
			name:     "creates a missing chat with its ChatID",
			chatID:   100,
			fn:       func(c *domain.Chat) { c.NotifyTime, c.Timezone = "09:00", "UTC" },
			wantSeen: domain.Chat{ChatID: 100},
			want:     domain.Chat{ChatID: 100, NotifyTime: "09:00", Timezone: "UTC"},
		},
		{
			name:   "keeps the members and pick of an existing chat",
			before: []domain.Chat{fullChat()},
			chatID: 42,
			fn: func(c *domain.Chat) {
				c.NotifyTime, c.Timezone, c.Difficulties = "21:15", "Asia/Tbilisi", []string{"Easy"}
			},
			wantSeen: fullChat(),
			want: func() domain.Chat {
				c := fullChat()
				c.NotifyTime, c.Timezone, c.Difficulties = "21:15", "Asia/Tbilisi", []string{"Easy"}
				return c
			}(),
		},
		{
			name:     "the key wins over a ChatID set by fn",
			chatID:   -100500,
			fn:       func(c *domain.Chat) { c.ChatID, c.NotifyTime = 7, "07:00" },
			wantSeen: domain.Chat{ChatID: -100500},
			want:     domain.Chat{ChatID: -100500, NotifyTime: "07:00"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s := newStore(t, path)
			for _, c := range tt.before {
				put(t, s, c)
			}

			calls := 0
			var seen domain.Chat
			if err := s.Upsert(ctx, tt.chatID, func(c *domain.Chat) {
				calls++
				seen = clone(*c)
				tt.fn(c)
			}); err != nil {
				t.Fatalf("Upsert: %v", err)
			}

			if calls != 1 {
				t.Errorf("fn calls: got %d, want 1", calls)
			}
			if !reflect.DeepEqual(seen, tt.wantSeen) {
				t.Errorf("fn got:\n got %+v\nwant %+v", seen, tt.wantSeen)
			}
			all, err := newStore(t, path).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 1 || !reflect.DeepEqual(all[0], tt.want) {
				t.Errorf("reloaded chats:\n got %+v\nwant [%+v]", all, tt.want)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	ctx := t.Context()
	bumpAlice := func(c *domain.Chat) bool {
		m := c.Members["7"]
		m.Count++
		c.Members["7"] = m
		return true
	}
	withAliceCount := func(n int) domain.Chat {
		c := fullChat()
		c.Members["7"] = domain.Member{Name: "Alice", Count: n, LastSolvedDate: "2026-09-26"}
		return c
	}

	tests := []struct {
		name      string
		chatID    int64
		fn        func(c *domain.Chat) bool
		callers   int
		wantFound bool
		wantCalls int
		want      domain.Chat
	}{
		{
			name:      "saves the change fn makes",
			chatID:    42,
			fn:        bumpAlice,
			callers:   1,
			wantFound: true,
			wantCalls: 1,
			want:      withAliceCount(4),
		},
		{
			name:      "fn returning false saves nothing",
			chatID:    42,
			fn:        func(c *domain.Chat) bool { bumpAlice(c); return false },
			callers:   1,
			wantFound: true,
			wantCalls: 1,
			want:      fullChat(),
		},
		{
			name:      "missing chat is neither passed to fn nor created",
			chatID:    99,
			fn:        bumpAlice,
			callers:   1,
			wantFound: false,
			wantCalls: 0,
			want:      fullChat(),
		},
		{
			name:      "concurrent updates of one chat are all kept",
			chatID:    42,
			fn:        bumpAlice,
			callers:   50,
			wantFound: true,
			wantCalls: 50,
			want:      withAliceCount(53),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s := newStore(t, path)
			put(t, s, fullChat())

			var calls atomic.Int32
			var wg sync.WaitGroup
			for range tt.callers {
				wg.Go(func() {
					found, err := s.Update(ctx, tt.chatID, func(c *domain.Chat) bool {
						calls.Add(1)
						return tt.fn(c)
					})
					if err != nil {
						t.Errorf("Update: %v", err)
					}
					if found != tt.wantFound {
						t.Errorf("found: got %v, want %v", found, tt.wantFound)
					}
				})
			}
			wg.Wait()

			if got := int(calls.Load()); got != tt.wantCalls {
				t.Errorf("fn calls: got %d, want %d", got, tt.wantCalls)
			}
			all, err := newStore(t, path).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 1 || !reflect.DeepEqual(all[0], tt.want) {
				t.Errorf("reloaded chats:\n got %+v\nwant [%+v]", all, tt.want)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name   string
		chatID int64
		want   []int64
	}{
		{name: "removes the chat from memory and file", chatID: 42, want: []int64{43}},
		{name: "a missing chat leaves the others", chatID: 99, want: []int64{42, 43}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s := newStore(t, path)
			put(t, s, fullChat())
			put(t, s, minimalChat())

			if err := s.Delete(ctx, tt.chatID); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			for _, store := range []*jsonStorage{s, newStore(t, path)} {
				all, err := store.All(ctx)
				if err != nil {
					t.Fatal(err)
				}
				ids := make([]int64, 0, len(all))
				for _, c := range all {
					ids = append(ids, c.ChatID)
				}
				if !slices.Equal(ids, tt.want) {
					t.Errorf("chat IDs: got %v, want %v", ids, tt.want)
				}
			}
		})
	}
}

func TestAll(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name  string
		chats []int64
		want  []int64
	}{
		{name: "sorted by ChatID", chats: []int64{42, -100123, 5}, want: []int64{-100123, 5, 42}},
		{name: "an empty store has no chats", chats: nil, want: []int64{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t, filepath.Join(t.TempDir(), "config.json"))
			for _, id := range tt.chats {
				put(t, s, domain.Chat{ChatID: id})
			}

			all, err := s.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, 0, len(all))
			for _, c := range all {
				ids = append(ids, c.ChatID)
			}
			if !slices.Equal(ids, tt.want) {
				t.Errorf("chat IDs: got %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestCancelledContext(t *testing.T) {
	ctx := t.Context()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	mustNotRun := func(t *testing.T) func(*domain.Chat) bool {
		return func(*domain.Chat) bool { t.Error("fn must not run on a done ctx"); return true }
	}

	tests := []struct {
		name string
		call func(t *testing.T, s *jsonStorage) error
	}{
		{
			name: "Get",
			call: func(_ *testing.T, s *jsonStorage) error { _, _, err := s.Get(cancelled, 42); return err },
		},
		{
			name: "Upsert",
			call: func(t *testing.T, s *jsonStorage) error {
				return s.Upsert(cancelled, 99, func(c *domain.Chat) { mustNotRun(t)(c) })
			},
		},
		{
			name: "Update",
			call: func(t *testing.T, s *jsonStorage) error { _, err := s.Update(cancelled, 42, mustNotRun(t)); return err },
		},
		{
			name: "Delete",
			call: func(_ *testing.T, s *jsonStorage) error { return s.Delete(cancelled, 42) },
		},
		{
			name: "All",
			call: func(_ *testing.T, s *jsonStorage) error { _, err := s.All(cancelled); return err },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s := newStore(t, path)
			put(t, s, fullChat())
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if err := tt.call(t, s); !errors.Is(err, context.Canceled) {
				t.Errorf("error: got %v, want %v", err, context.Canceled)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Errorf("file changed:\n got %s\nwant %s", after, before)
			}
			all, err := s.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if want := []domain.Chat{fullChat()}; !reflect.DeepEqual(all, want) {
				t.Errorf("chats in memory:\n got %+v\nwant %+v", all, want)
			}
		})
	}
}

func TestLoadLegacyConfig(t *testing.T) {
	ctx := t.Context()
	golden, err := os.ReadFile(filepath.Join("testdata", "legacy_config.json"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		raw  []byte
		want []domain.Chat
	}{
		{
			name: "config without difficulty fields",
			raw: []byte(`{
  "chats": {
    "123": {
      "chat_id": 123,
      "notify_time": "09:00",
      "timezone": "Asia/Tbilisi",
      "members": {
        "7": {"name": "Alice", "count": 5, "last_solved_date": "2026-09-20"}
      }
    }
  }
}`),
			want: []domain.Chat{{
				ChatID:     123,
				NotifyTime: "09:00",
				Timezone:   "Asia/Tbilisi",
				Members:    map[string]domain.Member{"7": {Name: "Alice", Count: 5, LastSolvedDate: "2026-09-20"}},
			}},
		},
		{
			name: "config without members",
			raw:  []byte(`{"chats":{"5":{"chat_id":5,"notify_time":"21:15","timezone":"UTC"}}}`),
			want: []domain.Chat{{ChatID: 5, NotifyTime: "21:15", Timezone: "UTC"}},
		},
		{
			// Written by the pre-refactor jsonStorage; the flat daily_pick
			// must fill both Pick.Problem and Pick.DailyDifficulty.
			name: "golden fixture written by the old binary",
			raw:  golden,
			want: fromLegacy(t, golden),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, tt.raw, 0o644); err != nil {
				t.Fatal(err)
			}

			got, err := newStore(t, path).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loaded chats:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// TestGoldenFixtureShape pins what the fixture must cover, so the golden rows
// above and below cannot pass on a fixture that lost a case.
func TestGoldenFixtureShape(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "legacy_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	chats := fromLegacy(t, golden)

	tests := []struct {
		name  string
		match func(c domain.Chat) bool
	}{
		{name: "a negative group ID", match: func(c domain.Chat) bool { return c.ChatID < 0 }},
		{name: "a legacy chat with members null and no difficulties", match: func(c domain.Chat) bool {
			return c.Members == nil && c.Difficulties == nil
		}},
		{name: "a chat with members and a full flat daily_pick", match: func(c domain.Chat) bool {
			p := c.DailyPick
			return len(c.Members) > 0 && p != nil && p.Date != "" && p.DailyDifficulty != "" &&
				p.ID != "" && p.Title != "" && p.Link != "" && p.Difficulty != "" && len(p.Tags) > 0
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.ContainsFunc(chats, tt.match) {
				t.Errorf("testdata/legacy_config.json has no chat with %s: %+v", tt.name, chats)
			}
		})
	}
}

func TestRollbackSafety(t *testing.T) {
	ctx := t.Context()
	golden, err := os.ReadFile(filepath.Join("testdata", "legacy_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	newPick := domain.Pick{
		Problem: domain.Problem{
			Date:       "2026-09-29",
			ID:         "2",
			Title:      "Add Two Numbers",
			Link:       "/problems/add-two-numbers/",
			Difficulty: "Medium",
			Tags:       []string{"Linked List", "Math", "Recursion"},
		},
		DailyDifficulty: "Hard",
	}

	tests := []struct {
		name string
		fn   func(c *domain.Chat) bool // applied to every chat through Update
		// want returns what the old binary must read back.
		want func(t *testing.T) legacyFile
		// sameBytes: re-encoding what the old binary reads gives the fixture back.
		sameBytes bool
	}{
		{
			name:      "a rewrite by the new code reads back unchanged",
			fn:        func(*domain.Chat) bool { return true },
			want:      func(t *testing.T) legacyFile { return decodeLegacy(t, golden) },
			sameBytes: true,
		},
		{
			name: "a pick saved by the new code is the old flat daily_pick",
			fn: func(c *domain.Chat) bool {
				p := newPick
				p.Tags = slices.Clone(newPick.Tags)
				c.DailyPick = &p
				return true
			},
			want: func(t *testing.T) legacyFile {
				f := decodeLegacy(t, golden)
				for k, c := range f.Chats {
					c.DailyPick = &legacyDailyPick{
						Date:            "2026-09-29",
						DailyDifficulty: "Hard",
						ID:              "2",
						Title:           "Add Two Numbers",
						Link:            "/problems/add-two-numbers/",
						Difficulty:      "Medium",
						Tags:            []string{"Linked List", "Math", "Recursion"},
					}
					f.Chats[k] = c
				}
				return f
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, golden, 0o644); err != nil {
				t.Fatal(err)
			}
			s := newStore(t, path)
			all, err := s.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range all {
				if _, err := s.Update(ctx, c.ChatID, tt.fn); err != nil {
					t.Fatalf("Update %d: %v", c.ChatID, err)
				}
			}

			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			old := decodeLegacy(t, written)
			if want := tt.want(t); !reflect.DeepEqual(old, want) {
				t.Errorf("old binary reads:\n got %+v\nwant %+v", old, want)
			}
			if !tt.sameBytes {
				return
			}
			reencoded, err := json.MarshalIndent(old, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(reencoded, bytes.TrimSpace(golden)) {
				t.Errorf("old binary re-encodes:\n%s\nwant the fixture:\n%s", reencoded, golden)
			}
		})
	}
}

// TestSaveInPlace pins the bind-mount contract (spec §4.7): every save
// rewrites config.json in place. A temp file renamed over it fails with EBUSY
// when config.json is bind-mounted into the container.
func TestSaveInPlace(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name string
		save func(s *jsonStorage) error
	}{
		{
			name: "Upsert",
			save: func(s *jsonStorage) error {
				return s.Upsert(ctx, 43, func(c *domain.Chat) { c.NotifyTime = "07:00" })
			},
		},
		{
			name: "Update",
			save: func(s *jsonStorage) error {
				_, err := s.Update(ctx, 42, func(c *domain.Chat) bool { c.NotifyTime = "07:00"; return true })
				return err
			},
		},
		{
			name: "Delete",
			save: func(s *jsonStorage) error { return s.Delete(ctx, 42) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			s := newStore(t, path)
			put(t, s, fullChat())
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			if err := tt.save(s); err != nil {
				t.Fatal(err)
			}

			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) {
				t.Error("config.json was replaced by another file, want it rewritten in place")
			}
			if after.Mode() != before.Mode() {
				t.Errorf("mode: got %v, want %v", after.Mode(), before.Mode())
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "config.json" {
				t.Errorf("directory holds %v, want only config.json", entries)
			}
		})
	}
}

// TestLoadDamagedFile pins today's start on a damaged config.json, which a
// crash mid-write leaves behind (spec §12): the error is logged, the store
// starts empty rather than half-loaded, and the next save writes a valid
// file. Follow-up 1 deliberately turns this into a startup error.
func TestLoadDamagedFile(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty file", raw: ""},
		{name: "truncated mid-write", raw: `{"chats":{"42":{"chat_id":42,"notify_time":"09:00","timezone":"UTC","members":{"7":{"name":"Ali`},
		{name: "a field of the wrong type", raw: `{"chats":{"42":{"chat_id":42,"notify_time":"09:00","timezone":"UTC","members":{"7":{"name":"Alice","count":"five"}}}}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.raw), 0o644); err != nil {
				t.Fatal(err)
			}

			s, err := NewJSONStorage(path)
			if err != nil {
				t.Fatalf("NewJSONStorage: %v, want an empty store", err)
			}
			all, err := s.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 0 {
				t.Errorf("chats: got %+v, want none", all)
			}

			put(t, s, minimalChat())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := fromLegacy(t, raw), []domain.Chat{minimalChat()}; !reflect.DeepEqual(got, want) {
				t.Errorf("file after the next save:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/storage/ -race -count=1`
Expected: build failure starting with
```
internal/storage/json_test.go:146:14: s.Upsert undefined (type *jsonStorage has no field or method Upsert)
internal/storage/json_test.go:166:20: assignment mismatch: 3 variables but newStore(t, path).Get returns 2 values
internal/storage/json_test.go:166:47: too many arguments in call to newStore(t, path).Get
	have (context.Context, int64)
	want (int64)
```
then `FAIL	github.com/solympe/leetcode-tg-notifier/internal/storage [build failed]`.

- [ ] **Step 3: Move storage onto `domain.Chat`**

Run: `git rm -q internal/storage/storage.go`

Replace `internal/storage/json.go` with:
```go
package storage

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strconv"
	"sync"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)

// jsonFile is the on-disk format, {"chats":{"<id>":Chat}}. The domain JSON
// tags are the file's contract: renaming one needs a migration.
type jsonFile struct {
	Chats map[string]domain.Chat `json:"chats"`
}

// jsonStorage keeps every chat in memory and rewrites the whole file on each
// change. One mutex guards both, so every method is atomic. A method whose
// ctx is already done returns ctx.Err() before taking the lock; a started
// write always completes, because file I/O cannot be cancelled.
type jsonStorage struct {
	mu   sync.Mutex
	path string
	data jsonFile
}

func NewJSONStorage(path string) (*jsonStorage, error) {
	s := &jsonStorage{
		path: path,
		data: jsonFile{Chats: make(map[string]domain.Chat)},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *jsonStorage) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("load storage: %w", err)
	}
	if err := json.Unmarshal(data, &s.data); err != nil {
		log.Printf("storage: unmarshal error, starting fresh: %v", err)
		s.data = jsonFile{Chats: make(map[string]domain.Chat)}
	}
	return nil
}

// save writes the file in place, not through a temp file and rename, which
// fails with EBUSY on a bind-mounted config.json.
func (s *jsonStorage) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func key(chatID int64) string {
	return strconv.FormatInt(chatID, 10)
}

// clone deep-copies the reference fields of c, so the stored chats and the
// ones callers hold never share memory; nil fields stay nil.
func clone(c domain.Chat) domain.Chat {
	c.Members = maps.Clone(c.Members)
	c.Difficulties = slices.Clone(c.Difficulties)
	if c.DailyPick != nil {
		pick := *c.DailyPick
		pick.Tags = slices.Clone(pick.Tags)
		c.DailyPick = &pick
	}
	return c
}

func (s *jsonStorage) Get(ctx context.Context, chatID int64) (domain.Chat, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Chat{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Chats[key(chatID)]
	return clone(c), ok, nil
}

// Upsert applies fn to the chat, or to a new Chat{ChatID: chatID} when it is
// missing, and saves the result.
func (s *jsonStorage) Upsert(ctx context.Context, chatID int64, fn func(*domain.Chat)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.mutate(chatID, true, func(c *domain.Chat) bool {
		fn(c)
		return true
	})
	return err
}

// Update applies fn to the chat and saves the result when fn returns true. It
// reports whether the chat exists; a missing chat is neither passed to fn nor
// created, so a removed chat is never brought back.
func (s *jsonStorage) Update(ctx context.Context, chatID int64, fn func(*domain.Chat) bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return s.mutate(chatID, false, fn)
}

// mutate runs fn on a clone of the chat under the lock. When fn returns true
// it forces ChatID to the key, stores a clone in memory, then saves the file.
// A missing chat is created only when create is set.
func (s *jsonStorage) mutate(chatID int64, create bool, fn func(*domain.Chat) bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Chats[key(chatID)]
	if !ok && !create {
		return false, nil
	}
	if !ok {
		c = domain.Chat{ChatID: chatID}
	}
	c = clone(c)
	if !fn(&c) {
		return true, nil
	}
	c.ChatID = chatID
	s.data.Chats[key(chatID)] = clone(c)
	return true, s.save()
}

func (s *jsonStorage) Delete(ctx context.Context, chatID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Chats, key(chatID))
	return s.save()
}

// All returns every chat sorted by ChatID.
func (s *jsonStorage) All(ctx context.Context) ([]domain.Chat, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chats := make([]domain.Chat, 0, len(s.data.Chats))
	for _, c := range s.data.Chats {
		chats = append(chats, clone(c))
	}
	slices.SortFunc(chats, func(a, b domain.Chat) int { return cmp.Compare(a.ChatID, b.ChatID) })
	return chats, nil
}
```

- [ ] **Step 4: Re-run the storage tests**

Run: `go test ./internal/storage/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected:
```
--- PASS: TestRoundTrip
--- PASS: TestStoredChatIsIndependent
--- PASS: TestUpsert
--- PASS: TestUpdate
--- PASS: TestDelete
--- PASS: TestAll
--- PASS: TestCancelledContext
--- PASS: TestLoadLegacyConfig
--- PASS: TestGoldenFixtureShape
--- PASS: TestRollbackSafety
--- PASS: TestSaveInPlace
--- PASS: TestLoadDamagedFile
ok  	github.com/solympe/leetcode-tg-notifier/internal/storage
```
(each `--- PASS` line ends with its duration). `TestRollbackSafety`'s first row proves the only byte change is the key order inside `daily_pick` (spec §5.2): decoding the rewritten file with the legacy structs and re-encoding gives the fixture back byte for byte. `TestSaveInPlace` and `TestLoadDamagedFile` are Review Focus pins 4 and 5. Under `-v` the damaged-file rows log `storage: unmarshal error, starting fresh: …`, which is expected.

#### (b) leetcode

- [ ] **Step 5: Port `http_test.go` to ctx and value returns**

Mechanical renames first:
```bash
perl -pi -e 's/&Problem\{/domain.Problem{/g; s/\*Problem\b/domain.Problem/g; s/\bDifficulty(Easy|Medium|Hard)\b/domain.$1/g; s/hc\.FetchRandom\(tt\.difficulties\)/hc.FetchRandom(ctx, tt.difficulties)/; s/hc\.FetchDaily\(\)/hc.FetchDaily(ctx)/' internal/leetcode/http_test.go
```

Then make these edits in `internal/leetcode/http_test.go`, top to bottom. Line numbers refer to the file before this step.

Replace the import block (lines 3-18) with
```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode/mocks"
)
```

Replace `func TestFetchRandom(t *testing.T) {` (line 125) with
```go
func TestFetchRandom(t *testing.T) {
	ctx := t.Context()
```

In the `TestFetchRandom` loop body, replace (lines 392-394)
```go
				if got != nil {
					t.Errorf("problem: got %+v, want nil", got)
				}
```
with
```go
				if !reflect.DeepEqual(got, domain.Problem{}) {
					t.Errorf("problem: got %+v, want the zero value", got)
				}
```

Replace `func TestFetchDaily(t *testing.T) {` (line 442) with
```go
func TestFetchDaily(t *testing.T) {
	ctx := t.Context()
```

After the `"empty question"` row (lines 476-478)
```go
			body:    `{"data":{"activeDailyCodingChallengeQuestion":null}}`,
			wantErr: "empty response",
		},
```
insert
```go
		{
			name:    "malformed JSON",
			status:  http.StatusOK,
			body:    `{"data":`,
			wantErr: "decode",
		},
```

In the `TestFetchDaily` loop body, replace (lines 516-522)
```go
			got, err := hc.FetchDaily(ctx)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error: got %v, want containing %q", err, tt.wantErr)
				}
				return
			}
```
with
```go
			got, err := hc.FetchDaily(ctx)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error: got %v, want containing %q", err, tt.wantErr)
				}
				if !reflect.DeepEqual(got, domain.Problem{}) {
					t.Errorf("problem: got %+v, want the zero value", got)
				}
				return
			}
```

Append to the end of the file:
```go
func TestContext(t *testing.T) {
	ctx := t.Context()
	const deadline = 50 * time.Millisecond
	cancelled := func(parent context.Context) (context.Context, context.CancelFunc) {
		c, cancel := context.WithCancel(parent)
		cancel()
		return c, cancel
	}
	withDeadline := func(parent context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(parent, deadline)
	}
	easyLevel := func(ctrl *gomock.Controller) *mocks.MockrandSource {
		m := mocks.NewMockrandSource(ctrl)
		m.EXPECT().IntN(gomock.Eq(1)).Return(0)
		return m
	}
	fetchDaily := func(ctx context.Context, hc *httpClient) (domain.Problem, error) { return hc.FetchDaily(ctx) }
	fetchRandom := func(ctx context.Context, hc *httpClient) (domain.Problem, error) {
		return hc.FetchRandom(ctx, []string{domain.Easy})
	}

	tests := []struct {
		name     string
		ctx      func(parent context.Context) (context.Context, context.CancelFunc)
		randMock func(*gomock.Controller) *mocks.MockrandSource
		call     func(ctx context.Context, hc *httpClient) (domain.Problem, error)
		wantErr  error
		wantHits int32
	}{
		{
			name:     "FetchDaily with a cancelled ctx sends nothing",
			ctx:      cancelled,
			randMock: mocks.NewMockrandSource,
			call:     fetchDaily,
			wantErr:  context.Canceled,
			wantHits: 0,
		},
		{
			name:     "FetchRandom with a cancelled ctx sends nothing",
			ctx:      cancelled,
			randMock: easyLevel,
			call:     fetchRandom,
			wantErr:  context.Canceled,
			wantHits: 0,
		},
		{
			name:     "FetchDaily gives up on a request in flight at the deadline",
			ctx:      withDeadline,
			randMock: mocks.NewMockrandSource,
			call:     fetchDaily,
			wantErr:  context.DeadlineExceeded,
			wantHits: 1,
		},
		{
			name:     "FetchRandom gives up on a request in flight at the deadline",
			ctx:      withDeadline,
			randMock: easyLevel,
			call:     fetchRandom,
			wantErr:  context.DeadlineExceeded,
			wantHits: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			var hits atomic.Int32
			release := make(chan struct{})
			// The handler holds every request until the client gives up.
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(func() { close(release) }) // runs first: never leave a handler blocked
			hc := NewHTTPClient(srv.URL, srv.Client())
			hc.rnd = tt.randMock(ctrl)
			callCtx, cancel := tt.ctx(ctx)
			defer cancel()

			start := time.Now()
			got, err := tt.call(callCtx, hc)
			elapsed := time.Since(start)

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error: got %v, want %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, domain.Problem{}) {
				t.Errorf("problem: got %+v, want the zero value", got)
			}
			if elapsed > deadline+time.Second {
				t.Errorf("returned after %v, want about %v", elapsed, deadline)
			}
			if n := hits.Load(); n != tt.wantHits {
				t.Errorf("server hits: got %d, want %d", n, tt.wantHits)
			}
		})
	}
}
```

`TestNewHTTPClient` (Task 2) is unchanged.

- [ ] **Step 6: Run it to see it fail**

Run: `go test ./internal/leetcode/ -race -count=1`
Expected: build failure starting with
```
internal/leetcode/http_test.go:392:36: too many arguments in call to hc.FetchRandom
	have (context.Context, []string)
	want ([]string)
internal/leetcode/http_test.go:528:30: too many arguments in call to hc.FetchDaily
	have (context.Context)
	want ()
```

- [ ] **Step 7: Thread ctx through the client and return values**

Run: `git rm -q internal/leetcode/client.go internal/leetcode/client_test.go internal/leetcode/format_test.go`
(`TestAllDifficulties` lives in `domain` since Task 6; the `format_test.go` cases are in `telegram/view_test.go` since Task 8.)

In `internal/leetcode/http.go`, replace the import block (lines 5-16) with
```go
import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
)
```

and replace everything from line 115 (`// query POSTs a GraphQL payload and decodes a 200 OK JSON response into out.`) to the end of the file (line 263) with
```go
// query POSTs a GraphQL payload and decodes a 200 OK JSON response into out.
// The request is bound to ctx and to the client's per-request timeout,
// whichever ends first.
func (hc *httpClient) query(ctx context.Context, payload []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hc.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := hc.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("close response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

func (hc *httpClient) FetchDaily(ctx context.Context) (domain.Problem, error) {
	var lcResp lcResponse
	if err := hc.query(ctx, []byte(dailyQuery), &lcResp); err != nil {
		return domain.Problem{}, err
	}

	q := lcResp.Data.ActiveDailyCodingChallengeQuestion
	if q.Question.Title == "" {
		return domain.Problem{}, fmt.Errorf("empty response from LeetCode")
	}

	return domain.Problem{
		Date:       q.Date,
		Link:       q.Link,
		ID:         q.Question.FrontendQuestionID,
		Title:      q.Question.Title,
		Difficulty: q.Question.Difficulty,
		Tags:       tagNames(q.Question.TopicTags),
	}, nil
}

// FetchRandom returns a random free algorithms problem of one of the given
// difficulties (any difficulty when empty). The returned Problem has no Date.
func (hc *httpClient) FetchRandom(ctx context.Context, difficulties []string) (domain.Problem, error) {
	if len(difficulties) == 0 {
		difficulties = domain.Difficulties()
	}
	for _, d := range difficulties {
		if !slices.Contains(domain.Difficulties(), d) {
			return domain.Problem{}, fmt.Errorf("unknown difficulty %q", d)
		}
	}
	d := difficulties[hc.rnd.IntN(len(difficulties))]

	first, err := hc.fetchQuestionList(ctx, d, 1, 0)
	if err != nil {
		return domain.Problem{}, fmt.Errorf("count %s problems: %w", d, err)
	}
	if first.Total <= 0 {
		return domain.Problem{}, fmt.Errorf("no %s problems on LeetCode", d)
	}

	// Each attempt draws one problem uniformly and rejects it when it is paid
	// (LeetCode ignores the premiumOnly filter) or missing, so every free
	// problem is equally likely.
	for range maxRandomAttempts {
		draw, err := hc.fetchQuestionList(ctx, d, 1, hc.rnd.IntN(first.Total))
		if err != nil {
			return domain.Problem{}, fmt.Errorf("fetch %s problem: %w", d, err)
		}
		if len(draw.Questions) == 0 || draw.Questions[0].PaidOnly {
			continue
		}
		q := draw.Questions[0]
		return domain.Problem{
			Link:       "/problems/" + q.TitleSlug + "/",
			ID:         q.FrontendQuestionID,
			Title:      q.Title,
			Difficulty: q.Difficulty,
			Tags:       tagNames(q.TopicTags),
		}, nil
	}
	return domain.Problem{}, fmt.Errorf("no free %s problem found in %d attempts", d, maxRandomAttempts)
}

func (hc *httpClient) fetchQuestionList(ctx context.Context, difficulty string, limit, skip int) (questionList, error) {
	payload, err := json.Marshal(listRequest{
		Query: questionListQuery,
		Variables: listVariables{
			CategorySlug: randomCategory,
			Limit:        limit,
			Skip:         skip,
			Filters:      listFilters{Difficulty: strings.ToUpper(difficulty)},
		},
	})
	if err != nil {
		return questionList{}, fmt.Errorf("marshal: %w", err)
	}
	var resp listResponse
	if err := hc.query(ctx, payload, &resp); err != nil {
		return questionList{}, err
	}
	return resp.Data.ProblemsetQuestionList, nil
}

func tagNames(tags []topicTag) []string {
	names := make([]string, 0, len(tags))
	for _, t := range tags {
		names = append(names, t.Name)
	}
	return names
}
```

The `//go:generate` line, the queries, the JSON types, `randSource`, `globalRand`, `httpClient` and `NewHTTPClient` (lines 1-4 and 17-114) are unchanged, so `leetcode/mocks/mock_deps.go` does not change.

- [ ] **Step 8: Re-run the leetcode tests**

Run: `go test ./internal/leetcode/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected:
```
--- PASS: TestFetchRandom
--- PASS: TestNewHTTPClient
--- PASS: TestFetchDaily
--- PASS: TestContext
ok  	github.com/solympe/leetcode-tg-notifier/internal/leetcode
```

#### (c) scheduler

- [ ] **Step 9: Replace `internal/scheduler/cron_test.go`**

Task 2's `recorder`/`chatAt` helpers go with the old `Schedule(chatID, storage.ChatConfig)`. Its review row "failed reschedule leaves no runnable entry" (`25:00`) becomes `TestSchedule`'s "hour out of range keeps the previous entry": with parse-before-replace (deviation f) the old entry stays scheduled and is the one `RunNow` runs.
```go
package scheduler

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

// recorder counts the runs of named jobs.
type recorder struct {
	mu   sync.Mutex
	runs []string
}

func (r *recorder) job(name string) func() {
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.runs = append(r.runs, name)
	}
}

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.runs...)
}

// assertNext checks that the chat's next firing is at hh:mm in zone, within 24h.
func assertNext(t *testing.T, s *cronScheduler, chatID int64, hh, mm int, zone string) {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	next, ok := s.Next(chatID)
	if !ok {
		t.Fatalf("Next(%d): nothing scheduled", chatID)
	}
	if in := next.In(loc); in.Hour() != hh || in.Minute() != mm || in.Second() != 0 {
		t.Errorf("Next(%d) = %v, want %02d:%02d:00 in %s", chatID, in, hh, mm, zone)
	}
	if !next.After(now) || next.Sub(now) > 24*time.Hour {
		t.Errorf("Next(%d) = %v, want within 24h after %v", chatID, next, now)
	}
}

// runNow calls RunNow and fails instead of hanging when it deadlocks.
func runNow(t *testing.T, s *cronScheduler, chatID int64) bool {
	t.Helper()
	done := make(chan bool, 1)
	go func() { done <- s.RunNow(chatID) }()
	select {
	case ok := <-done:
		return ok
	case <-time.After(2 * time.Second):
		t.Fatalf("RunNow(%d) did not return: deadlock", chatID)
		return false
	}
}

func TestNext(t *testing.T) {
	tests := []struct {
		name       string
		notifyTime string
		timezone   string
		start      bool
		wantHH     int
		wantMM     int
	}{
		{name: "09:30 Europe/Moscow before Start", notifyTime: "09:30", timezone: "Europe/Moscow", wantHH: 9, wantMM: 30},
		{name: "07:00 Asia/Dubai before Start", notifyTime: "07:00", timezone: "Asia/Dubai", wantHH: 7, wantMM: 0},
		{name: "23:59 UTC before Start", notifyTime: "23:59", timezone: "UTC", wantHH: 23, wantMM: 59},
		{name: "09:30 Europe/Moscow after Start", notifyTime: "09:30", timezone: "Europe/Moscow", start: true, wantHH: 9, wantMM: 30},
		{name: "07:00 Asia/Dubai after Start", notifyTime: "07:00", timezone: "Asia/Dubai", start: true, wantHH: 7, wantMM: 0},
		{name: "23:59 UTC after Start", notifyTime: "23:59", timezone: "UTC", start: true, wantHH: 23, wantMM: 59},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			if tt.start {
				s.Start()
				t.Cleanup(s.Stop)
			}
			if err := s.Schedule(100, tt.notifyTime, tt.timezone, func() {}); err != nil {
				t.Fatalf("Schedule: %v", err)
			}

			assertNext(t, s, 100, tt.wantHH, tt.wantMM, tt.timezone)
		})
	}
}

func TestSchedule(t *testing.T) {
	tests := []struct {
		name       string
		notifyTime string
		timezone   string
		wantErr    string // "" = no error
		wantRuns   []string
		wantHH     int
		wantMM     int
		wantZone   string
	}{
		{
			name:       "rescheduling replaces the entry",
			notifyTime: "21:15",
			timezone:   "Asia/Tbilisi",
			wantRuns:   []string{"new"},
			wantHH:     21,
			wantMM:     15,
			wantZone:   "Asia/Tbilisi",
		},
		{
			name:       "time without a colon keeps the previous entry",
			notifyTime: "0900",
			timezone:   "UTC",
			wantErr:    `invalid time "0900"`,
			wantRuns:   []string{"old"},
			wantHH:     10,
			wantMM:     0,
			wantZone:   "UTC",
		},
		{
			name:       "12-hour time keeps the previous entry",
			notifyTime: "9am",
			timezone:   "UTC",
			wantErr:    `invalid time "9am"`,
			wantRuns:   []string{"old"},
			wantHH:     10,
			wantMM:     0,
			wantZone:   "UTC",
		},
		{
			name:       "hour out of range keeps the previous entry",
			notifyTime: "25:00",
			timezone:   "UTC",
			wantErr:    "25",
			wantRuns:   []string{"old"},
			wantHH:     10,
			wantMM:     0,
			wantZone:   "UTC",
		},
		{
			name:       "unknown zone keeps the previous entry",
			notifyTime: "09:00",
			timezone:   "Mars/Base",
			wantErr:    "Mars/Base",
			wantRuns:   []string{"old"},
			wantHH:     10,
			wantMM:     0,
			wantZone:   "UTC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			var rec recorder
			if err := s.Schedule(100, "10:00", "UTC", rec.job("old")); err != nil {
				t.Fatalf("first Schedule: %v", err)
			}

			err := s.Schedule(100, tt.notifyTime, tt.timezone, rec.job("new"))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Schedule: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error: got %v, want containing %q", err, tt.wantErr)
			}

			if n := len(s.c.Entries()); n != 1 {
				t.Errorf("cron entries: got %d, want 1", n)
			}
			assertNext(t, s, 100, tt.wantHH, tt.wantMM, tt.wantZone)
			if !runNow(t, s, 100) {
				t.Fatal("RunNow: nothing scheduled")
			}
			if got := rec.got(); !slices.Equal(got, tt.wantRuns) {
				t.Errorf("runs: got %v, want %v", got, tt.wantRuns)
			}
		})
	}
}

func TestRunNow(t *testing.T) {
	tests := []struct {
		name          string
		schedule      bool // chat 100 gets job before the call
		remove        bool // chat 100 is removed before the call
		stop          bool // the scheduler is started and stopped before the call
		chatID        int64
		job           func(s *cronScheduler, ran *atomic.Int32) func()
		wantOK        bool
		wantRan       int32
		wantScheduled bool // chat 100 is still scheduled after the call
	}{
		{
			name:          "runs the scheduled job",
			schedule:      true,
			chatID:        100,
			job:           func(_ *cronScheduler, ran *atomic.Int32) func() { return func() { ran.Add(1) } },
			wantOK:        true,
			wantRan:       1,
			wantScheduled: true,
		},
		{
			name:     "a job that removes its own chat does not deadlock",
			schedule: true,
			chatID:   100,
			job: func(s *cronScheduler, ran *atomic.Int32) func() {
				return func() { ran.Add(1); s.Remove(100) }
			},
			wantOK:        true,
			wantRan:       1,
			wantScheduled: false,
		},
		{
			name:     "a panicking job is recovered",
			schedule: true,
			chatID:   100,
			job: func(_ *cronScheduler, ran *atomic.Int32) func() {
				return func() { ran.Add(1); panic("boom") }
			},
			wantOK:        true,
			wantRan:       1,
			wantScheduled: true,
		},
		{
			name:          "works after Stop",
			schedule:      true,
			stop:          true,
			chatID:        100,
			job:           func(_ *cronScheduler, ran *atomic.Int32) func() { return func() { ran.Add(1) } },
			wantOK:        true,
			wantRan:       1,
			wantScheduled: true,
		},
		{
			name:          "false after Remove",
			schedule:      true,
			remove:        true,
			chatID:        100,
			job:           func(_ *cronScheduler, ran *atomic.Int32) func() { return func() { ran.Add(1) } },
			wantOK:        false,
			wantRan:       0,
			wantScheduled: false,
		},
		{
			name:          "false for an unknown chat",
			schedule:      true,
			chatID:        200,
			job:           func(_ *cronScheduler, ran *atomic.Int32) func() { return func() { ran.Add(1) } },
			wantOK:        false,
			wantRan:       0,
			wantScheduled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			var ran atomic.Int32
			if tt.schedule {
				if err := s.Schedule(100, "10:00", "UTC", tt.job(s, &ran)); err != nil {
					t.Fatalf("Schedule: %v", err)
				}
			}
			if tt.remove {
				s.Remove(100)
			}
			if tt.stop {
				s.Start()
				s.Stop()
			}

			if ok := runNow(t, s, tt.chatID); ok != tt.wantOK {
				t.Errorf("RunNow(%d): got %v, want %v", tt.chatID, ok, tt.wantOK)
			}
			if n := ran.Load(); n != tt.wantRan {
				t.Errorf("job runs: got %d, want %d", n, tt.wantRan)
			}
			if _, ok := s.Next(100); ok != tt.wantScheduled {
				t.Errorf("Next(100) ok: got %v, want %v", ok, tt.wantScheduled)
			}
			if _, ok := s.Next(tt.chatID); ok && !tt.wantOK {
				t.Errorf("Next(%d) ok: got true for a chat RunNow did not find", tt.chatID)
			}
		})
	}
}

// fireOnce is a cron.Schedule that fires a single time, at at.
type fireOnce struct{ at time.Time }

func (f fireOnce) Next(t time.Time) time.Time {
	if t.Before(f.at) {
		return f.at
	}
	return time.Time{} // never again
}

func TestStop(t *testing.T) {
	tests := []struct {
		name     string
		running  bool // a firing started by cron is still running when Stop is called
		remove   bool // the running job removes its own chat once released
		wantNext bool // chat 100 is still scheduled after Stop
	}{
		{name: "waits for a running job", running: true, wantNext: true},
		{name: "a running job may call Remove", running: true, remove: true, wantNext: false},
		{name: "returns with no running job", running: false, wantNext: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			started := make(chan struct{})
			release := make(chan struct{})
			var finished atomic.Bool
			job := func() {}
			if tt.running {
				job = func() {
					close(started)
					<-release
					if tt.remove {
						s.Remove(100)
					}
					finished.Store(true)
				}
			}
			// White-box: Schedule only takes daily times, so the entry is
			// added to the cron directly to fire once, in 10ms.
			id := s.c.Schedule(fireOnce{at: time.Now().Add(10 * time.Millisecond)}, cron.FuncJob(job))
			s.mu.Lock()
			s.entries[100] = id
			s.mu.Unlock()
			s.Start()
			if tt.running {
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					t.Fatal("cron did not start the job")
				}
			}

			stopped := make(chan struct{})
			go func() { s.Stop(); close(stopped) }()
			if tt.running {
				select {
				case <-stopped:
					t.Fatal("Stop returned while a job was running")
				case <-time.After(50 * time.Millisecond):
				}
				close(release)
			}
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("Stop did not return")
			}
			if finished.Load() != tt.running {
				t.Errorf("job finished before Stop returned: got %v, want %v", finished.Load(), tt.running)
			}
			if _, ok := s.Next(100); ok != tt.wantNext {
				t.Errorf("Next(100) ok: got %v, want %v", ok, tt.wantNext)
			}
		})
	}
}
```

- [ ] **Step 10: Run it to see it fail**

Run: `go test ./internal/scheduler/ -race -count=1`
Expected: build failure listing
```
internal/scheduler/cron_test.go:87:9: undefined: New
internal/scheduler/cron_test.go:165:9: undefined: New
internal/scheduler/cron_test.go:269:9: undefined: New
internal/scheduler/cron_test.go:324:9: undefined: New
```
then `FAIL	github.com/solympe/leetcode-tg-notifier/internal/scheduler [build failed]`.

- [ ] **Step 11: Rewrite the scheduler**

Run: `git rm -q internal/scheduler/scheduler.go`

Replace `internal/scheduler/cron.go` with:
```go
package scheduler

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// cronScheduler runs one daily cron entry per chat at HH:MM in an IANA zone.
// Every job runs wrapped in cron.Recover, so a panic is logged, not fatal.
type cronScheduler struct {
	c       *cron.Cron
	mu      sync.Mutex // guards entries; never held while a job runs
	entries map[int64]cron.EntryID
}

// New returns a scheduler that is not started yet. Schedule, RunNow and Next
// work before Start.
func New() *cronScheduler {
	return &cronScheduler{
		c:       cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger))),
		entries: make(map[int64]cron.EntryID),
	}
}

// Schedule runs job every day at notifyTime ("HH:MM") in timezone, replacing
// the chat's previous entry. The spec is parsed first, so an invalid time or
// zone returns an error and keeps the previous entry.
func (cs *cronScheduler) Schedule(chatID int64, notifyTime, timezone string, job func()) error {
	hour, minute, ok := strings.Cut(notifyTime, ":")
	if !ok {
		return fmt.Errorf("invalid time %q", notifyTime)
	}
	spec, err := cron.ParseStandard(fmt.Sprintf("CRON_TZ=%s %s %s * * *", timezone, minute, hour))
	if err != nil {
		return fmt.Errorf("parse schedule: %w", err)
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()
	if old, ok := cs.entries[chatID]; ok {
		cs.c.Remove(old)
	}
	id := cs.c.Schedule(spec, cron.FuncJob(job))
	cs.entries[chatID] = id
	log.Printf("Scheduled chat %d at %s %s (entry %d)", chatID, notifyTime, timezone, id)
	return nil
}

func (cs *cronScheduler) Remove(chatID int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if id, ok := cs.entries[chatID]; ok {
		cs.c.Remove(id)
		delete(cs.entries, chatID)
	}
}

// Start starts firing scheduled jobs. A second Start is a no-op.
func (cs *cronScheduler) Start() {
	cs.c.Start()
}

// Stop stops new firings and waits for the jobs cron has already started.
// It does not hold cs.mu while waiting, so a running job may call Remove.
// A RunNow in progress is not waited for: it runs in the caller's goroutine.
func (cs *cronScheduler) Stop() {
	<-cs.c.Stop().Done()
}

// RunNow runs the chat's scheduled job synchronously, exactly as cron would
// run it (Recover included), and reports whether the chat has one. cs.mu is
// released before the job runs, so a job that removes its own chat cannot
// deadlock. It also works after Stop. It is a test seam.
func (cs *cronScheduler) RunNow(chatID int64) bool {
	e, ok := cs.entry(chatID)
	if !ok {
		return false
	}
	e.WrappedJob.Run()
	return true
}

// Next returns the chat's next firing time, in time.Local, and whether the
// chat has a scheduled job. It works whether or not cron is running. It is a
// test seam.
func (cs *cronScheduler) Next(chatID int64) (time.Time, bool) {
	e, ok := cs.entry(chatID)
	if !ok {
		return time.Time{}, false
	}
	return e.Schedule.Next(time.Now()), true
}

// entry returns a snapshot of the chat's cron entry. It holds cs.mu only to
// look up the entry ID.
func (cs *cronScheduler) entry(chatID int64) (cron.Entry, bool) {
	cs.mu.Lock()
	id, ok := cs.entries[chatID]
	cs.mu.Unlock()
	if !ok {
		return cron.Entry{}, false
	}
	e := cs.c.Entry(id)
	return e, e.Valid()
}
```

- [ ] **Step 12: Re-run the scheduler tests**

Run: `go test ./internal/scheduler/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected:
```
--- PASS: TestNext
--- PASS: TestSchedule
--- PASS: TestRunNow
--- PASS: TestStop
ok  	github.com/solympe/leetcode-tg-notifier/internal/scheduler
```
Under `-v` the panicking-job row also logs cron's recovered panic with a stack trace (`cron: … panic, error=boom`); the grep hides it, and it is expected.

#### (d) app wiring

- [ ] **Step 13: Wire `app.New` per spec §4.2**

In `internal/app/app.go`, replace the local imports (lines 13-16)
```go
	"github.com/solympe/leetcode-tg-notifier/internal/bot"
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/scheduler"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
```
with
```go
	"github.com/solympe/leetcode-tg-notifier/internal/leetcode"
	"github.com/solympe/leetcode-tg-notifier/internal/notifier"
	"github.com/solympe/leetcode-tg-notifier/internal/scheduler"
	"github.com/solympe/leetcode-tg-notifier/internal/storage"
	"github.com/solympe/leetcode-tg-notifier/internal/telegram"
```

Replace lines 45-53
```go
type application struct {
	api   *tgbotapi.BotAPI // concrete third-party type: GetUpdatesChan, StopReceivingUpdates, GetUpdates
	sched jobRunner
	bot   updateHandler
}

// New builds the object graph and restores every stored schedule. It calls
// getMe, so it fails on a bad token or an unreachable Telegram endpoint.
func New(_ context.Context, cfg Config) (*application, error) {
```
with
```go
type application struct {
	api        *tgbotapi.BotAPI // concrete third-party type: GetUpdatesChan, StopReceivingUpdates, GetUpdates
	sched      jobRunner
	bot        updateHandler
	cancelJobs context.CancelFunc // cancels the jobs root: the parent of every scheduled job's ctx
}

// New builds the object graph and restores every stored schedule. It calls
// getMe, so it fails on a bad token or an unreachable Telegram endpoint. ctx
// bounds the restore; the jobs root is detached from it and cancelled when
// Run returns.
func New(ctx context.Context, cfg Config) (*application, error) {
```

Replace lines 65-73
```go
	b := bot.New(api, api.Self.UserName, store, leetcode.NewHTTPClient(cfg.LeetCodeEndpoint, nil), nil)
	sched := scheduler.NewCronScheduler(b.SendDailyProblem)
	b.SetScheduler(sched)
	for _, c := range store.All() {
		if err := sched.Schedule(c.ChatID, c); err != nil {
			log.Printf("restore schedule for %d: %v", c.ChatID, err)
		}
	}
	return &application{api: api, sched: sched, bot: b}, nil
```
with
```go
	jobs, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))
	sched := scheduler.New()
	svc := notifier.New(jobs, store, leetcode.NewHTTPClient(cfg.LeetCodeEndpoint, nil), sched, telegram.NewSender(api), time.Now)
	if err := svc.Restore(ctx); err != nil {
		log.Print(err) // never fatal, as before
	}
	return &application{
		api:        api,
		sched:      sched,
		bot:        telegram.NewHandler(api, svc, api.Self.UserName),
		cancelJobs: cancelJobs,
	}, nil
```

Replace lines 76-80
```go
// Run handles updates one at a time until ctx is done, then shuts down
// gracefully: it drains every update already received, confirms the last
// batch and waits for running jobs. Call it once per application.
func (a *application) Run(ctx context.Context) {
	a.sched.Start()
```
with
```go
// Run handles updates one at a time until ctx is done, then shuts down
// gracefully: it drains every update already received, confirms the last
// batch, waits for running jobs and cancels the jobs root. Call it once per
// application.
func (a *application) Run(ctx context.Context) {
	defer a.cancelJobs() // 5. backstop: no job context outlives Run
	a.sched.Start()
```
The rest of `Run` and `handle` is unchanged. `defer a.cancelJobs()` is registered first, so it runs last: after `sched.Stop()` has waited for running jobs (spec §6.5 step 5).

#### (e) delete the old bot

- [ ] **Step 14: Delete `internal/bot`**

Run: `git rm -r -q internal/bot`

- [ ] **Step 15: Build and vet the whole tree**

Run: `go build ./... && go vet ./... && go vet -tags integration ./internal/app/`
Expected: no output. `go vet` also proves `*jsonStorage` satisfies `notifier.chatStore`, `*httpClient` satisfies `problemSource`, `*cronScheduler` satisfies `dailyScheduler` and `app.jobRunner`, and `*handler` satisfies `app.updateHandler`.

#### (f) generate and tidy

- [ ] **Step 16: Regenerate mocks and tidy the module**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./... && go mod tidy && git status --porcelain -- internal/leetcode/mocks internal/notifier/mocks internal/telegram/mocks go.mod go.sum
```
Expected: no output (no port changed in this task; tidy removes nothing, because `robfig/cron`, `tgbotapi` and `go.uber.org/mock` are all still imported).

- [ ] **Step 17: Task gate**

Run:
```bash
go build ./... && go test ./... -race -count=1 && make lint && make test-integration && git diff --exit-code HEAD -- 'internal/app/*_test.go' && echo SUITE-UNCHANGED
```
Expected: `ok` for `internal/domain`, `internal/leetcode`, `internal/notifier`, `internal/scheduler`, `internal/storage` and `internal/telegram` (`?` / `[no test files]` for the module root, `internal/app` and the three `mocks` packages); `0 issues.`; the integration run prints `ok  	github.com/solympe/leetcode-tg-notifier/internal/app` (rows 1–16, 20–21 and `TestNewFails` against the new code, untouched); then `SUITE-UNCHANGED`.

- [ ] **Step 18: Commit**

```bash
git add internal/storage/json.go internal/storage/json_test.go \
  internal/leetcode/http.go internal/leetcode/http_test.go \
  internal/scheduler/cron.go internal/scheduler/cron_test.go \
  internal/app/app.go
git status --porcelain
```
Expected: only staged entries (`M ` and `D `): the seven files above, plus `D ` for `internal/storage/storage.go`, `internal/leetcode/client.go`, `internal/leetcode/client_test.go`, `internal/leetcode/format_test.go`, `internal/scheduler/scheduler.go` and the 21 files under `internal/bot/`. Nothing unstaged or untracked.
```bash
git commit -F - <<'EOF'
refactor: switch to domain, notifier and telegram; delete internal/bot

- storage persists domain.Chat with ctx-first Get/Upsert/Update/Delete/All;
  Upsert and Update share mutate, All is sorted, and every method returns
  ctx.Err() before locking. The file format is unchanged apart from the key
  order inside daily_pick; a rollback test decodes the golden fixture with
  the old structs.
- leetcode returns domain.Problem values and binds each request to ctx;
  client.go and the Format* helpers are gone (they live in telegram/view.go).
- scheduler takes a job closure per Schedule, parses before replacing and
  wraps every job in cron.Recover; SendFunc and the storage import are gone.
- app.New wires store, api, jobs root, scheduler, notifier, sender and
  handler with no late binding and restores through the service; Run
  cancels the jobs root when it returns.
- internal/bot is deleted.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [ ] **Step 19: Extra gate and mocks check on the committed tree**

Run:
```bash
git diff --exit-code HEAD~1 -- 'internal/app/*_test.go' && echo SUITE-UNCHANGED
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `SUITE-UNCHANGED`, then `CLEAN` and nothing else. The first line is spec §10 step 7's extra gate: the characterization suite passed against the old code in Task 5 and passes, byte-identical, against the new code here.

---

### Task 11: Deliberate-fix rows and docs

Spec §10 step 8.

**Files:**
- Modify: `internal/app/integration_test.go` (`TestIntegration` table: rows 17–19 between row 16 and row 20)
- Modify: `README.md:33` (Go version), `:56-60` (Development)
- Modify: `CLAUDE.md:62-89` (Test Style example)
- Outside the repo: `~/.claude/projects/-Users-solympe-Desktop-git-leetcode-tg-notifier/memory/MEMORY.md`

**Interfaces:**
Consumes (Tasks 4-5 harness, spec §8.2–8.4; Task 10 wiring):
- The `TestIntegration` table row type `struct{ name string; seed string; run func(e *env) }`, whose loop runs `t.Parallel()`, `e := newEnv(t)`, `e.seed(tt.seed)` when non-empty, then `e.start()`, then `tt.run(e)`.
- `env` field `t *testing.T`; `e.say(chatID int64, from tgbotapi.User, text string)`; `e.sent(chatID int64, text string, kb keyboard) int` (returns the message ID); `e.pressOn(chatID int64, from tgbotapi.User, msgID int, data string) string` (returns the cbID); `e.expectAnswer(cbID string) string`; `e.expectQuiet(chatID int64)`; `e.sync()`; `e.stop()`; `e.fire(chatID int64) bool`; `e.lc.dailyCalls() int`; the strict cleanup invariants of §8.3.
- `alice`, and the literal-text constants `aboutText` (`"For questions, suggestions, and bug reports — DM @solympe"`) and `menuExpiredText` (`"⚠️ This menu is no longer active. Use /setup or /difficulty to start again."`).
- Task 10: `a.cancelJobs` deferred in `Run`; `notifier` job closures derive their ctx from the jobs root; `telegram.sender` refuses calls on a done ctx.
- Task 7: `notifier.New(jobs context.Context, store chatStore, lc problemSource, sched dailyScheduler, out messenger, now func() time.Time) *service`, `(*service).SendDaily(ctx, chatID)`, and the generated `notifier/mocks` (`MockchatStore`, `MockproblemSource`, `MockdailyScheduler`, `Mockmessenger`) used by the CLAUDE.md example.

Produces: integration rows 17–19 (the table now holds rows 1–21); updated README and CLAUDE.md. No Go API changes.

- [ ] **Step 1: Add rows 17–19**

In `internal/app/integration_test.go`, insert after row 16 (`"16 shutdown drains exactly once"`) and before row 20 (`"20 restart mid-dialog expires its buttons, not Done"`):
```go
		{
			// Deviation (a): today these callbacks are never answered.
			name: "17 unknown callbacks are answered",
			run: func(e *env) {
				e.say(1700, alice, "/about")
				m := e.sent(1700, aboutText, nil)
				for _, data := range []string{"bogus", "/start", "done:x"} {
					if got := e.expectAnswer(e.pressOn(1700, alice, m, data)); got != menuExpiredText {
						e.t.Errorf("%q answered %q, want %q", data, got, menuExpiredText)
					}
				}
				e.expectQuiet(1700)
			},
		},
		{
			// Deviation (b): today ties come in random map order.
			name: "18 rating ties are ordered by name",
			seed: `{"chats":{"-1800":{"chat_id":-1800,"notify_time":"20:00","timezone":"UTC","members":{` +
				`"9":{"name":"Carol","count":2,"last_solved_date":"2026-09-27"},` +
				`"7":{"name":"Alice","count":2,"last_solved_date":"2026-09-27"},` +
				`"8":{"name":"Bob","count":3,"last_solved_date":"2026-09-27"}}}}}`,
			run: func(e *env) {
				e.say(-1800, alice, "/rating")
				e.sent(-1800, "🏆 <b>Rating</b>\n\n🥇 Bob — 3\n🥈 Alice — 2\n🥉 Carol — 2\n", nil)
			},
		},
		{
			// A job run after Run returned gets a cancelled ctx: the daily
			// request never leaves the client, and the fetch-failed notice is
			// refused by the sender, so the strict invariants at cleanup see
			// no Bot API call for the chat.
			name: "19 jobs root is cancelled when Run returns",
			seed: `{"chats":{"1900":{"chat_id":1900,"notify_time":"09:00","timezone":"UTC","members":null}}}`,
			run: func(e *env) {
				e.sync()
				e.stop()
				n := e.lc.dailyCalls()
				if !e.fire(1900) {
					e.t.Fatal("fire(1900) = false, want the restored entry")
				}
				if got := e.lc.dailyCalls(); got != n {
					e.t.Errorf("dailyCalls after Run returned: got %d, want %d", got, n)
				}
			},
		},
```

- [ ] **Step 2: Run the new rows**

Run: `go test -tags integration -race -count=1 ./internal/app/ -run 'TestIntegration/(17|18|19)' -v 2>&1 | grep -E -- '--- (PASS|FAIL)|^(ok|FAIL)'`
Expected:
```
--- PASS: TestIntegration
    --- PASS: TestIntegration/17_unknown_callbacks_are_answered
    --- PASS: TestIntegration/18_rating_ties_are_ordered_by_name
    --- PASS: TestIntegration/19_jobs_root_is_cancelled_when_Run_returns
ok  	github.com/solympe/leetcode-tg-notifier/internal/app
```
(the three subtest lines may come in any order; each ends with its duration).

- [ ] **Step 3: Confirm the rows pin deliberate changes (they fail on the pre-switch tree)**

Run:
```bash
git worktree add -q ../pre-switch HEAD~1
cp internal/app/integration_test.go ../pre-switch/internal/app/
(cd ../pre-switch && go test -tags integration -race -count=1 ./internal/app/ -run 'TestIntegration/(17|19)' 2>&1 | grep -E -- '--- FAIL|dailyCalls|not answered'; true)
git worktree remove --force ../pre-switch
git status --porcelain
```
`HEAD~1` is the tree before Task 10 (the old `internal/bot` wiring). Expected (subtest order may vary):
```
--- FAIL: TestIntegration
    --- FAIL: TestIntegration/19_jobs_root_is_cancelled_when_Run_returns
        integration_test.go:602: dailyCalls after Run returned: got 1, want 0
    --- FAIL: TestIntegration/17_unknown_callbacks_are_answered
        integration_test.go:568: callback cb1: not answered within 5s
```
then only ` M internal/app/integration_test.go`. The old router never answers unknown data, and the old job ignores any ctx. Row 18 is left out of this check: on the old code it fails only when map order puts Carol before Alice.

- [ ] **Step 4: Prove the Review Focus pins bite, then revert**

Each mutation breaks one pinned behaviour of the new code: a fatal `Restore`, a lost `NewBotAPI: ` prefix, a temp-file-and-rename save, and a half-loaded damaged file.

Run:
```bash
perl -0pi -e 's/(svc\.Restore\(ctx\); err != nil \{\n\t\t)log\.Print\(err\)/${1}return nil, err/' internal/app/app.go
go test -tags integration -race -count=1 -run 'TestIntegration/21' ./internal/app/ 2>&1 | grep -E -- '--- FAIL|New: restore'
git checkout internal/app/app.go
perl -pi -e 's/"NewBotAPI: %w"/"%w"/' internal/app/app.go
go test -tags integration -race -count=1 -run 'TestNewFails' ./internal/app/ 2>&1 | grep -E -- '--- FAIL|want the prefix'
git checkout internal/app/app.go
perl -0pi -e 's/os\.WriteFile\(s\.path, data, 0644\); err != nil \{\n\t\treturn fmt\.Errorf\("write: %w", err\)\n\t\}\n\treturn nil/os.WriteFile(s.path+".tmp", data, 0644); err != nil {\n\t\treturn fmt.Errorf("write: %w", err)\n\t}\n\treturn os.Rename(s.path+".tmp", s.path)/' internal/storage/json.go
go test -count=1 -run 'TestSaveInPlace' ./internal/storage/ 2>&1 | grep -E -- '--- FAIL|replaced by another'
git checkout internal/storage/json.go
perl -0pi -e 's/(starting fresh: %v", err\)\n)\t\ts\.data = jsonFile\{Chats: make\(map\[string\]domain\.Chat\)\}\n/$1/' internal/storage/json.go
go test -count=1 -run 'TestLoadDamagedFile' ./internal/storage/ 2>&1 | grep -E -- '--- FAIL'
git checkout internal/storage/json.go
git status --porcelain
```
Expected:
```
--- FAIL: TestIntegration (0.00s)
    --- FAIL: TestIntegration/21_an_unschedulable_stored_chat_does_not_block_the_others (0.01s)
        integration_test.go:676: New: restore schedule for 2100: invalid time "0900"
Updated 1 path from the index
--- FAIL: TestNewFails (0.01s)
    --- FAIL: TestNewFails/a_wrong_BOT_TOKEN (0.00s)
        integration_test.go:718: New: got "", want the prefix "NewBotAPI: "
Updated 1 path from the index
--- FAIL: TestSaveInPlace (0.00s)
    --- FAIL: TestSaveInPlace/Upsert (0.00s)
        json_test.go:815: config.json was replaced by another file, want it rewritten in place
    --- FAIL: TestSaveInPlace/Update (0.00s)
        json_test.go:815: config.json was replaced by another file, want it rewritten in place
    --- FAIL: TestSaveInPlace/Delete (0.00s)
        json_test.go:815: config.json was replaced by another file, want it rewritten in place
Updated 1 path from the index
--- FAIL: TestLoadDamagedFile (0.00s)
    --- FAIL: TestLoadDamagedFile/a_field_of_the_wrong_type (0.00s)
Updated 1 path from the index
 M internal/app/integration_test.go
```
The durations vary. The `integration_test.go` line numbers are those of the file after Step 1. The wrong-type row fails because a partial `json.Unmarshal` fills chat 42 before it reports the type error.

- [ ] **Step 5: Update the README**

In `README.md`, replace line 33
```
- Go 1.24+
```
with
```
- Go 1.26+
```
and replace the Development block (lines 56-60)
````
```bash
make test   # run tests
make lint   # run linter
make build  # build binary
```
````
with
````
```bash
make test              # run tests
make test-integration  # run tests plus the hermetic integration suite (in-process fake Telegram and LeetCode)
make lint              # run linter
make generate          # regenerate the gomock mocks (needs mockgen v0.6.0)
make build             # build binary
```
````

- [ ] **Step 6: Update the CLAUDE.md Test Style example**

In `CLAUDE.md`, replace the example between ```` ```go ```` and ```` ``` ```` under "Initialize mocks with their `EXPECT()` calls inside the factory, not in the loop body." (lines 62-89, which still call the deleted `New(tt.senderMock(ctrl), "TestBot", tt.storeMock(ctrl), tt.lcMock(ctrl), tt.schedMock(ctrl))`) with:
````
```go
func TestSendDaily(t *testing.T) {
    ctx := t.Context() // once, before the table: the factories and the call share it
    daily := domain.Problem{Date: "2026-09-28", ID: "4", Title: "Median of Two Sorted Arrays", Difficulty: domain.Hard}

    tests := []struct {
        name      string
        storeMock func(*gomock.Controller) *mocks.MockchatStore
        lcMock    func(*gomock.Controller) *mocks.MockproblemSource
        schedMock func(*gomock.Controller) *mocks.MockdailyScheduler
        outMock   func(*gomock.Controller) *mocks.Mockmessenger
    }{
        {
            name:      "sends the official daily",
            storeMock: mocks.NewMockchatStore, // no expectations — pass constructor directly
            lcMock: func(ctrl *gomock.Controller) *mocks.MockproblemSource {
                m := mocks.NewMockproblemSource(ctrl)
                m.EXPECT().FetchDaily(gomock.Eq(ctx)).Return(daily, nil)
                return m
            },
            schedMock: mocks.NewMockdailyScheduler,
            outMock: func(ctrl *gomock.Controller) *mocks.Mockmessenger {
                m := mocks.NewMockmessenger(ctrl)
                // IDs are typed: gomock.Eq(100) never matches an int64
                m.EXPECT().SendProblem(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Eq(domain.Pick{Problem: daily})).Return(nil)
                return m
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctrl := gomock.NewController(t)
            s := New(ctx, tt.storeMock(ctrl), tt.lcMock(ctrl), tt.schedMock(ctrl), tt.outMock(ctrl), time.Now)
            s.SendDaily(ctx, 100)
        })
    }
}
```
````
The text around it, including the `storeMock: mocks.NewMockchatStore` snippet, is unchanged. This example compiles as-is in `package notifier` (checked by pasting it into a throwaway `_test.go` with imports `testing`, `time`, `gomock`, `domain` and `notifier/mocks`, running it, and deleting the file).

- [ ] **Step 7: Task gate**

Run:
```bash
go build ./... && go test ./... -race -count=1 && make lint && make test-integration
```
Expected: `ok` for `internal/domain`, `internal/leetcode`, `internal/notifier`, `internal/scheduler`, `internal/storage` and `internal/telegram`; `0 issues.`; the integration run prints `ok  	github.com/solympe/leetcode-tg-notifier/internal/app` (rows 1–21 and `TestNewFails`).

- [ ] **Step 8: Commit**

```bash
git add internal/app/integration_test.go README.md CLAUDE.md
git commit -F - <<'EOF'
test(app): pin the deliberate changes; docs for the new layout

- Integration rows 17-19: unknown callbacks get the menu-expired toast,
  /rating ties are ordered by name, and a job run after Run returns gets
  a cancelled ctx (no LeetCode request, no Bot API call).
- README: Go 1.26+, make test-integration and make generate.
- CLAUDE.md: the Test Style example uses notifier.New instead of the
  deleted bot.New.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>
EOF
```

- [ ] **Step 9: Mocks check on the committed tree**

Run:
```bash
PATH="$(go env GOPATH)/bin:$PATH" go generate ./...
git status --porcelain
test -z "$(git status --porcelain)" && echo CLEAN
```
Expected: `CLEAN` and nothing else.

- [ ] **Step 10: Refresh the project memory notes (outside the repo, not committed)**

Replace the whole of `~/.claude/projects/-Users-solympe-Desktop-git-leetcode-tg-notifier/memory/MEMORY.md` with:
````markdown
# LeetCode TG Notifier — Memory

## Project
Go Telegram bot that sends LeetCode daily problem at a user-configured time.

## Key Files
- `main.go` — entry point: reads `BOT_TOKEN`/`STORAGE_PATH`, builds a `signal.NotifyContext`, calls `app.New` + `Run`
- `config.json` — persisted chat settings (created at runtime); its format is the `internal/domain` JSON tags
- `go.mod` — module `github.com/solympe/leetcode-tg-notifier`, go 1.26.1
- `Makefile` — targets: build, test, test-integration, lint, generate, tidy
- `.golangci.yml` — golangci-lint v2 config (`run.build-tags: [integration]`)
- `docs/superpowers/specs/2026-09-28-architecture-refactor-design.md` — architecture spec

## Dependencies
- `github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1`
- `github.com/robfig/cron/v3 v3.0.1`
- `go.uber.org/mock v0.6.0` (mockgen v0.6.0 for `make generate`)

## Architecture (compact hexagonal-lite)
- `internal/app` — the only composition root: `Config`, `New(ctx, cfg)` (store → getMe → jobs root ctx → `scheduler.New()` → `notifier.New` → `Restore` → `telegram.NewHandler`), `Run(ctx)` with graceful shutdown (drain, confirm last batch, `sched.Stop`, `cancelJobs`); ports `jobRunner`, `updateHandler`
- `internal/domain` — `Chat`, `Member`, `Problem`, `Pick{Problem; DailyDifficulty}` with the config.json tags; rules `Wants`, `PickFor`, `RecordSolve`, `Standings`; `ErrNotSubscribed`, `ErrAlreadySolved`, `ErrBlocked`; stdlib only
- `internal/notifier` — use cases on `*service`: `Restore`, `Subscribe`, `SetDifficulties`, `Unsubscribe`, `Subscription`, `Solve`, `SendToday` (compare-and-set pick of the day), `SendDaily`; ports in `deps.go`: `chatStore`, `problemSource`, `dailyScheduler`, `messenger`
- `internal/telegram` — `handler` (routing, commands table, one-answer (toast, act) callback protocol, panic recovery), `dialog.go` (sessions for /setup and /difficulty), `view.go` (every text, keyboard, `formatPick`), `sender.go` (403 → `domain.ErrBlocked`, refuses calls on a done ctx); ports in `deps.go`: `botAPI`, `service`
- `internal/leetcode` — `httpClient`: `FetchDaily(ctx)`, `FetchRandom(ctx, difficulties)` returning `domain.Problem`; port `randSource`
- `internal/storage` — `jsonStorage`: ctx-first `Get`/`Upsert`/`Update`/`Delete`/`All` over `domain.Chat`; `testdata/legacy_config.json` golden fixture from the pre-refactor binary
- `internal/scheduler` — `cronScheduler`: `Schedule(chatID, "HH:MM", tz, job func())`, `Remove`, `Start`, `Stop`, test seams `RunNow`/`Next`; jobs wrapped in `cron.Recover`

## Mocks
- One `mocks/` per consumer, `mockgen -source`: `notifier/deps.go`, `telegram/deps.go`, `leetcode/http.go`
- `make generate`; CI fails when `git status --porcelain` is not empty afterwards

## Tests
- Unit: table-driven; mock factories are struct fields; `ctx := t.Context()` once per Test before the table; typed IDs in matchers (`gomock.Eq(int64(100))`)
- Integration: `make test-integration` (`//go:build integration`, white-box `package app`): harness `env` (start/stop/restart/seed/stored/fire/scheduledAt/say/press/pressOn/sent/edited/rekeyed/expectAnswer/sync/expectQuiet), fake Telegram and fake LeetCode, strict invariants at cleanup; rows 1–21 (17–19 pin deviations a, b and the jobs root; 20–21 and `TestNewFails` pin the review focus)

## golangci-lint v2 notes
- `version: "2"` required at top of config
- `goimports` is a FORMATTER → goes in `formatters:` section, not `linters:`
- `revive`: `enable-default-rules: true`; `exported`, `package-comments` and `unexported-return` disabled
- Import grouping: stdlib | blank | third-party | blank | local (github.com/solympe/...)
- Test-file errcheck exclusion lives in `linters.exclusions.rules` (v1 `issues.exclude-rules` fails `config verify`)

## Run
```bash
export BOT_TOKEN=<token>
go run main.go
```

## Commands
/start, /about, /setup, /difficulty, /today, /daily, /status, /rating, /unsubscribe
````
