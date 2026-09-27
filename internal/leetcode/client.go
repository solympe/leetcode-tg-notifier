package leetcode

const (
	DifficultyEasy   = "Easy"
	DifficultyMedium = "Medium"
	DifficultyHard   = "Hard"
)

// AllDifficulties returns every difficulty in canonical order. The slice is
// freshly allocated on each call, so callers may modify it.
func AllDifficulties() []string {
	return []string{DifficultyEasy, DifficultyMedium, DifficultyHard}
}

type Problem struct {
	Date       string
	Link       string
	ID         string
	Title      string
	Difficulty string
	Tags       []string
}
