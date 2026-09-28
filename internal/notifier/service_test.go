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
