package notifier

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/notifier/mocks"
)

// dailyDate is the date of every daily below and the UTC day of clock.
const dailyDate = "2026-09-28"

var errDisk = errors.New("disk full")

// clock is 01:30 on September 29 in UTC+3, which is still September 28 in UTC.
func clock() time.Time {
	return time.Date(2026, time.September, 29, 1, 30, 0, 0, time.FixedZone("UTC+3", 3*60*60))
}

func dailyOf(d string) domain.Problem {
	return domain.Problem{Date: dailyDate, ID: "4", Title: "Median of Two Sorted Arrays", Link: "/problems/median-of-two-sorted-arrays/", Difficulty: d, Tags: []string{"Array"}}
}

func officialPick(d string) domain.Pick { return domain.Pick{Problem: dailyOf(d)} }

// twoSum, climbingStairs and addTwoNumbers are draws as FetchRandom returns them: without a Date.
func twoSum() domain.Problem {
	return domain.Problem{ID: "1", Title: "Two Sum", Link: "/problems/two-sum/", Difficulty: domain.Easy, Tags: []string{"Array"}}
}

func climbingStairs() domain.Problem {
	return domain.Problem{ID: "70", Title: "Climbing Stairs", Link: "/problems/climbing-stairs/", Difficulty: domain.Easy, Tags: []string{"Math"}}
}

