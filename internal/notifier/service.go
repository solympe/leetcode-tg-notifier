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
