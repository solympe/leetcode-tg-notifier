package leetcode

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

// listVars mirrors the wire format independently of the client's query.
type listVars struct {
	CategorySlug string `json:"categorySlug"`
	Limit        int    `json:"limit"`
	Skip         int    `json:"skip"`
	Filters      struct {
		Difficulty string `json:"difficulty"`
	} `json:"filters"`
}

// call is one expected request and its canned response.
type call struct {
	skip   int
	status int // 0 means http.StatusOK
	body   string
}

// newServer serves calls in order. It asserts the headers, that the query
// contains wantQuery, the list variables when wantFilter is set, and that
// exactly len(calls) requests were made.
func newServer(t *testing.T, wantQuery, wantFilter string, calls []call) *httptest.Server {
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
		c := calls[idx]

		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("User-Agent") != "Mozilla/5.0" {
			t.Errorf("request #%d: %s with headers %v", idx+1, r.Method, r.Header)
		}
		var req struct {
			Query     string   `json:"query"`
			Variables listVars `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("request #%d: decode body: %v", idx+1, err)
		}
		if !strings.Contains(req.Query, wantQuery) {
			t.Errorf("request #%d: query %q does not contain %q", idx+1, req.Query, wantQuery)
		}
		var want listVars
		if wantFilter != "" {
			want = listVars{CategorySlug: "algorithms", Limit: 1, Skip: c.skip}
			want.Filters.Difficulty = wantFilter
		}
		if req.Variables != want {
			t.Errorf("request #%d: variables got %+v, want %+v", idx+1, req.Variables, want)
		}

		w.WriteHeader(max(c.status, http.StatusOK))
		_, _ = io.WriteString(w, c.body)
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

// intNs expects IntN calls in order, given as (n, result) pairs.
func intNs(pairs ...int) func(*gomock.Controller) *mocks.MockrandSource {
	return func(ctrl *gomock.Controller) *mocks.MockrandSource {
		m := mocks.NewMockrandSource(ctrl)
		var calls []any
		for i := 0; i < len(pairs); i += 2 {
			calls = append(calls, m.EXPECT().IntN(gomock.Eq(pairs[i])).Return(pairs[i+1]))
		}
		gomock.InOrder(calls...)
		return m
	}
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

// checkResult fails unless err contains wantErr (no error when wantErr is
// empty) and got equals want (the zero value on error).
func checkResult(t *testing.T, got domain.Problem, err error, want domain.Problem, wantErr string) {
	t.Helper()
	if (err == nil) != (wantErr == "") || err != nil && !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("error: got %v, want containing %q", err, wantErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problem:\n got %+v\nwant %+v", got, want)
	}
}

func TestFetchRandom(t *testing.T) {
	twoSum := question("1", "Two Sum", "two-sum", "Easy", false, "Array", "Hash Table")
	paidEasy := question("252", "Meeting Rooms", "meeting-rooms", "Easy", true, "Array")
	paidMedium := question("253", "Meeting Rooms II", "meeting-rooms-ii", "Medium", true, "Heap (Priority Queue)")
	addTwo := question("2", "Add Two Numbers", "add-two-numbers", "Medium", false, "Linked List", "Math")
	rainWater := question("42", "Trapping Rain Water", "trapping-rain-water", "Hard", false, "Array", "Two Pointers")

	addTwoProblem := domain.Problem{
		ID:         "2",
		Title:      "Add Two Numbers",
		Link:       "/problems/add-two-numbers/",
		Difficulty: "Medium",
		Tags:       []string{"Linked List", "Math"},
	}
	// Easy is counted, then every one of the maxRandomAttempts draws is paid.
	allPaidRand := []int{1, 0}
	allPaidCalls := []call{{skip: 0, body: listJSON(806, twoSum)}}
	for i := range maxRandomAttempts {
		allPaidRand = append(allPaidRand, 806, 10*(i+1))
		allPaidCalls = append(allPaidCalls, call{skip: 10 * (i + 1), body: listJSON(806, paidEasy)})
	}

	tests := []struct {
		name         string
		difficulties []string
		randMock     func(*gomock.Controller) *mocks.MockrandSource
		wantFilter   string
		calls        []call
		want         domain.Problem
		wantErr      string
	}{
		{
			name:         "subset picks the level chosen by the random source",
			difficulties: []string{domain.Easy, domain.Hard},
			randMock:     intNs(2, 1, 907, 900),
			wantFilter:   "HARD",
			calls:        []call{{skip: 0, body: listJSON(907, rainWater)}, {skip: 900, body: listJSON(907, rainWater)}},
			want: domain.Problem{
				ID:         "42",
				Title:      "Trapping Rain Water",
				Link:       "/problems/trapping-rain-water/",
				Difficulty: "Hard",
				Tags:       []string{"Array", "Two Pointers"},
			},
		},
		{
			name:       "empty difficulties chooses among all three",
			randMock:   intNs(3, 1, 1937, 0),
			wantFilter: "MEDIUM",
			calls:      []call{{skip: 0, body: listJSON(1937, addTwo)}, {skip: 0, body: listJSON(1937, addTwo)}},
			want:       addTwoProblem,
		},
		{
			name:         "paid draw is rejected and drawn again",
			difficulties: []string{domain.Medium},
			randMock:     intNs(1, 0, 1937, 500, 1937, 1),
			wantFilter:   "MEDIUM",
			calls: []call{
				{skip: 0, body: listJSON(1937, addTwo)},
				{skip: 500, body: listJSON(1937, paidMedium)},
				{skip: 1, body: listJSON(1937, addTwo)},
			},
			want: addTwoProblem,
		},
		{
			// The list shrank between the count and the draw.
			name:         "empty draw is drawn again",
			difficulties: []string{domain.Medium},
			randMock:     intNs(1, 0, 1937, 1936, 1937, 0),
			wantFilter:   "MEDIUM",
			calls: []call{
				{skip: 0, body: listJSON(1937, addTwo)},
				{skip: 1936, body: listJSON(1936)},
				{skip: 0, body: listJSON(1937, addTwo)},
			},
			want: addTwoProblem,
		},
		{
			name:         "every draw is paid",
			difficulties: []string{domain.Easy},
			randMock:     intNs(allPaidRand...),
			wantFilter:   "EASY",
			calls:        allPaidCalls,
			wantErr:      "no free Easy problem found in 8 attempts",
		},
		{
			name:         "total zero",
			difficulties: []string{domain.Easy},
			randMock:     intNs(1, 0),
			wantFilter:   "EASY",
			calls:        []call{{skip: 0, body: listJSON(0)}},
			wantErr:      "no Easy problems",
		},
		{
			name:         "non-200 status on the count request",
			difficulties: []string{domain.Easy},
			randMock:     intNs(1, 0),
			wantFilter:   "EASY",
			calls:        []call{{skip: 0, status: http.StatusServiceUnavailable, body: "busy"}},
			wantErr:      "count Easy problems: unexpected status: 503",
		},
		{
			name:         "malformed JSON on a draw",
			difficulties: []string{domain.Easy},
			randMock:     intNs(1, 0, 806, 5),
			wantFilter:   "EASY",
			calls:        []call{{skip: 0, body: listJSON(806, twoSum)}, {skip: 5, body: `{"data":`}},
			wantErr:      "fetch Easy problem: decode",
		},
		{
			name:         "invalid difficulty makes no HTTP call",
			difficulties: []string{domain.Easy, "Extreme"},
			randMock:     mocks.NewMockrandSource,
			wantErr:      `unknown difficulty "Extreme"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t, "questionList(", tt.wantFilter, tt.calls)
			hc := NewHTTPClient(srv.URL, srv.Client())
			hc.rnd = tt.randMock(gomock.NewController(t))

			got, err := hc.FetchRandom(t.Context(), tt.difficulties)
			checkResult(t, got, err, tt.want, tt.wantErr)
		})
	}
}

