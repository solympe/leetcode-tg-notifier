package leetcode

//go:generate mockgen -source=http.go -destination=mocks/mock_deps.go -package=mocks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	graphqlURL         = "https://leetcode.com/graphql"
	defaultHTTPTimeout = 10 * time.Second

	randomCategory = "algorithms"
	// Up to ~17% of a level is paid (Medium), so 8 paid draws in a row happen
	// about once in a million picks.
	maxRandomAttempts = 8
)

const dailyQuery = `{"query":"query { activeDailyCodingChallengeQuestion { date link question { title frontendQuestionId: questionFrontendId difficulty topicTags { name } } } }"}`

const questionListQuery = `query q($categorySlug: String, $limit: Int, $skip: Int, $filters: QuestionListFilterInput) {` +
	` problemsetQuestionList: questionList(categorySlug: $categorySlug, limit: $limit, skip: $skip, filters: $filters) {` +
	` total: totalNum questions: data { frontendQuestionId: questionFrontendId title titleSlug difficulty paidOnly: isPaidOnly topicTags { name } } } }`

type topicTag struct {
	Name string `json:"name"`
}

type lcResponse struct {
	Data struct {
		ActiveDailyCodingChallengeQuestion struct {
			Date     string `json:"date"`
			Link     string `json:"link"`
			Question struct {
				Title              string     `json:"title"`
				FrontendQuestionID string     `json:"frontendQuestionId"`
				Difficulty         string     `json:"difficulty"`
				TopicTags          []topicTag `json:"topicTags"`
			} `json:"question"`
		} `json:"activeDailyCodingChallengeQuestion"`
	} `json:"data"`
}

type listRequest struct {
	Query     string        `json:"query"`
	Variables listVariables `json:"variables"`
}

type listVariables struct {
	CategorySlug string      `json:"categorySlug"`
	Limit        int         `json:"limit"`
	Skip         int         `json:"skip"`
	Filters      listFilters `json:"filters"`
}

type listFilters struct {
	Difficulty string `json:"difficulty"` // upper-case: EASY, MEDIUM, HARD
}

type listQuestion struct {
	FrontendQuestionID string     `json:"frontendQuestionId"`
	Title              string     `json:"title"`
	TitleSlug          string     `json:"titleSlug"`
	Difficulty         string     `json:"difficulty"`
	PaidOnly           bool       `json:"paidOnly"`
	TopicTags          []topicTag `json:"topicTags"`
}

type questionList struct {
	Total     int            `json:"total"`
	Questions []listQuestion `json:"questions"`
}

type listResponse struct {
	Data struct {
		ProblemsetQuestionList questionList `json:"problemsetQuestionList"`
	} `json:"data"`
}

// randSource picks random indexes for FetchRandom.
type randSource interface {
	IntN(n int) int
}

// globalRand is the randSource backed by math/rand/v2.
type globalRand struct{}

func (globalRand) IntN(n int) int { return rand.IntN(n) }

type httpClient struct {
	http     *http.Client
	endpoint string
	rnd      randSource
}

