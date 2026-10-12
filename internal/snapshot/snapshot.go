// Package snapshot generates bounded, public aggregate samples and internal conditional checkpoints.
package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/analysis"
	"github.com/cpanato/oss-maintainer-insights/internal/github"
	"github.com/cpanato/oss-maintainer-insights/schemas"
)

const QueryPolicy = "newest-eight-human-prs-90-days-v1"

type Entry struct {
	Repository string  `json:"repository"`
	Group      string  `json:"group,omitempty"`
	BotIDs     []int64 `json:"bot_ids,omitempty"`
}
type Selection struct {
	Listed    int  `json:"listed"`
	Selected  int  `json:"selected"`
	Eligible  int  `json:"eligible_on_page"`
	Truncated bool `json:"truncated"`
	More      bool `json:"list_has_more"`
}
type Rate struct {
	Remaining *int   `json:"remaining"`
	Limit     *int   `json:"limit"`
	ResetAt   *int64 `json:"resetAt"`
}
type Failure struct {
	PR     int    `json:"pr"`
	Stream string `json:"stream"`
	Reason string `json:"reason"`
}

// SampleAge describes selected creation times without publishing individual PRs.
type SampleAge struct {
	Oldest string `json:"oldest_created_at"`
	Newest string `json:"newest_created_at"`
}

// Public contains aggregates only. Source events and validators never enter website artifacts.
type Public struct {
	SampleAge *SampleAge      `json:"sample_age,omitempty"`
	Policy    string          `json:"policy"`
	Result    analysis.Result `json:"result"`
	Selection Selection       `json:"selection"`
	Requests  int             `json:"requests"`
	Rate      Rate            `json:"rate"`
	Failures  []Failure       `json:"failures"`
	Complete  bool            `json:"complete"`
}
type Page struct {
	ETag  string          `json:"etag"`
	Next  string          `json:"next"`
	Value json.RawMessage `json:"value"`
}
type State struct {
	RepositoryID       int64           `json:"repository_id,omitempty"`
	ObservedAt         string          `json:"observed_at,omitempty"`
	Pending            bool            `json:"pending,omitempty"`
	MethodologyVersion string          `json:"methodology_version,omitempty"`
	BotIDs             []int64         `json:"bot_ids,omitempty"`
	Version            int             `json:"version"`
	Policy             string          `json:"policy"`
	Repository         string          `json:"repository"`
	Pages              map[string]Page `json:"pages"`
}
type Getter interface {
	GetConditional(context.Context, string, any, string) (http.Header, error)
	RequestsUsed() int
	RateLimit() github.RateLimit
}
type reader struct {
	resume     bool
	checkpoint func(State) error
	client     Getter
	previous   State
	next       State
}

func (r *reader) get(ctx context.Context, path string, destination any) (string, error) {
	cached := r.previous.Pages[path]
	if r.resume && !strings.HasPrefix(path, "/repos/") && len(cached.Value) > 0 {
		if err := json.Unmarshal(cached.Value, destination); err != nil {
			return "", &github.Failure{Reason: "invalid_response"}
		}
		r.next.Pages[path] = cached
		return cached.Next, nil
	}
	headers, err := r.client.GetConditional(ctx, path, destination, cached.ETag)
	var next string
	if errors.Is(err, github.ErrNotModified) {
		if len(cached.Value) == 0 {
			return "", &github.Failure{Reason: "invalid_response"}
		}
		if err := json.Unmarshal(cached.Value, destination); err != nil {
			return "", &github.Failure{Reason: "invalid_response"}
		}
		next = cached.Next
		if headers.Get("Link") != "" {
			next, err = github.NextPage(headers.Get("Link"))
			if err != nil {
				return "", err
			}
		}
		data, marshalErr := json.Marshal(destination)
		if marshalErr != nil {
			return "", marshalErr
		}
		r.next.Pages[path] = Page{ETag: cached.ETag, Next: next, Value: data}
		if r.checkpoint != nil {
			if err := r.checkpoint(r.next); err != nil {
				return "", err
			}
		}
		return next, nil
	}
	if err != nil {
		return "", err
	}
	next, err = github.NextPage(headers.Get("Link"))
	if err != nil {
		return "", err
	}
	// Marshaling the typed destination discards all unrecognized API fields.
	data, err := json.Marshal(destination)
	if err != nil {
		return "", err
	}
	r.next.Pages[path] = Page{ETag: headers.Get("ETag"), Next: next, Value: data}
	if r.checkpoint != nil {
		if err := r.checkpoint(r.next); err != nil {
			return "", err
		}
	}
	return next, nil
}

type rawActor struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