func addTwoNumbers() domain.Problem {
	return domain.Problem{ID: "2", Title: "Add Two Numbers", Link: "/problems/add-two-numbers/", Difficulty: domain.Medium, Tags: []string{"Math"}}
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

// cloneChat deep-copies c as the store does, so fn cannot reach want through
// a shared map or slice.
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

// applyUpdate stands in for chatStore.Update of stored: it fails the test
// unless fn leaves want and reports wantWrite, then returns found, err.
func applyUpdate(ctrl *gomock.Controller, stored, want domain.Chat, wantWrite bool, err error) func(context.Context, int64, func(*domain.Chat) bool) (bool, error) {
	return func(_ context.Context, _ int64, fn func(*domain.Chat) bool) (bool, error) {
		c := cloneChat(stored)
		if write := fn(&c); write != wantWrite {
			ctrl.T.Errorf("Update fn reported write=%v, want %v", write, wantWrite)
		}
		if !reflect.DeepEqual(c, want) {
			ctrl.T.Errorf("Update left\n %+v (pick %+v)\nwant\n %+v (pick %+v)", c, c.DailyPick, want, want.DailyPick)
		}
		return true, err
	}
}

// TestSend covers SendToday and, in the daily rows, SendDaily. What the
// integration suite drives end to end (rows 6-9, 13 and 14: any-difficulty
// chats, a new day's pick, concurrent sends, the 403 on a cron send, the
// outage) and what domain's Wants and PickFor tables pin is left to them.
func TestSend(t *testing.T) {
	ctx := t.Context()
	members := map[string]domain.Member{"7": {Name: "Alice", Count: 3, LastSolvedDate: dailyDate}}

	fetches := func(err error) func(*gomock.Controller) *mocks.MockproblemSource {
		return func(ctrl *gomock.Controller) *mocks.MockproblemSource {
			m := mocks.NewMockproblemSource(ctrl)
			m.EXPECT().FetchDaily(gomock.Eq(ctx)).Return(dailyOf(domain.Hard), err)
			return m
		}
	}
	// draws serves a Hard daily, then one FetchRandom(set) returning r, err.
	draws := func(set []string, r domain.Problem, err error) func(*gomock.Controller) *mocks.MockproblemSource {
		return func(ctrl *gomock.Controller) *mocks.MockproblemSource {
			m := fetches(nil)(ctrl)
			m.EXPECT().FetchRandom(gomock.Eq(ctx), gomock.Eq(set)).Return(r, err)
			return m
		}
	}
	gets := func(c domain.Chat, ok bool, err error) func(*gomock.Controller) *mocks.MockchatStore {
		return func(ctrl *gomock.Controller) *mocks.MockchatStore {
			m := mocks.NewMockchatStore(ctrl)
			m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(c, ok, err)
			return m
		}
	}
	// picks serves Get(100) = got, then one Update that finds cur, must leave
	// want and report wantWrite, and returns err.
	picks := func(got, cur, want domain.Chat, wantWrite bool, err error) func(*gomock.Controller) *mocks.MockchatStore {
		return func(ctrl *gomock.Controller) *mocks.MockchatStore {
			m := mocks.NewMockchatStore(ctrl)
			gomock.InOrder(
				m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(got, true, nil),
				m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).
					DoAndReturn(applyUpdate(ctrl, cur, want, wantWrite, err)),
			)
			return m
		}
	}
	sends := func(p domain.Pick, err error) func(*gomock.Controller) *mocks.Mockmessenger {
		return func(ctrl *gomock.Controller) *mocks.Mockmessenger {
			m := mocks.NewMockmessenger(ctrl)
			m.EXPECT().SendProblem(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Eq(p)).Return(err)
			return m
		}
	}
	fetchFailed := func(err error) func(*gomock.Controller) *mocks.Mockmessenger {
		return func(ctrl *gomock.Controller) *mocks.Mockmessenger {
			m := mocks.NewMockmessenger(ctrl)
			m.EXPECT().SendFetchFailed(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(err)
			return m
		}
	}
	easy := subscribed(domain.Easy)
	easyPick := randomPick(twoSum(), dailyDate)

	tests := []struct {
		name      string
		daily     bool // SendDaily instead of SendToday
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		lcMock    func(*gomock.Controller) *mocks.MockproblemSource
		schedMock func(*gomock.Controller) *mocks.MockdailyScheduler
		outMock   func(*gomock.Controller) *mocks.Mockmessenger
	}{
		{
			name:      "store read error sends fetch-failed",
			storeMock: gets(domain.Chat{}, false, context.DeadlineExceeded),
			lcMock:    fetches(nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   fetchFailed(nil),
		},
		{
			name:      "subscribed daily beats a same-day pick",
			storeMock: gets(withPick(subscribed(domain.Easy, domain.Hard), easyPick), true, nil),
			lcMock:    fetches(nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(officialPick(domain.Hard), nil),
		},
		{
			name:      "valid pick is resent without a random fetch or a save",
			storeMock: gets(withPick(easy, easyPick), true, nil),
			lcMock:    fetches(nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(easyPick, nil),
		},
		{
			name: "random draw of every subscribed difficulty is saved onto the current chat",
			storeMock: picks(
				subscribed(domain.Easy, domain.Medium),
				withMembers(subscribed(domain.Easy, domain.Medium), members),
				withPick(withMembers(subscribed(domain.Easy, domain.Medium), members), easyPick),
				true, nil,
			),
			lcMock:    draws([]string{domain.Easy, domain.Medium}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(easyPick, nil),
		},
		{
			// The chat switched from Easy to Medium after today's Easy pick was
			// saved, so the pick is rejected before the draw and under the save.
			name: "same-day pick of a difficulty no longer subscribed is replaced",
			storeMock: picks(
				withPick(subscribed(domain.Medium), easyPick),
				withPick(subscribed(domain.Medium), easyPick),
				withPick(subscribed(domain.Medium), randomPick(addTwoNumbers(), dailyDate)),
				true, nil,
			),
			lcMock:    draws([]string{domain.Medium}, addTwoNumbers(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(addTwoNumbers(), dailyDate), nil),
		},
		{
			// Another send committed Climbing Stairs between our Get and our Update.
			name: "lost race: the pick committed first is sent and ours is dropped",
			storeMock: picks(
				easy,
				withPick(easy, randomPick(climbingStairs(), dailyDate)),
				withPick(easy, randomPick(climbingStairs(), dailyDate)),
				false, nil,
			),
			lcMock:    draws([]string{domain.Easy}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(randomPick(climbingStairs(), dailyDate), nil),
		},
		{
			name: "chat deleted before the pick is saved still gets it",
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				gomock.InOrder(
					m.EXPECT().Get(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(easy, true, nil),
					m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(false, nil),
				)
				return m
			},
			lcMock:    draws([]string{domain.Easy}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(easyPick, nil),
		},
		{
			name:      "pick save error still sends it",
			storeMock: picks(easy, easy, withPick(easy, easyPick), true, errDisk),
			lcMock:    draws([]string{domain.Easy}, twoSum(), nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(easyPick, nil),
		},
		{
			// No Remove or Delete expected: gomock fails the test if they are called.
			name:      "other send error keeps the subscription",
			storeMock: gets(domain.Chat{}, false, nil),
			lcMock:    fetches(nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(officialPick(domain.Hard), errors.New("Too Many Requests: retry after 5")),
		},
		{
			// The bare store proves the pick is never read or written.
			name:      "daily: official daily is sent without touching the store",
			daily:     true,
			storeMock: mocks.NewMockchatStore,
			lcMock:    fetches(nil),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   sends(officialPick(domain.Hard), nil),
		},
		{
			name:      "daily: fetch failure sends fetch-failed, whose own error is only logged",
			daily:     true,
			storeMock: mocks.NewMockchatStore,
			lcMock:    fetches(errors.New("timeout")),
			schedMock: mocks.NewMockdailyScheduler,
			outMock:   fetchFailed(errors.New("Bad Gateway")),
		},
		{
			name:  "daily: blocked chat is unsubscribed",
			daily: true,
			storeMock: func(ctrl *gomock.Controller) *mocks.MockchatStore {
				m := mocks.NewMockchatStore(ctrl)
				m.EXPECT().Delete(gomock.Eq(ctx), gomock.Eq(int64(100))).Return(nil)
				return m
			},
			lcMock: fetches(nil),
			schedMock: func(ctrl *gomock.Controller) *mocks.MockdailyScheduler {
				m := mocks.NewMockdailyScheduler(ctrl)
				m.EXPECT().Remove(gomock.Eq(int64(100)))
				return m
			},
			outMock: sends(officialPick(domain.Hard), fmt.Errorf("x: %w", domain.ErrBlocked)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(ctx, tt.storeMock(ctrl), tt.lcMock(ctrl), tt.schedMock(ctrl), tt.outMock(ctrl), clock)

			if tt.daily {
				s.SendDaily(ctx, 100)
			} else {
				s.SendToday(ctx, 100)
			}
		})
	}
}

// rootKey marks the jobs root, so a port can tell a job's ctx derives from it.
type rootKey struct{}

func TestJob(t *testing.T) {
	jobs := context.WithValue(t.Context(), rootKey{}, "jobs")

	tests := []struct {
		name      string
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		lcMock    func(*gomock.Controller) *mocks.MockproblemSource
		outMock   func(*gomock.Controller) *mocks.Mockmessenger
	}{
		{
			// A cancelled root is integration row 19.
			name: "job sends today's problem under the jobs root with a jobTimeout deadline",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(jobs, tt.storeMock(ctrl), tt.lcMock(ctrl), mocks.NewMockdailyScheduler(ctrl), tt.outMock(ctrl), clock)

			s.job(100)()
		})
	}
}
