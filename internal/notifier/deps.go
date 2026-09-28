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
