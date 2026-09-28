package leetcode

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/solympe/leetcode-tg-notifier/internal/leetcode/mocks"
)

type listVars struct {
	CategorySlug string `json:"categorySlug"`
	Limit        int    `json:"limit"`
	Skip         int    `json:"skip"`
	Filters      struct {
		Difficulty string `json:"difficulty"`
	} `json:"filters"`
}

// listCall is one expected question-list request and its canned response.
type listCall struct {
	limit  int
	skip   int
	status int // 0 means http.StatusOK
	body   string
}

func assertHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Errorf("method: got %s, want %s", r.Method, http.MethodPost)
	}
	if got := r.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", got, "application/json")
	}
	if got := r.Header.Get("User-Agent"); got != "Mozilla/5.0" {
		t.Errorf("User-Agent: got %q, want %q", got, "Mozilla/5.0")
	}
}

// newListServer serves the scripted question-list calls in order and asserts
// each request's variables and that exactly len(calls) requests were made.
func newListServer(t *testing.T, wantFilter string, calls []listCall) *httptest.Server {
	t.Helper()
	var (
		mu sync.Mutex
		n  int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		idx := n
		n++
		mu.Unlock()
		if idx >= len(calls) {
			t.Errorf("unexpected request #%d", idx+1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		call := calls[idx]

		assertHeaders(t, r)
		var req struct {
			Query     string   `json:"query"`
			Variables listVars `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("request #%d: decode body: %v", idx+1, err)
		}
		if !strings.Contains(req.Query, "questionList(") {
			t.Errorf("request #%d: query is not a question list query: %q", idx+1, req.Query)
		}
		want := listVars{CategorySlug: "algorithms", Limit: call.limit, Skip: call.skip}
		want.Filters.Difficulty = wantFilter
		if req.Variables != want {
			t.Errorf("request #%d: variables got %+v, want %+v", idx+1, req.Variables, want)
		}

		status := call.status
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, call.body)
	}))
	t.Cleanup(func() {
		srv.Close()
		mu.Lock()
		defer mu.Unlock()
		if n != len(calls) {
			t.Errorf("server got %d requests, want %d", n, len(calls))
		}
	})
	return srv
}

// question renders one list entry in the shape LeetCode returns.
func question(id, title, slug, difficulty string, paid bool, tags ...string) string {
	tagJSON := make([]string, 0, len(tags))
	for _, tag := range tags {
		tagJSON = append(tagJSON, fmt.Sprintf(`{"name":%q}`, tag))
	}
	return fmt.Sprintf(
		`{"frontendQuestionId":%q,"title":%q,"titleSlug":%q,"difficulty":%q,"paidOnly":%t,"topicTags":[%s]}`,
		id, title, slug, difficulty, paid, strings.Join(tagJSON, ","),
	)
}

func listJSON(total int, questions ...string) string {
	return fmt.Sprintf(
		`{"data":{"problemsetQuestionList":{"total":%d,"questions":[%s]}}}`,
		total, strings.Join(questions, ","),
	)
}

func TestFetchRandom(t *testing.T) {
	twoSum := question("1", "Two Sum", "two-sum", "Easy", false, "Array", "Hash Table")
	paidEasy := question("252", "Meeting Rooms", "meeting-rooms", "Easy", true, "Array")
	paidMedium := question("253", "Meeting Rooms II", "meeting-rooms-ii", "Medium", true, "Heap (Priority Queue)")
	addTwo := question("2", "Add Two Numbers", "add-two-numbers", "Medium", false, "Linked List", "Math")
	rainWater := question("42", "Trapping Rain Water", "trapping-rain-water", "Hard", false, "Array", "Two Pointers")
	regex := question("10", "Regular Expression Matching", "regular-expression-matching", "Hard", false, "String")

	twoSumProblem := &Problem{
		ID:         "1",
		Title:      "Two Sum",
		Link:       "/problems/two-sum/",
		Difficulty: "Easy",
		Tags:       []string{"Array", "Hash Table"},
	}
	addTwoProblem := &Problem{
		ID:         "2",
		Title:      "Add Two Numbers",
		Link:       "/problems/add-two-numbers/",
		Difficulty: "Medium",
		Tags:       []string{"Linked List", "Math"},
	}

	// failedDraws is the count request followed by maxRandomAttempts draws at
	// skip 10, 20, ... that all return body.
	failedDraws := func(body string) []listCall {
		calls := []listCall{{limit: 1, skip: 0, body: listJSON(806, twoSum)}}
		for i := range maxRandomAttempts {
			calls = append(calls, listCall{limit: 1, skip: 10 * (i + 1), body: body})
		}
		return calls
	}
	// failedDrawsRand picks Easy and then skip 10, 20, ... for failedDraws.
	failedDrawsRand := func(ctrl *gomock.Controller) *mocks.MockrandSource {
		m := mocks.NewMockrandSource(ctrl)
		calls := []any{m.EXPECT().IntN(gomock.Eq(1)).Return(0)}
		for i := range maxRandomAttempts {
			calls = append(calls, m.EXPECT().IntN(gomock.Eq(806)).Return(10*(i+1)))
		}
		gomock.InOrder(calls...)
		return m
	}

	tests := []struct {
		name         string
		difficulties []string
		randMock     func(*gomock.Controller) *mocks.MockrandSource
		wantFilter   string
		calls        []listCall
		want         *Problem
		wantErr      string
	}{
		{
			name:         "free problem on the first draw",
			difficulties: []string{DifficultyHard},
			randMock: func(ctrl *gomock.Controller) *mocks.MockrandSource {
				m := mocks.NewMockrandSource(ctrl)
				gomock.InOrder(
					m.EXPECT().IntN(gomock.Eq(1)).Return(0),
					m.EXPECT().IntN(gomock.Eq(907)).Return(100),
				)
				return m
			},
			wantFilter: "HARD",
			calls: []listCall{
				{limit: 1, skip: 0, body: listJSON(907, rainWater)},
				{limit: 1, skip: 100, body: listJSON(907, regex)},
			},
			want: &Problem{
				ID:         "10",
				Title:      "Regular Expression Matching",
				Link:       "/problems/regular-expression-matching/",
				Difficulty: "Hard",
				Tags:       []string{"String"},
			},
		},
		{
			name:         "paid draw is rejected and drawn again",
			difficulties: []string{DifficultyMedium},
			randMock: func(ctrl *gomock.Controller) *mocks.MockrandSource {
				m := mocks.NewMockrandSource(ctrl)
				gomock.InOrder(
					m.EXPECT().IntN(gomock.Eq(1)).Return(0),
					m.EXPECT().IntN(gomock.Eq(1937)).Return(500),
					m.EXPECT().IntN(gomock.Eq(1937)).Return(1),
				)
				return m
			},
			wantFilter: "MEDIUM",
			calls: []listCall{
				{limit: 1, skip: 0, body: listJSON(1937, addTwo)},
				{limit: 1, skip: 500, body: listJSON(1937, paidMedium)},
				{limit: 1, skip: 1, body: listJSON(1937, addTwo)},
			},
			want: addTwoProblem,
		},
		{
			// The list shrank between the count and the draw.
			name:         "empty draw is drawn again",
			difficulties: []string{DifficultyEasy},
			randMock: func(ctrl *gomock.Controller) *mocks.MockrandSource {
				m := mocks.NewMockrandSource(ctrl)
				gomock.InOrder(
					m.EXPECT().IntN(gomock.Eq(1)).Return(0),
					m.EXPECT().IntN(gomock.Eq(806)).Return(805),
					m.EXPECT().IntN(gomock.Eq(806)).Return(0),
				)
				return m
			},
			wantFilter: "EASY",
			calls: []listCall{
				{limit: 1, skip: 0, body: listJSON(806, twoSum)},
				{limit: 1, skip: 805, body: listJSON(805)},
				{limit: 1, skip: 0, body: listJSON(806, twoSum)},
			},
			want: twoSumProblem,
		},
		{
			name:         "subset picks the level chosen by the random source",
			difficulties: []string{DifficultyEasy, DifficultyHard},
			randMock: func(ctrl *gomock.Controller) *mocks.MockrandSource {
				m := mocks.NewMockrandSource(ctrl)
				gomock.InOrder(
					m.EXPECT().IntN(gomock.Eq(2)).Return(1),
					m.EXPECT().IntN(gomock.Eq(907)).Return(900),
				)
				return m
			},
			wantFilter: "HARD",
			calls: []listCall{
				{limit: 1, skip: 0, body: listJSON(907, regex)},
				{limit: 1, skip: 900, body: listJSON(907, rainWater)},
			},
			want: &Problem{
				ID:         "42",
				Title:      "Trapping Rain Water",
				Link:       "/problems/trapping-rain-water/",
				Difficulty: "Hard",
				Tags:       []string{"Array", "Two Pointers"},
			},
		},
		{
			name:         "empty difficulties chooses among all three",
			difficulties: nil,
			randMock: func(ctrl *gomock.Controller) *mocks.MockrandSource {
				m := mocks.NewMockrandSource(ctrl)
				gomock.InOrder(
					m.EXPECT().IntN(gomock.Eq(3)).Return(1),
					m.EXPECT().IntN(gomock.Eq(1937)).Return(0),
				)
				return m
			},
			wantFilter: "MEDIUM",
			calls: []listCall{
				{limit: 1, skip: 0, body: listJSON(1937, addTwo)},
				{limit: 1, skip: 0, body: listJSON(1937, addTwo)},
			},
			want: addTwoProblem,
		},
		{
			name:         "every draw is paid",
			difficulties: []string{DifficultyEasy},
			randMock:     failedDrawsRand,
			wantFilter:   "EASY",
			calls:        failedDraws(listJSON(806, paidEasy)),
			wantErr:      "no free Easy problem found in 8 attempts",
		},
		{
			name:         "every draw is empty",
			difficulties: []string{DifficultyEasy},
			randMock:     failedDrawsRand,
			wantFilter:   "EASY",
			calls:        failedDraws(listJSON(806)),
			wantErr:      "no free Easy problem found in 8 attempts",
		},
		{
			name:         "total zero",
			difficulties: []string{DifficultyEasy},
			randMock: func(ctrl *gomock.Controller) *mocks.MockrandSource {
				m := mocks.NewMockrandSource(ctrl)
				m.EXPECT().IntN(gomock.Eq(1)).Return(0)
				return m
			},
			wantFilter: "EASY",
			calls:      []listCall{{limit: 1, skip: 0, body: listJSON(0)}},
			wantErr:    "no Easy problems",
		},
		{
			name:         "non-200 status on count request",
			difficulties: []string{DifficultyEasy},
			randMock: func(ctrl *gomock.Controller) *mocks.MockrandSource {
				m := mocks.NewMockrandSource(ctrl)
				m.EXPECT().IntN(gomock.Eq(1)).Return(0)
				return m
			},
			wantFilter: "EASY",
			calls:      []listCall{{limit: 1, skip: 0, status: http.StatusServiceUnavailable, body: "busy"}},
			wantErr:    "count Easy problems: unexpected status: 503",
		},
		{
			name:         "non-200 status on a draw",
			difficulties: []string{DifficultyEasy},
			randMock: func(ctrl *gomock.Controller) *mocks.MockrandSource {
				m := mocks.NewMockrandSource(ctrl)
				gomock.InOrder(
					m.EXPECT().IntN(gomock.Eq(1)).Return(0),
					m.EXPECT().IntN(gomock.Eq(806)).Return(5),
				)
				return m
			},
			wantFilter: "EASY",
			calls: []listCall{
				{limit: 1, skip: 0, body: listJSON(806, twoSum)},
				{limit: 1, skip: 5, status: http.StatusTooManyRequests, body: "slow down"},
			},
			wantErr: "fetch Easy problem: unexpected status: 429",
		},
		{
			name:         "malformed JSON on count request",
			difficulties: []string{DifficultyEasy},
			randMock: func(ctrl *gomock.Controller) *mocks.MockrandSource {
				m := mocks.NewMockrandSource(ctrl)
				m.EXPECT().IntN(gomock.Eq(1)).Return(0)
				return m
			},
			wantFilter: "EASY",
			calls:      []listCall{{limit: 1, skip: 0, body: `{"data":`}},
			wantErr:    "count Easy problems: decode",
		},
		{
			name:         "malformed JSON on a draw",
			difficulties: []string{DifficultyEasy},
			randMock: func(ctrl *gomock.Controller) *mocks.MockrandSource {
				m := mocks.NewMockrandSource(ctrl)
				gomock.InOrder(
					m.EXPECT().IntN(gomock.Eq(1)).Return(0),
					m.EXPECT().IntN(gomock.Eq(806)).Return(5),
				)
				return m
			},
			wantFilter: "EASY",
			calls: []listCall{
				{limit: 1, skip: 0, body: listJSON(806, twoSum)},
				{limit: 1, skip: 5, body: `{"data":`},
			},
			wantErr: "fetch Easy problem: decode",
		},
		{
			name:         "invalid difficulty makes no HTTP call",
			difficulties: []string{DifficultyEasy, "Extreme"},
			randMock:     mocks.NewMockrandSource,
			wantErr:      `unknown difficulty "Extreme"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			srv := newListServer(t, tt.wantFilter, tt.calls)
			hc := NewHTTPClient(srv.URL, srv.Client())
			hc.rnd = tt.randMock(ctrl)

			got, err := hc.FetchRandom(tt.difficulties)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error: got %v, want containing %q", err, tt.wantErr)
				}
				if got != nil {
					t.Errorf("problem: got %+v, want nil", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("problem:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

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

func TestFetchDaily(t *testing.T) {
	const dailyJSON = `{"data":{"activeDailyCodingChallengeQuestion":{"date":"2026-09-27","link":"/problems/two-sum/",` +
		`"question":{"title":"Two Sum","frontendQuestionId":"1","difficulty":"Easy",` +
		`"topicTags":[{"name":"Array"},{"name":"Hash Table"}]}}}}`

	tests := []struct {
		name    string
		status  int
		body    string
		want    *Problem
		wantErr string
	}{
		{
			name:   "happy path",
			status: http.StatusOK,
			body:   dailyJSON,
			want: &Problem{
				Date:       "2026-09-27",
				Link:       "/problems/two-sum/",
				ID:         "1",
				Title:      "Two Sum",
				Difficulty: "Easy",
				Tags:       []string{"Array", "Hash Table"},
			},
		},
		{
			name:    "non-200 status",
			status:  http.StatusBadGateway,
			body:    dailyJSON,
			wantErr: "502",
		},
		{
			name:    "empty question",
			status:  http.StatusOK,
			body:    `{"data":{"activeDailyCodingChallengeQuestion":null}}`,
			wantErr: "empty response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				mu       sync.Mutex
				requests int
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests++
				mu.Unlock()

				assertHeaders(t, r)
				var req struct {
					Query string `json:"query"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode body: %v", err)
				}
				if !strings.Contains(req.Query, "activeDailyCodingChallengeQuestion") {
					t.Errorf("query is not the daily query: %q", req.Query)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(func() {
				srv.Close()
				mu.Lock()
				defer mu.Unlock()
				if requests != 1 {
					t.Errorf("server got %d requests, want 1", requests)
				}
			})

			hc := NewHTTPClient(srv.URL, srv.Client())

			got, err := hc.FetchDaily()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error: got %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("problem:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
