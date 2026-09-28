//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// lcProblem is one catalogue entry of the fake LeetCode.
type lcProblem struct {
	id, title, slug, difficulty string
	tags                        []string
}

// catalogue holds exactly one free algorithms problem per level, keyed by the
// list query's upper-case difficulty filter, so a single-level random draw is
// deterministic under real math/rand.
var catalogue = map[string]lcProblem{
	"EASY":   {id: "1", title: "Two Sum", slug: "two-sum", difficulty: "Easy", tags: []string{"Array", "Hash Table"}},
	"MEDIUM": {id: "2", title: "Add Two Numbers", slug: "add-two-numbers", difficulty: "Medium", tags: []string{"Linked List", "Math", "Recursion"}},
	"HARD":   {id: "4", title: "Median of Two Sorted Arrays", slug: "median-of-two-sorted-arrays", difficulty: "Hard", tags: []string{"Array", "Binary Search", "Divide and Conquer"}},
}

// fakeLeetCode serves the two GraphQL queries the client sends: the active
// daily and the algorithms question list.
type fakeLeetCode struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	date       string
	daily      lcProblem
	dailyFails bool
	listFails  bool
	listDelay  time.Duration
	rotating   bool
	nDaily     int
	nList      int
	nRotated   int
}

func newFakeLeetCode(t *testing.T) *fakeLeetCode {
	f := &fakeLeetCode{t: t, date: "2026-09-28", daily: catalogue["HARD"]}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

type lcRequest struct {
	Query     string `json:"query"`
	Variables struct {
		CategorySlug string `json:"categorySlug"`
		Limit        int    `json:"limit"`
		Skip         int    `json:"skip"`
		Filters      struct {
			Difficulty string `json:"difficulty"`
		} `json:"filters"`
	} `json:"variables"`
}

func (f *fakeLeetCode) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
		f.t.Errorf("fake leetcode: unexpected %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	if got := r.Header.Get("Content-Type"); got != "application/json" {
		f.t.Errorf("fake leetcode: Content-Type %q, want application/json", got)
	}
	if got := r.Header.Get("User-Agent"); got != "Mozilla/5.0" {
		f.t.Errorf("fake leetcode: User-Agent %q, want Mozilla/5.0", got)
	}
	var req lcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("fake leetcode: decode body: %v", err)
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	switch {
	case strings.Contains(req.Query, "activeDailyCodingChallengeQuestion"):
		f.serveDaily(w)
	case strings.Contains(req.Query, "questionList("):
		f.serveList(w, r, req)
	default:
		f.t.Errorf("fake leetcode: unknown query %q", req.Query)
		http.Error(w, "unknown query", http.StatusBadRequest)
	}
}

func (f *fakeLeetCode) serveDaily(w http.ResponseWriter) {
	f.mu.Lock()
	f.nDaily++
	fail, date, p := f.dailyFails, f.date, f.daily
	f.mu.Unlock()
	if fail {
		http.Error(w, "outage", http.StatusInternalServerError)
		return
	}
	writeGraphQL(w, map[string]any{
		"activeDailyCodingChallengeQuestion": map[string]any{
			"date": date,
			"link": "/problems/" + p.slug + "/",
			"question": map[string]any{
				"title":              p.title,
				"frontendQuestionId": p.id,
				"difficulty":         p.difficulty,
				"topicTags":          topicTags(p.tags),
			},
		},
	})
}

func (f *fakeLeetCode) serveList(w http.ResponseWriter, r *http.Request, req lcRequest) {
	v := req.Variables
	if v.CategorySlug != "algorithms" {
		f.t.Errorf("fake leetcode: categorySlug %q, want algorithms", v.CategorySlug)
	}
	f.mu.Lock()
	f.nList++
	fail, delay, rotating := f.listFails, f.listDelay, f.rotating
	p, known := catalogue[v.Filters.Difficulty]
	if rotating {
		f.nRotated++
		n := f.nRotated
		p = lcProblem{
			id:         strconv.Itoa(1000 + n),
			title:      fmt.Sprintf("%s %d", p.title, n),
			slug:       fmt.Sprintf("%s-%d", p.slug, n),
			difficulty: p.difficulty,
			tags:       p.tags,
		}
	}
	f.mu.Unlock()

	select {
	case <-time.After(delay):
	case <-r.Context().Done():
		return
	}
	if fail {
		http.Error(w, "outage", http.StatusInternalServerError)
		return
	}
	if !known {
		f.t.Errorf("fake leetcode: unknown difficulty filter %q", v.Filters.Difficulty)
		http.Error(w, "unknown difficulty", http.StatusBadRequest)
		return
	}
	questions := []map[string]any{}
	if rotating || (v.Skip == 0 && v.Limit > 0) {
		questions = append(questions, map[string]any{
			"frontendQuestionId": p.id,
			"title":              p.title,
			"titleSlug":          p.slug,
			"difficulty":         p.difficulty,
			"paidOnly":           false,
			"topicTags":          topicTags(p.tags),
		})
	}
	writeGraphQL(w, map[string]any{
		"problemsetQuestionList": map[string]any{"total": 1, "questions": questions},
	})
}

func topicTags(names []string) []map[string]string {
	tags := make([]map[string]string, 0, len(names))
	for _, n := range names {
		tags = append(tags, map[string]string{"name": n})
	}
	return tags
}

func writeGraphQL(w http.ResponseWriter, data map[string]any) {
	raw, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// setDaily makes the catalogue problem of difficulty the daily of date.
func (f *fakeLeetCode) setDaily(date, difficulty string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.date, f.daily = date, catalogue[strings.ToUpper(difficulty)]
}

// failDaily makes daily queries answer 500.
func (f *fakeLeetCode) failDaily(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dailyFails = fail
}

// failList makes list queries answer 500.
func (f *fakeLeetCode) failList(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listFails = fail
}

// setListDelay delays every list response by d.
func (f *fakeLeetCode) setListDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listDelay = d
}

// setRotating makes every list response return a new free problem of the
// requested level, whatever skip is.
func (f *fakeLeetCode) setRotating(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rotating = on
}

func (f *fakeLeetCode) dailyCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nDaily
}

func (f *fakeLeetCode) listCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nList
}
