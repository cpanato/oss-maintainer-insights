// Package analysis calculates versioned metrics without network or storage access.
package analysis

type Actor struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}
type Repository struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
}
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}
type Event struct {
	ID         string `json:"id"`
	Actor      *Actor `json:"actor"`
	OccurredAt string `json:"occurred_at"`
}
type Review struct {
	Event
	State string `json:"state"`
}
type Streams struct {
	Reviews        bool `json:"reviews"`
	IssueComments  bool `json:"issue_comments"`
	ReviewComments bool `json:"review_comments"`
}
type PullRequest struct {
	ID             int64    `json:"id"`
	Author         *Actor   `json:"author"`
	CreatedAt      string   `json:"created_at"`
	Draft          bool     `json:"draft"`
	Outcome        string   `json:"outcome"`
	Coverage       Streams  `json:"coverage"`
	Reviews        []Review `json:"reviews"`
	IssueComments  []Event  `json:"issue_comments"`
	ReviewComments []Event  `json:"review_comments"`
}
type Enumeration struct {
	CreationCohort bool `json:"creation_cohort"`
	ReviewActivity bool `json:"review_activity"`
}
type Input struct {
	SchemaVersion int           `json:"schema_version"`
	Repository    Repository    `json:"repository"`
	Mode          string        `json:"mode"`
	ObservedAt    string        `json:"observed_at"`
	Window        Window        `json:"window"`
	Enumeration   Enumeration   `json:"enumeration"`
	BotIDs        []int64       `json:"bot_ids"`
	PullRequests  []PullRequest `json:"pull_requests"`
}
type Metric struct {
	Value  *float64 `json:"value"`
	Status string   `json:"status"`
}
type Exclusions struct {
	DraftPRs           int `json:"draft_prs"`
	BotAuthors         int `json:"bot_authors"`
	MissingAuthors     int `json:"missing_authors"`
	BotEvents          int `json:"bot_events"`
	MissingEventActors int `json:"missing_event_actors"`
	SelfEvents         int `json:"self_events"`
}
type Outcomes struct {
	Open           int `json:"open"`
	ClosedUnmerged int `json:"closed_unmerged"`
	Merged         int `json:"merged"`
}
type CreationCohort struct {
	EligiblePRs int      `json:"eligible_prs"`
	Coverage    string   `json:"coverage"`
	Authors     int      `json:"authors"`
	Outcomes    Outcomes `json:"outcomes"`
}
type Reviewer struct {
	Actor       Actor `json:"actor"`
	ReviewUnits int   `json:"review_units"`
}
type ReviewActivity struct {
	EligiblePRs          int        `json:"eligible_prs"`
	CoveredPRs           int        `json:"covered_prs"`
	Coverage             string     `json:"coverage"`
	ReviewUnits          int        `json:"review_units"`
	Participants         int        `json:"participants"`
	Distribution         []Reviewer `json:"distribution"`
	Concentration80      Metric     `json:"concentration_80"`
	LargestReviewerShare Metric     `json:"largest_reviewer_share"`
}
type FirstResponse struct {
	EligiblePRs   int    `json:"eligible_prs"`
	CoveredPRs    int    `json:"covered_prs"`
	Coverage      string `json:"coverage"`
	RespondedPRs  int    `json:"responded_prs"`
	UnansweredPRs int    `json:"unanswered_prs"`
	MedianMS      Metric `json:"median_ms"`
}
type Result struct {
	SchemaVersion      int            `json:"schema_version"`
	MethodologyVersion string         `json:"methodology_version"`
	Repository         Repository     `json:"repository"`
	Mode               string         `json:"mode"`
	CollectedAt        string         `json:"collected_at"`
	Window             Window         `json:"window"`
	Exclusions         Exclusions     `json:"exclusions"`
	CreationCohort     CreationCohort `json:"creation_cohort"`
	ReviewActivity     ReviewActivity `json:"review_activity"`
	FirstResponse      FirstResponse  `json:"first_response"`
}
