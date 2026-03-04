package leetcode

type Problem struct {
	Date       string
	Link       string
	ID         string
	Title      string
	Difficulty string
	Tags       []string
}

type Client interface {
	FetchDaily() (*Problem, error)
}
