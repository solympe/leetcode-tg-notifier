package notifier

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/domain"
	"github.com/solympe/leetcode-tg-notifier/internal/notifier/mocks"
)

// applyUpsert is applyUpdate for chatStore.Upsert of stored (Chat{ChatID}
// for a new chat).
func applyUpsert(ctrl *gomock.Controller, stored, want domain.Chat, err error) func(context.Context, int64, func(*domain.Chat)) error {
	update := applyUpdate(ctrl, stored, want, true, err)
	return func(ctx context.Context, id int64, fn func(*domain.Chat)) error {
		_, err := update(ctx, id, func(c *domain.Chat) bool { fn(c); return true })
		return err
	}
}

func TestRestore(t *testing.T) {
	ctx := t.Context()

	all := func(ctrl *gomock.Controller) *mocks.MockchatStore {
		m := mocks.NewMockchatStore(ctrl)
		m.EXPECT().All(gomock.Eq(ctx)).Return([]domain.Chat{
			{ChatID: -100500, NotifyTime: "21:15", Timezone: "Asia/Tbilisi"},
			{ChatID: 100, NotifyTime: "09:00", Timezone: "UTC"},
		}, nil)
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
	failsAll := func(ctrl *gomock.Controller) *mocks.MockchatStore {
		m := mocks.NewMockchatStore(ctrl)
		m.EXPECT().All(gomock.Eq(ctx)).Return(nil, context.Canceled)
		return m
	}

	tests := []struct {
		name      string
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		schedMock func(*gomock.Controller) *mocks.MockdailyScheduler
		wantErr   string
	}{
		{name: "every chat is scheduled", storeMock: all, schedMock: schedules(nil, nil)},
		{
			// One failing chat among good ones is integration row 21.
			name:      "a failure does not stop the next chat, and every failure is joined",
			storeMock: all,
			schedMock: schedules(errors.New("bad group spec"), errors.New("bad private spec")),
			wantErr:   "restore schedule for -100500: bad group spec\nrestore schedule for 100: bad private spec",
		},
		{name: "All error is returned", storeMock: failsAll, schedMock: mocks.NewMockdailyScheduler, wantErr: "context canceled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(ctx, tt.storeMock(ctrl), mocks.NewMockproblemSource(ctrl), tt.schedMock(ctrl), mocks.NewMockmessenger(ctrl), clock)

			got := ""
			if err := s.Restore(ctx); err != nil {
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
	members := map[string]domain.Member{"7": {Name: "Alice", Count: 3, LastSolvedDate: "2026-09-27"}}
	pick := randomPick(twoSum(), dailyDate)
	errSched := errors.New(`invalid time "0900"`)

	// The job handed to Schedule is run by integration rows 8, 13 and 15.
	schedules := func(err error) func(*gomock.Controller) *mocks.MockdailyScheduler {
		return func(ctrl *gomock.Controller) *mocks.MockdailyScheduler {
			m := mocks.NewMockdailyScheduler(ctrl)
			m.EXPECT().Schedule(gomock.Eq(int64(100)), gomock.Eq("09:00"), gomock.Eq("UTC"), gomock.Any()).Return(err)
			return m
		}
	}
	upserts := func(stored, want domain.Chat, err error) func(*gomock.Controller) *mocks.MockchatStore {
		return func(ctrl *gomock.Controller) *mocks.MockchatStore {
			m := mocks.NewMockchatStore(ctrl)
			m.EXPECT().Upsert(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(applyUpsert(ctrl, stored, want, err))
			return m
		}
	}

	tests := []struct {
		name      string
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		schedMock func(*gomock.Controller) *mocks.MockdailyScheduler
		wantErrs  []error
	}{
		{
			name:      "new chat is saved and scheduled",
			storeMock: upserts(domain.Chat{ChatID: 100}, subscribed(domain.Easy), nil),
			schedMock: schedules(nil),
		},
		{
			name: "existing chat keeps its members and pick",
			storeMock: upserts(
				withPick(withMembers(domain.Chat{ChatID: 100, NotifyTime: "07:00", Timezone: "Asia/Tbilisi", Difficulties: domain.Difficulties()}, members), pick),
				withPick(withMembers(subscribed(domain.Easy), members), pick),
				nil,
			),
			schedMock: schedules(nil),
		},
		{
			name:      "a failed save still schedules, and both errors are joined",
			storeMock: upserts(domain.Chat{ChatID: 100}, subscribed(domain.Easy), errDisk),
			schedMock: schedules(errSched),
			wantErrs:  []error{errDisk, errSched},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			s := New(ctx, tt.storeMock(ctrl), mocks.NewMockproblemSource(ctrl), tt.schedMock(ctrl), mocks.NewMockmessenger(ctrl), clock)

			err := s.Subscribe(ctx, 100, "09:00", "UTC", []string{domain.Easy})
			if len(tt.wantErrs) == 0 && err != nil {
				t.Errorf("Subscribe() error = %v, want nil", err)
			}
			for _, want := range tt.wantErrs {
				if !errors.Is(err, want) {
					t.Errorf("Subscribe() error = %v, want it to wrap %v", err, want)
				}
			}
		})
	}
}

func TestSetDifficulties(t *testing.T) {
	ctx := t.Context()
	pick := randomPick(twoSum(), dailyDate)

	updates := func(found bool, err error) func(*gomock.Controller) *mocks.MockchatStore {
		return func(ctrl *gomock.Controller) *mocks.MockchatStore {
			m := mocks.NewMockchatStore(ctrl)
			m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(found, err)
			return m
		}
	}

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
					true, nil,
				))
				return m
			},
		},
		{name: "missing chat is ErrNotSubscribed", storeMock: updates(false, nil), wantErr: domain.ErrNotSubscribed},
		{name: "store error is returned as is", storeMock: updates(true, errDisk), wantErr: errDisk},
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

	tests := []struct {
		name      string
		schedMock func(*gomock.Controller) *mocks.MockdailyScheduler
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		wantErr   error
	}{
		{
			// The success path is integration row 12.
			name: "entry is removed, then the chat deleted, and its error returned",
			schedMock: func(ctrl *gomock.Controller) *mocks.MockdailyScheduler {
				m := mocks.NewMockdailyScheduler(ctrl)
				removed = m.EXPECT().Remove(gomock.Eq(int64(100)))
				return m
			},
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
			s := New(ctx, tt.storeMock(ctrl), mocks.NewMockproblemSource(ctrl), sched, mocks.NewMockmessenger(ctrl), clock)

			if err := s.Unsubscribe(ctx, 100); !errors.Is(err, tt.wantErr) {
				t.Errorf("Unsubscribe() error = %v, want %v", err, tt.wantErr)
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
	// solves applies fn to stored, which it must turn into want, and returns err.
	solves := func(stored, want domain.Chat, wantWrite bool, err error) func(*gomock.Controller) *mocks.MockchatStore {
		return func(ctrl *gomock.Controller) *mocks.MockchatStore {
			m := mocks.NewMockchatStore(ctrl)
			m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).DoAndReturn(applyUpdate(ctrl, stored, want, wantWrite, err))
			return m
		}
	}
	refuses := func(found bool, err error) func(*gomock.Controller) *mocks.MockchatStore {
		return func(ctrl *gomock.Controller) *mocks.MockchatStore {
			m := mocks.NewMockchatStore(ctrl)
			m.EXPECT().Update(gomock.Eq(ctx), gomock.Eq(int64(100)), gomock.Any()).Return(found, err)
			return m
		}
	}
	solved := withMembers(subscribed(), alice(1, dailyDate))

	tests := []struct {
		name      string
		storeMock func(*gomock.Controller) *mocks.MockchatStore
		wantTotal int
		wantErr   error
	}{
		{
			// Other RecordSolve rules are domain tests.
			name:      "first solve is counted on the UTC day",
			storeMock: solves(subscribed(), solved, true, nil),
			wantTotal: 1,
		},
		{
			name:      "second solve the same day is refused with the total",
			storeMock: solves(withMembers(subscribed(), alice(3, dailyDate)), withMembers(subscribed(), alice(3, dailyDate)), false, nil),
			wantTotal: 3,
			wantErr:   domain.ErrAlreadySolved,
		},
		{
			name:      "write error returns the applied total",
			storeMock: solves(subscribed(), solved, true, errDisk),
			wantTotal: 1,
			wantErr:   errDisk,
		},
		{name: "missing chat is ErrNotSubscribed", storeMock: refuses(false, nil), wantErr: domain.ErrNotSubscribed},
		{name: "refused call returns 0", storeMock: refuses(false, context.Canceled), wantErr: context.Canceled},
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