func actor(raw *rawActor) (*analysis.Actor, error) {
	if raw == nil || raw.Login == "ghost" {
		return nil, nil
	}
	if raw.ID <= 0 || raw.ID > 9007199254740991 || strings.TrimSpace(raw.Login) == "" || (raw.Type != "User" && raw.Type != "Bot") {
		return nil, &github.Failure{Reason: "invalid_response"}
	}
	return &analysis.Actor{ID: raw.ID, Login: raw.Login, Type: raw.Type}, nil
}
func timestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !strings.HasSuffix(value, "Z") || (parsed.Format(time.RFC3339) != value && parsed.Format("2006-01-02T15:04:05.000Z") != value) {
		return time.Time{}, &github.Failure{Reason: "invalid_response"}
	}
	return parsed, nil
}

type rawPR struct {
	ID      int64     `json:"id"`
	Number  int       `json:"number"`
	User    *rawActor `json:"user"`
	Created string    `json:"created_at"`
	Draft   *bool     `json:"draft"`
	State   string    `json:"state"`
	Merged  *string   `json:"merged_at"`
}
type rawEvent struct {
	Commands  []string  `json:"review_commands,omitempty"`
	ID        int64     `json:"id"`
	User      *rawActor `json:"user"`
	Created   string    `json:"created_at,omitempty"`
	Submitted *string   `json:"submitted_at,omitempty"`
	State     string    `json:"state,omitempty"`
}

