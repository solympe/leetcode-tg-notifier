package leetcode

import (
	"slices"
	"testing"
)

func TestAllDifficulties(t *testing.T) {
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
			tt.mutate(AllDifficulties())
			if got := AllDifficulties(); !slices.Equal(got, want) {
				t.Errorf("AllDifficulties() = %v, want %v", got, want)
			}
		})
	}
}
