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