// Parse body text before checkpoint serialization; only bounded signals survive.
func (raw *rawEvent) UnmarshalJSON(data []byte) error {
	type fields rawEvent
	var value struct {
		fields
		Body *string `json:"body"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*raw = rawEvent(value.fields)
	if value.Body != nil {
		raw.Commands = analysis.ParseReviewCommands(*value.Body)
	}
	return nil
}

func normalize(raw rawEvent, review bool) (analysis.Review, error) {
	if review && raw.State == "PENDING" {
		return analysis.Review{State: "PENDING"}, nil
	}
	user, err := actor(raw.User)
	if err != nil {
		return analysis.Review{}, err
	}
	date := raw.Created
	if review {
		if raw.Submitted == nil {
			return analysis.Review{}, &github.Failure{Reason: "invalid_response"}
		}
		date = *raw.Submitted
		if raw.State != "APPROVED" && raw.State != "CHANGES_REQUESTED" && raw.State != "COMMENTED" && raw.State != "DISMISSED" {
			return analysis.Review{}, &github.Failure{Reason: "invalid_response"}
		}
	}
	if _, err := timestamp(date); err != nil {
		return analysis.Review{}, err
	}
	if raw.ID <= 0 || raw.ID > 9007199254740991 {
		return analysis.Review{}, &github.Failure{Reason: "invalid_response"}
	}
	return analysis.Review{Event: analysis.Event{ReviewCommands: raw.Commands, ID: strconv.FormatInt(raw.ID, 10), Actor: user, OccurredAt: date}, State: raw.State}, nil
}
func (r *reader) events(ctx context.Context, path string, review bool) ([]analysis.Review, bool, error) {
	values := []analysis.Review{}
	positions := map[string]int{}
	visited := map[string]bool{}
	for path != "" {
		if visited[path] {
			return values, false, &github.Failure{Reason: "invalid_response"}
		}
		visited[path] = true
		var records []rawEvent
		next, err := r.get(ctx, path, &records)
		if err != nil {
			return values, false, err
		}
		if records == nil {
			delete(r.next.Pages, path)
			return values, false, &github.Failure{Reason: "invalid_response"}
		}
		// A malformed page contributes no records, matching browser pagination.
		page := []analysis.Review{}
		for _, raw := range records {
			value, err := normalize(raw, review)
			if err != nil {
				delete(r.next.Pages, path)
				return values, false, err
			}
			if value.State != "PENDING" {
				page = append(page, value)
			}
		}
		for _, value := range page {
			if position, ok := positions[value.ID]; ok {
				values[position] = value
			} else {
				positions[value.ID] = len(values)
				values = append(values, value)
			}
		}
		path = next
	}
	return values, true, nil
}
func reason(err error) string {
	var failure *github.Failure
	if errors.As(err, &failure) {
		return failure.Reason
	}
	return "invalid_response"
}

// Collect revalidates every selected stream, including older PRs. Unchanged pages use sanitized checkpoints.
func Collect(ctx context.Context, client Getter, entry Entry, observed time.Time, previous State) (Public, State, error) {
	return collect(ctx, client, entry, observed, previous, false, false, nil)
}

// CollectCatalog samples three disjoint creation-age bands without selecting for activity.
func CollectCatalog(ctx context.Context, client Getter, entry Entry, observed time.Time, previous State) (Public, State, error) {
	return collect(ctx, client, entry, observed, previous, true, false, nil)
}

// CollectCatalogWithCheckpoint resumes a pending bounded sample at its frozen
// observation time and reports sanitized page progress to the caller.
func CollectCatalogWithCheckpoint(ctx context.Context, client Getter, entry Entry, observed time.Time, previous State, checkpoint func(State) error) (Public, State, error) {
	return collect(ctx, client, entry, observed, previous, true, true, checkpoint)
}

const CatalogPolicy = "three-age-bands-three-hundred-human-prs-90-days-v1"

func collect(ctx context.Context, client Getter, entry Entry, observed time.Time, previous State, catalog, resume bool, checkpoint func(State) error) (Public, State, error) {
	observed = observed.UTC().Truncate(time.Millisecond)
	policy := QueryPolicy
	if catalog {
		policy = CatalogPolicy
	}
	state := State{Version: 2, Policy: policy, Repository: entry.Repository, Pages: map[string]Page{}}
	if catalog {
		if observed.IsZero() {
			observed = time.Now().UTC()
		}
		observed = observed.Truncate(time.Second)
	}
	if previous.Version != 2 || previous.Policy != policy || !strings.EqualFold(previous.Repository, entry.Repository) {
		previous = State{}
	}
	pendingAt, pendingErr := timestamp(previous.ObservedAt)
	resume = resume && previous.Pending && pendingErr == nil && previous.MethodologyVersion == analysis.MethodologyVersion && slices.Equal(previous.BotIDs, entry.BotIDs) && !observed.Before(pendingAt) && observed.Sub(pendingAt) <= 7*24*time.Hour
	if resume {
		state.RepositoryID = previous.RepositoryID
		observed = pendingAt
		for path, page := range previous.Pages {
			state.Pages[path] = page
		}
	}
	if catalog {
		state.ObservedAt = observed.Format(time.RFC3339)
		state.Pending = true
		state.MethodologyVersion = analysis.MethodologyVersion
		state.BotIDs = append([]int64(nil), entry.BotIDs...)
	}
	r := reader{client: client, previous: previous, next: state, resume: resume, checkpoint: checkpoint}
	path, err := github.RepositoryPath(entry.Repository)
	if err != nil {
		return Public{}, r.next, err
	}
	var metadata struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Private  *bool  `json:"private"`
	}
	if _, err = r.get(ctx, path, &metadata); err != nil {
		return Public{}, r.next, err
	}
	if metadata.ID <= 0 || metadata.ID > 9007199254740991 || metadata.Private == nil || *metadata.Private {
		return Public{}, r.next, &github.Failure{Reason: "invalid_response"}
	}
	if _, err = github.RepositoryPath(metadata.FullName); err != nil {
		return Public{}, r.next, &github.Failure{Reason: "invalid_response"}
	}
	if r.resume && r.next.RepositoryID != metadata.ID {
		return Public{}, r.next, &github.Failure{Reason: "invalid_response"}
	}
	r.next.RepositoryID = metadata.ID
	path = "/repositories/" + strconv.FormatInt(metadata.ID, 10)
	var eligible []rawPR
	var selection Selection
	if catalog {
		if observed.IsZero() {
			observed = time.Now().UTC()
		}
		observed = observed.Truncate(time.Second)
		eligible, selection, err = r.catalogPRs(ctx, path, metadata.FullName, entry.BotIDs, observed)
		if err != nil {
			return Public{}, r.next, err
		}
	} else {
		var records []rawPR
		next, err := r.get(ctx, path+"/pulls?state=all&sort=created&direction=desc&per_page=30", &records)
		if err != nil {
			return Public{}, r.next, err
		}
		if records == nil || len(records) > 30 {
			return Public{}, r.next, &github.Failure{Reason: "invalid_response"}
		}
		bots := map[int64]bool{}
		for _, id := range entry.BotIDs {
			bots[id] = true
		}
		if observed.IsZero() {
			observed = time.Now().UTC().Truncate(time.Millisecond)
		}
		start := observed.Add(-90 * 24 * time.Hour)
		eligible = []rawPR{}
		seen := map[int64]bool{}
		for _, pr := range records {
			if pr.ID <= 0 || pr.ID > 9007199254740991 || pr.Number <= 0 || pr.Draft == nil || seen[pr.ID] || (pr.State != "open" && pr.State != "closed") {
				return Public{}, r.next, &github.Failure{Reason: "invalid_response"}
			}
			seen[pr.ID] = true
			created, err := timestamp(pr.Created)
			if err != nil {
				return Public{}, r.next, err
			}
			user, err := actor(pr.User)
			if err != nil {
				return Public{}, r.next, err
			}
			if *pr.Draft || user == nil || user.Type != "User" || bots[user.ID] || created.Before(start) || !created.Before(observed) {
				continue
			}
			eligible = append(eligible, pr)
		}
		sort.Slice(eligible, func(i, j int) bool {
			if eligible[i].Created == eligible[j].Created {
				return eligible[i].Number > eligible[j].Number
			}
			a, _ := timestamp(eligible[i].Created)
			b, _ := timestamp(eligible[j].Created)
			return a.After(b)
		})
		selection = Selection{Listed: len(records), Eligible: len(eligible), Truncated: len(eligible) > 8, More: next != ""}
		if len(eligible) > 8 {
			eligible = eligible[:8]
		}
		selection.Selected = len(eligible)
	}
	start := observed.Add(-90 * 24 * time.Hour)
	input := analysis.Input{SchemaVersion: 1, Repository: analysis.Repository{ID: metadata.ID, FullName: metadata.FullName}, Mode: "live_sample", ObservedAt: observed.Format("2006-01-02T15:04:05.000Z"), Window: analysis.Window{Start: start.Format("2006-01-02T15:04:05.000Z"), End: observed.Format("2006-01-02T15:04:05.000Z")}, Enumeration: analysis.Enumeration{CreationCohort: true, ReviewActivity: true}, BotIDs: append([]int64{}, entry.BotIDs...), PullRequests: []analysis.PullRequest{}}
	for _, raw := range eligible {
		user, _ := actor(raw.User)
		outcome := "open"
		if raw.Merged != nil {
			outcome = "merged"
		} else if raw.State == "closed" {
			outcome = "closed_unmerged"
		}
		input.PullRequests = append(input.PullRequests, analysis.PullRequest{ID: raw.ID, Author: user, CreatedAt: raw.Created, Outcome: outcome, Reviews: []analysis.Review{}, IssueComments: []analysis.Event{}, ReviewComments: []analysis.Event{}})
	}
	failures := []Failure{}
	for index, raw := range eligible {
		events, complete, err := r.events(ctx, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", path, raw.Number), true)
		input.PullRequests[index].Reviews = events
		input.PullRequests[index].Coverage.Reviews = complete
		if err != nil {
			failures = append(failures, Failure{PR: raw.Number, Stream: "reviews", Reason: reason(err)})
		}
	}
	for index, raw := range eligible {
		pr := &input.PullRequests[index]
		if !pr.Coverage.Reviews {
			continue
		}
		for _, stream := range []string{"issue_comments", "review_comments"} {
			endpoint := fmt.Sprintf("%s/pulls/%d/comments?per_page=100", path, raw.Number)
			if stream == "issue_comments" {
				endpoint = fmt.Sprintf("%s/issues/%d/comments?per_page=100", path, raw.Number)
			}
			events, complete, err := r.events(ctx, endpoint, false)
			values := []analysis.Event{}
			for _, event := range events {
				values = append(values, event.Event)
			}
			if stream == "issue_comments" {
				pr.IssueComments = values
				pr.Coverage.IssueComments = complete
			} else {
				pr.ReviewComments = values
				pr.Coverage.ReviewComments = complete
			}
			if err != nil {
				failures = append(failures, Failure{PR: raw.Number, Stream: stream, Reason: reason(err)})
			}
		}
	}
	if err := schemas.Validate("analysis-input", input); err != nil {
		return Public{}, r.next, err
	}
	result, err := analysis.Analyze(input)
	if err != nil {
		return Public{}, r.next, err
	}
	complete := ctx.Err() == nil
	for _, pr := range input.PullRequests {
		complete = complete && pr.Coverage.Reviews && pr.Coverage.IssueComments && pr.Coverage.ReviewComments
	}
	rate := client.RateLimit()
	var reset *int64
	if rate.ResetAt != nil {
		value := rate.ResetAt.UnixMilli()
		reset = &value
	}
	public := Public{Policy: policy, Result: result, Selection: selection, Requests: client.RequestsUsed(), Rate: Rate{Remaining: rate.Remaining, Limit: rate.Limit, ResetAt: reset}, Failures: failures, Complete: complete}
	if catalog && len(eligible) > 0 {
		age := &SampleAge{Oldest: eligible[0].Created, Newest: eligible[0].Created}
		for _, pr := range eligible {
			at, _ := timestamp(pr.Created)
			oldest, _ := timestamp(age.Oldest)
			newest, _ := timestamp(age.Newest)
			if at.Before(oldest) {
				age.Oldest = pr.Created
			}
			if at.After(newest) {
				age.Newest = pr.Created
			}
		}
		public.SampleAge = age
	}
	if err := schemas.Validate("repository-snapshot", public); err != nil {
		return Public{}, r.next, err
	}
	r.next.Pending = !complete
	return public, r.next, nil
}
