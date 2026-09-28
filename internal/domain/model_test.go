package domain

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestDifficulties(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]string)
	}{
		{name: "canonical order", mutate: func([]string) {}},
		{name: "fresh slice on every call", mutate: func(s []string) { s[0] = "Mutated" }},
	}

	want := []string{"Easy", "Medium", "Hard"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mutate(Difficulties())
			if got := Difficulties(); !slices.Equal(got, want) {
				t.Errorf("Difficulties() = %v, want %v", got, want)
			}
		})
	}
}

// TestChatJSON pins the config.json contract: raw is a chat as today's
// storage.ChatConfig writes it, and wantJSON is how Chat writes it back.
func TestChatJSON(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		want     Chat
		wantJSON string
	}{
		{
			name: "flat legacy daily_pick decodes into Pick",
			raw: `{"chat_id":-100123,"notify_time":"09:00","timezone":"Europe/Moscow",` +
				`"members":{"7":{"name":"Alice","count":2,"last_solved_date":"2026-09-27"}},` +
				`"difficulties":["Easy"],` +
				`"daily_pick":{"date":"2026-09-28","daily_difficulty":"Hard","id":"1","title":"Two Sum",` +
				`"link":"/problems/two-sum/","difficulty":"Easy","tags":["Array","Hash Table"]}}`,
			want: Chat{
				ChatID:       -100123,
				NotifyTime:   "09:00",
				Timezone:     "Europe/Moscow",
				Members:      map[string]Member{"7": {Name: "Alice", Count: 2, LastSolvedDate: "2026-09-27"}},
				Difficulties: []string{"Easy"},
				DailyPick: &Pick{
					Problem: Problem{
						Date:       "2026-09-28",
						ID:         "1",
						Title:      "Two Sum",
						Link:       "/problems/two-sum/",
						Difficulty: "Easy",
						Tags:       []string{"Array", "Hash Table"},
					},
					DailyDifficulty: "Hard",
				},
			},
			wantJSON: `{"chat_id":-100123,"notify_time":"09:00","timezone":"Europe/Moscow",` +
				`"members":{"7":{"name":"Alice","count":2,"last_solved_date":"2026-09-27"}},` +
				`"difficulties":["Easy"],` +
				`"daily_pick":{"date":"2026-09-28","id":"1","title":"Two Sum","link":"/problems/two-sum/",` +
				`"difficulty":"Easy","tags":["Array","Hash Table"],"daily_difficulty":"Hard"}}`,
		},
		{
			name:     "null members and missing difficulties decode to nil",
			raw:      `{"chat_id":5,"notify_time":"21:15","timezone":"UTC","members":null}`,
			want:     Chat{ChatID: 5, NotifyTime: "21:15", Timezone: "UTC"},
			wantJSON: `{"chat_id":5,"notify_time":"21:15","timezone":"UTC","members":null}`,
		},
		{
			name:     "missing members field decodes to nil and is written as null",
			raw:      `{"chat_id":123,"notify_time":"07:00","timezone":"Asia/Tbilisi"}`,
			want:     Chat{ChatID: 123, NotifyTime: "07:00", Timezone: "Asia/Tbilisi"},
			wantJSON: `{"chat_id":123,"notify_time":"07:00","timezone":"Asia/Tbilisi","members":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Chat
			if err := json.Unmarshal([]byte(tt.raw), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decoded:\n got %#v\nwant %#v", got, tt.want)
			}

			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.wantJSON {
				t.Errorf("encoded:\n got %s\nwant %s", data, tt.wantJSON)
			}

			var back Chat
			if err := json.Unmarshal(data, &back); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(back, tt.want) {
				t.Errorf("round trip:\n got %#v\nwant %#v", back, tt.want)
			}
		})
	}
}
