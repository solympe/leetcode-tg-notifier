# CLAUDE.md — Project Rules

## Interface Design

**Never define public interfaces in the same package as their implementation.**
Interfaces belong at the point of consumption, not production.

```go
// BAD — storage package defines its own interface
package storage
type Storage interface { Get(...); Set(...); Delete(...) }

// GOOD — consumer defines exactly what it needs
package bot
type chatStore interface { Get(...); Set(...); Delete(...) }
```

**Return concrete types from constructors, not interfaces. The struct itself is private.**
```go
// BAD — returns interface
func NewJSONStorage(path string) (Storage, error)

// BAD — returns exported struct
func NewJSONStorage(path string) (*JSONStorage, error)

// GOOD — private struct, returned directly
type jsonStorage struct { ... }

func NewJSONStorage(path string) (*jsonStorage, error) { return &jsonStorage{...}, nil }
```

This applies everywhere: `New()` / `NewXxx()` always returns `*privateStruct`.
The caller gets a usable value via type inference without ever naming the type.

## Mocking

Use `go.uber.org/mock/mockgen`.

**`//go:generate` must be in a regular `.go` file — never in `_test.go`.**
`go generate` skips test files by design (hard toolchain constraint, no workaround).
Place the directive in the source file that defines the interfaces being mocked, or in a dedicated `generate.go` file.

**Use `-source` mode** when mocking private interfaces (the common case):
```go
//go:generate mockgen -source=bot.go -destination=mocks/mock_deps.go -package=mocks
```

**One `mocks/` directory per consuming package.** All dependency mocks for a package live in a single generated file, not scattered across packages.

After changing any interface, regenerate with:
```bash
go generate ./...
```

## Test Style

**Always use table-driven tests.**

**Mock factories are struct fields** with signature `func(*gomock.Controller) *mocks.MockX`.
Initialize mocks with their `EXPECT()` calls inside the factory, not in the loop body.

```go
func TestFoo(t *testing.T) {
    tests := []struct {
        name      string
        storeMock func(*gomock.Controller) *mocks.MockchatStore
        lcMock    func(*gomock.Controller) *mocks.MocklcFetcher
        wantErr   string
    }{
        {
            name: "happy path",
            storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
                m := mocks.NewMockchatStore(ctrl)
                m.EXPECT().Get(int64(1)).Return(cfg, true)
                return m
            },
            lcMock: mocks.NewMocklcFetcher, // no expectations — pass constructor directly
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctrl := gomock.NewController(t)
            b := New(tt.senderMock(ctrl), "TestBot", tt.storeMock(ctrl), tt.lcMock(ctrl), tt.schedMock(ctrl))
            // ...
        })
    }
}
```

When a mock needs no expectations, pass the constructor directly as the factory value:
```go
storeMock: mocks.NewMockchatStore, // equivalent to func(ctrl) *Mock { return New(ctrl) }
```

Use `gomock.Eq(expectedValue)` to assert exact arguments instead of capturing via `DoAndReturn` where possible.

## Error Handling

Use `errors.As` for type-checking errors (handles wrapping):
```go
// BAD
if tgErr, ok := err.(*tgbotapi.Error); ok && tgErr.Code == 403 {

// GOOD
func isBotBlocked(err error) bool {
    var tgErr *tgbotapi.Error
    return errors.As(err, &tgErr) && tgErr.Code == http.StatusForbidden
}
```

Use `net/http` status constants (`http.StatusForbidden`) instead of magic numbers.

Extract named predicates and helper methods for non-trivial error handling logic.

## Code Style

- Import grouping: `stdlib` | _(blank line)_ | `third-party` | _(blank line)_ | `local (github.com/solympe/...)`
- No public interfaces in packages that also contain the implementation
- No hand-written mocks — always use gomock
- No `//go:generate` in `_test.go` files

## Running

```bash
export BOT_TOKEN=<token>
go run main.go
```

```bash
go test ./...
go build ./...
make lint
```
