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
