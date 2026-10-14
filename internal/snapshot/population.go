package snapshot

import (
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/github"
)

// Population assigns each inspected PR to one category. Human and bot authors
// are both eligible; automation events remain excluded by the metric engine.
type Population struct {
	Inspected        int `json:"inspected_prs"`
	HumanAuthored    int `json:"human_authored_prs"`
	BotAuthored      int `json:"bot_authored_prs"`
	Draft            int `json:"draft_prs"`
	MissingAuthor    int `json:"missing_author_prs"`
	OutsideWindow    int `json:"outside_window_prs"`
	AfterObservation int `json:"after_observation_prs"`
}

func (p *Population) record(pr rawPR, start, observed time.Time, botIDs []int64) (bool, error) {
	if pr.Draft == nil {
		return false, &github.Failure{Reason: "invalid_response"}
	}
	created, err := timestamp(pr.Created)
	if err != nil {
		return false, err
	}
	p.Inspected++
	if !created.Before(observed) {
		p.AfterObservation++
		return false, nil
	}
	if !start.IsZero() && created.Before(start) {
		p.OutsideWindow++
		return false, nil
	}
	user, err := actor(pr.User)
	if err != nil {
		return false, err
	}
	if *pr.Draft {
		p.Draft++
		return false, nil
	}
	if user == nil {
		p.MissingAuthor++
		return false, nil
	}
	bot := user.Type == "Bot"
	for _, id := range botIDs {
		bot = bot || user.ID == id
	}
	if bot {
		p.BotAuthored++
	} else {
		p.HumanAuthored++
	}
	return true, nil
}
