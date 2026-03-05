package leetcode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const graphqlURL = "https://leetcode.com/graphql"

var dailyQuery = `{"query":"query { activeDailyCodingChallengeQuestion { date link question { title frontendQuestionId: questionFrontendId difficulty topicTags { name } } } }"}`

type lcResponse struct {
	Data struct {
		ActiveDailyCodingChallengeQuestion struct {
			Date     string `json:"date"`
			Link     string `json:"link"`
			Question struct {
				Title              string `json:"title"`
				FrontendQuestionID string `json:"frontendQuestionId"`
				Difficulty         string `json:"difficulty"`
				TopicTags          []struct {
					Name string `json:"name"`
				} `json:"topicTags"`
			} `json:"question"`
		} `json:"activeDailyCodingChallengeQuestion"`
	} `json:"data"`
}

type httpClient struct {
	http *http.Client
}

func NewHTTPClient(c *http.Client) Client {
	if c == nil {
		c = http.DefaultClient
	}
	return &httpClient{http: c}
}

func (hc *httpClient) FetchDaily() (*Problem, error) {
	req, err := http.NewRequest("POST", graphqlURL, strings.NewReader(dailyQuery))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := hc.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("close response body: %v", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var lcResp lcResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&lcResp); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	q := lcResp.Data.ActiveDailyCodingChallengeQuestion
	if q.Question.Title == "" {
		return nil, fmt.Errorf("empty response from LeetCode")
	}

	tags := make([]string, 0, len(q.Question.TopicTags))
	for _, t := range q.Question.TopicTags {
		tags = append(tags, t.Name)
	}

	return &Problem{
		Date:       q.Date,
		Link:       q.Link,
		ID:         q.Question.FrontendQuestionID,
		Title:      q.Question.Title,
		Difficulty: q.Question.Difficulty,
		Tags:       tags,
	}, nil
}

func FormatProblem(p *Problem) string {
	dateStr := p.Date
	if t, err := time.Parse("2006-01-02", p.Date); err == nil {
		dateStr = t.Format("January 2, 2006")
	}
	tagsLine := strings.Join(p.Tags, ", ")
	return fmt.Sprintf(
		"📅 LeetCode Daily — %s\n\n🔢 %s. %s\n💪 Difficulty: %s\n🏷 %s\n\n🔗 https://leetcode.com%s",
		dateStr, p.ID, p.Title, p.Difficulty, tagsLine, p.Link,
	)
}