// NewHTTPClient returns a LeetCode client that sends requests with c, or with
// a client limited to defaultHTTPTimeout per request when c is nil.
func NewHTTPClient(c *http.Client) *httpClient {
	if c == nil {
		c = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &httpClient{http: c, endpoint: graphqlURL, rnd: globalRand{}}
}

// query POSTs a GraphQL payload and decodes a 200 OK JSON response into out.
func (hc *httpClient) query(payload []byte, out any) error {
	req, err := http.NewRequest(http.MethodPost, hc.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := hc.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("close response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

func (hc *httpClient) FetchDaily() (*Problem, error) {
	var lcResp lcResponse
	if err := hc.query([]byte(dailyQuery), &lcResp); err != nil {
		return nil, err
	}

	q := lcResp.Data.ActiveDailyCodingChallengeQuestion
	if q.Question.Title == "" {
		return nil, fmt.Errorf("empty response from LeetCode")
	}

	return &Problem{
		Date:       q.Date,
		Link:       q.Link,
		ID:         q.Question.FrontendQuestionID,
		Title:      q.Question.Title,
		Difficulty: q.Question.Difficulty,
		Tags:       tagNames(q.Question.TopicTags),
	}, nil
}

// FetchRandom returns a random free algorithms problem of one of the given
// difficulties (any difficulty when empty). The returned Problem has no Date.
func (hc *httpClient) FetchRandom(difficulties []string) (*Problem, error) {
	if len(difficulties) == 0 {
		difficulties = AllDifficulties()
	}
	for _, d := range difficulties {
		if !slices.Contains(AllDifficulties(), d) {
			return nil, fmt.Errorf("unknown difficulty %q", d)
		}
	}
	d := difficulties[hc.rnd.IntN(len(difficulties))]

	first, err := hc.fetchQuestionList(d, 1, 0)
	if err != nil {
		return nil, fmt.Errorf("count %s problems: %w", d, err)
	}
	if first.Total <= 0 {
		return nil, fmt.Errorf("no %s problems on LeetCode", d)
	}

	// Each attempt draws one problem uniformly and rejects it when it is paid
	// (LeetCode ignores the premiumOnly filter) or missing, so every free
	// problem is equally likely.
	for range maxRandomAttempts {
		draw, err := hc.fetchQuestionList(d, 1, hc.rnd.IntN(first.Total))
		if err != nil {
			return nil, fmt.Errorf("fetch %s problem: %w", d, err)
		}
		if len(draw.Questions) == 0 || draw.Questions[0].PaidOnly {
			continue
		}
		q := draw.Questions[0]
		return &Problem{
			Link:       "/problems/" + q.TitleSlug + "/",
			ID:         q.FrontendQuestionID,
			Title:      q.Title,
			Difficulty: q.Difficulty,
			Tags:       tagNames(q.TopicTags),
		}, nil
	}
	return nil, fmt.Errorf("no free %s problem found in %d attempts", d, maxRandomAttempts)
}

func (hc *httpClient) fetchQuestionList(difficulty string, limit, skip int) (questionList, error) {
	payload, err := json.Marshal(listRequest{
		Query: questionListQuery,
		Variables: listVariables{
			CategorySlug: randomCategory,
			Limit:        limit,
			Skip:         skip,
			Filters:      listFilters{Difficulty: strings.ToUpper(difficulty)},
		},
	})
	if err != nil {
		return questionList{}, fmt.Errorf("marshal: %w", err)
	}
	var resp listResponse
	if err := hc.query(payload, &resp); err != nil {
		return questionList{}, err
	}
	return resp.Data.ProblemsetQuestionList, nil
}

func tagNames(tags []topicTag) []string {
	names := make([]string, 0, len(tags))
	for _, t := range tags {
		names = append(names, t.Name)
	}
	return names
}

func FormatProblem(p *Problem) string {
	return fmt.Sprintf("📅 LeetCode Daily — %s\n\n%s", formatDate(p.Date), formatBody(p))
}

// FormatRandomProblem formats a random pick that replaces a daily of
// dailyDifficulty; p.Date is the date of the replaced daily.
func FormatRandomProblem(p *Problem, dailyDifficulty string) string {
	return fmt.Sprintf(
		"🎲 LeetCode Random — %s\nToday's daily is %s, so here's a random %s problem for you.\n\n%s",
		formatDate(p.Date), dailyDifficulty, p.Difficulty, formatBody(p),
	)
}

// formatDate renders a "2006-01-02" date as "January 2, 2006", falling back
// to the raw string when it does not parse.
func formatDate(date string) string {
	if t, err := time.Parse("2006-01-02", date); err == nil {
		return t.Format("January 2, 2006")
	}
	return date
}

func formatBody(p *Problem) string {
	return fmt.Sprintf(
		"🔢 %s. %s\n💪 Difficulty: %s\n🏷 %s\n\n🔗 https://leetcode.com%s",
		p.ID, p.Title, p.Difficulty, strings.Join(p.Tags, ", "), p.Link,
	)
}