func TestFetchDaily(t *testing.T) {
	const dailyJSON = `{"data":{"activeDailyCodingChallengeQuestion":{"date":"2026-09-27","link":"/problems/two-sum/",` +
		`"question":{"title":"Two Sum","frontendQuestionId":"1","difficulty":"Easy",` +
		`"topicTags":[{"name":"Array"},{"name":"Hash Table"}]}}}}`

	tests := []struct {
		name    string
		resp    call
		want    domain.Problem
		wantErr string
	}{
		{
			name: "happy path",
			resp: call{body: dailyJSON},
			want: domain.Problem{
				Date:       "2026-09-27",
				Link:       "/problems/two-sum/",
				ID:         "1",
				Title:      "Two Sum",
				Difficulty: "Easy",
				Tags:       []string{"Array", "Hash Table"},
			},
		},
		{name: "non-200 status", resp: call{status: http.StatusBadGateway, body: dailyJSON}, wantErr: "unexpected status: 502"},
		{name: "empty question", resp: call{body: `{"data":{"activeDailyCodingChallengeQuestion":null}}`}, wantErr: "empty response"},
		{name: "malformed JSON", resp: call{body: `{"data":`}, wantErr: "decode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t, "activeDailyCodingChallengeQuestion", "", []call{tt.resp})
			got, err := NewHTTPClient(srv.URL, srv.Client()).FetchDaily(t.Context())
			checkResult(t, got, err, tt.want, tt.wantErr)
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
			if hc.http.Timeout != tt.wantTimeout {
				t.Errorf("timeout: got %v, want %v", hc.http.Timeout, tt.wantTimeout)
			}
			if hc.endpoint != tt.wantEndpoint {
				t.Errorf("endpoint: got %q, want %q", hc.endpoint, tt.wantEndpoint)
			}
		})
	}
}

func TestContext(t *testing.T) {
	tests := []struct {
		name     string
		inFlight bool // cancel once the server holds the request instead of before sending
		randMock func(*gomock.Controller) *mocks.MockrandSource
		call     func(ctx context.Context, hc *httpClient) (domain.Problem, error)
		wantHits int32
	}{
		{
			name:     "FetchDaily with a cancelled ctx sends nothing",
			randMock: mocks.NewMockrandSource,
			call:     func(ctx context.Context, hc *httpClient) (domain.Problem, error) { return hc.FetchDaily(ctx) },
			wantHits: 0,
		},
		{
			name:     "FetchRandom gives up on a request in flight",
			inFlight: true,
			randMock: intNs(1, 0),
			call: func(ctx context.Context, hc *httpClient) (domain.Problem, error) {
				return hc.FetchRandom(ctx, []string{domain.Easy})
			},
			wantHits: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if !tt.inFlight {
				cancel()
			}
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client hanging up
				cancel()
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second): // a client that ignores ctx fails instead of hanging
				}
			}))
			t.Cleanup(srv.Close)
			hc := NewHTTPClient(srv.URL, srv.Client())
			hc.rnd = tt.randMock(gomock.NewController(t))

			got, err := tt.call(ctx, hc)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("error: got %v, want %v", err, context.Canceled)
			}
			if got.Title != "" {
				t.Errorf("problem: got %+v, want the zero value", got)
			}
			if n := hits.Load(); n != tt.wantHits {
				t.Errorf("server hits: got %d, want %d", n, tt.wantHits)
			}
		})
	}
}
