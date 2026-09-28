package domain

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Wants reports whether the chat takes a daily of difficulty; empty means any.
func (c Chat) Wants(difficulty string) bool {
	return len(c.Difficulties) == 0 ||
		slices.ContainsFunc(c.Difficulties, func(d string) bool { return strings.EqualFold(d, difficulty) })
}

// PickFor returns the stored pick of the day when it can be resent in place
// of daily: it replaces that daily's date and its difficulty is still
// subscribed, which also rejects a pick made for a selection changed since.
func (c Chat) PickFor(daily Problem) (Pick, bool) {
	if c.DailyPick == nil || c.DailyPick.Date != daily.Date || !c.Wants(c.DailyPick.Difficulty) {
		return Pick{}, false
	}
	return *c.DailyPick, true
}

// RecordSolve counts userID's solve on day ("2006-01-02", UTC) and returns the
// member's total. A second solve on the same day is not counted and does not
// refresh the stored name.
func (c *Chat) RecordSolve(userID int64, name, day string) (total int, counted bool) {
	key := strconv.FormatInt(userID, 10)
	m := c.Members[key]
	if m.LastSolvedDate == day {
		return m.Count, false
	}
	if c.Members == nil {
		c.Members = make(map[string]Member)
	}
	m.Name = name
	m.Count++
	m.LastSolvedDate = day
	c.Members[key] = m
	return m.Count, true
}

// Standings returns the members by solve count, highest first, ties by name.
func (c Chat) Standings() []Member {
	ms := slices.Collect(maps.Values(c.Members))
	slices.SortFunc(ms, func(a, b Member) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.Name, b.Name))
	})
	return ms
}
