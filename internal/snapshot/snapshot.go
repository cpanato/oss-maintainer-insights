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

const QueryPolicy = "newest-eight-non-draft-prs-90-days-v2"

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
	Population *Population     `json:"population,omitempty"`
	Collection *Collection     `json:"collection,omitempty"`
	SampleAge  *SampleAge      `json:"sample_age,omitempty"`
	Policy     string          `json:"policy"`
	Result     analysis.Result `json:"result"`
	Selection  Selection       `json:"selection"`
	Requests   int             `json:"requests"`
	Rate       Rate            `json:"rate"`
	Failures   []Failure       `json:"failures"`
	Complete   bool            `json:"complete"`
}
type Page struct {
	ETag  string          `json:"etag"`
	Next  string          `json:"next"`
	Value json.RawMessage `json:"value"`
}
type Progress struct {
	Population          *Population     `json:"population,omitempty"`
	Window              analysis.Window `json:"window"`
	EnumeratedPRs       int             `json:"enumerated_prs"`
	EligiblePRs         int             `json:"eligible_prs"`
	CoveredPRs          int             `json:"covered_prs"`
	EnumerationComplete bool            `json:"enumeration_complete"`
}

type State struct {
	Tracked            map[int]TrackedPR `json:"tracked_prs,omitempty"`
	Incremental        bool              `json:"incremental,omitempty"`
	Progress           *Progress         `json:"progress,omitempty"`
	RepositoryID       int64             `json:"repository_id,omitempty"`
	ObservedAt         string            `json:"observed_at,omitempty"`
	Pending            bool              `json:"pending,omitempty"`
	MethodologyVersion string            `json:"methodology_version,omitempty"`
	BotIDs             []int64           `json:"bot_ids,omitempty"`
	Version            int               `json:"version"`
	Policy             string            `json:"policy"`
	Repository         string            `json:"repository"`
	Pages              map[string]Page   `json:"pages"`
}
type Getter interface {
	GetConditional(context.Context, string, any, string) (http.Header, error)
	RequestsUsed() int
	RateLimit() github.RateLimit
}
type reader struct {
	population  *Population
	replay      map[string]bool
	incremental bool
	resume      bool
	checkpoint  func(State) error
	client      Getter
	previous    State
	next        State
}

func (r *reader) get(ctx context.Context, path string, destination any) (string, error) {
	cached := r.previous.Pages[path]
	if (r.resume || r.replay[path]) && !strings.HasPrefix(path, "/repos/") && len(cached.Value) > 0 {
		if err := json.Unmarshal(cached.Value, destination); err != nil {
			return "", &github.Failure{Reason: "invalid_response"}
		}
		value, err := json.Marshal(destination)
		if err != nil {
			return "", err
		}
		r.next.Pages[path] = Page{ETag: cached.ETag, Next: cached.Next, Value: value}
		return cached.Next, nil
	}
	etag := cached.ETag
	// Older checkpoints stripped updated_at from typed PR pages. A one-time
	// unconditional fetch restores the metadata needed for safe reuse decisions.
	if _, prs := destination.(*[]rawPR); prs && r.incremental && !r.resume && !cachedPRUpdates(cached) {
		etag = ""
	}
	headers, err := r.client.GetConditional(ctx, path, destination, etag)
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
	Updated string    `json:"updated_at,omitempty"`
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
func (r *reader) cachedStream(path string, review bool) bool {
	visited := map[string]bool{}
	for path != "" {
		if visited[path] {
			return false
		}
		visited[path] = true
		page, ok := r.previous.Pages[path]
		if !ok {
			return false
		}
		var records []rawEvent
		if json.Unmarshal(page.Value, &records) != nil || records == nil {
			return false
		}
		for _, record := range records {
			if _, err := normalize(record, review); err != nil {
				return false
			}
		}
		path = page.Next
	}
	return true
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
	return collect(ctx, client, entry, observed, previous, false, false, false, nil)
}

// CollectCatalog enumerates the complete eligible 90-day creation cohort.
func CollectCatalog(ctx context.Context, client Getter, entry Entry, observed time.Time, previous State) (Public, State, error) {
	return collect(ctx, client, entry, observed, previous, true, false, false, nil)
}

// CollectCatalogWithCheckpoint resumes a pending bounded sample at its frozen
// observation time and reports sanitized page progress to the caller.
func CollectCatalogWithCheckpoint(ctx context.Context, client Getter, entry Entry, observed time.Time, previous State, checkpoint func(State) error) (Public, State, error) {
	return collect(ctx, client, entry, observed, previous, true, true, false, checkpoint)
}

const CatalogPolicy = "all-non-draft-prs-created-in-90-days-v2"

func collect(ctx context.Context, client Getter, entry Entry, observed time.Time, previous State, catalog, resume, incremental bool, checkpoint func(State) error) (Public, State, error) {
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
	compatiblePolicy := previous.Policy == policy || (catalog && slices.Contains([]string{QueryPolicy, "newest-eight-human-prs-90-days-v1", "all-human-prs-created-in-90-days-v1", "three-age-bands-eighteen-human-prs-90-days-v1", "three-age-bands-three-hundred-human-prs-90-days-v1"}, previous.Policy))
	if (previous.Version != 2 && previous.Version != 3) || !compatiblePolicy || !strings.EqualFold(previous.Repository, entry.Repository) {
		previous = State{}
	}
	pendingAt, pendingErr := timestamp(previous.ObservedAt)
	resume = resume && previous.Pending && previous.Policy == policy && previous.RepositoryID > 0 && pendingErr == nil && previous.MethodologyVersion == analysis.MethodologyVersion && slices.Equal(previous.BotIDs, entry.BotIDs) && !observed.Before(pendingAt)
	if resume {
		state.RepositoryID = previous.RepositoryID
		observed = pendingAt
		for path, page := range previous.Pages {
			state.Pages[path] = page
		}
	}
	if catalog {
		state.Version = 3
		state.Tracked = map[int]TrackedPR{}
		state.Incremental = incremental || (resume && previous.Incremental)
		state.ObservedAt = observed.Format(time.RFC3339)
		state.Pending = true
		state.MethodologyVersion = analysis.MethodologyVersion
		state.BotIDs = append([]int64(nil), entry.BotIDs...)
	}
	if catalog {
		state.Progress = &Progress{Window: analysis.Window{Start: observed.Add(-90 * 24 * time.Hour).Format("2006-01-02T15:04:05.000Z"), End: observed.Format("2006-01-02T15:04:05.000Z")}}
	}
	if resume && previous.Progress != nil {
		progress := *previous.Progress
		state.Progress = &progress
	}
	r := reader{population: &Population{}, client: client, previous: previous, next: state, resume: resume, checkpoint: checkpoint, incremental: state.Incremental, replay: map[string]bool{}}
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
			included, err := r.population.record(pr, start, observed, entry.BotIDs)
			if err != nil {
				return Public{}, r.next, err
			}
			if !included {
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
	if catalog {
		cachedCoverage := map[int64]bool{}
		if r.resume {
			r.next.Progress.CoveredPRs = 0
			for _, raw := range eligible {
				if r.cachedStream(fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", path, raw.Number), true) && r.cachedStream(fmt.Sprintf("%s/issues/%d/comments?per_page=100", path, raw.Number), false) && r.cachedStream(fmt.Sprintf("%s/pulls/%d/comments?per_page=100", path, raw.Number), false) {
					cachedCoverage[raw.ID] = true
					r.next.Progress.CoveredPRs++
				}
			}
		}
		for index, raw := range eligible {
			pr := &input.PullRequests[index]
			reused := r.reuseClosedPR(raw, path, observed)
			priorTrack, tracked := r.previous.Tracked[raw.Number]
			checkedBefore, checkedErr := timestamp(priorTrack.CheckedAt)
			tracked = tracked && checkedErr == nil && !observed.Before(checkedBefore)
			if r.resume && tracked && priorTrack.ID == raw.ID {
				r.next.Tracked[raw.Number] = priorTrack
			}
			for _, stream := range []string{"reviews", "issue_comments", "review_comments"} {
				endpoint := fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", path, raw.Number)
				if stream == "issue_comments" {
					endpoint = fmt.Sprintf("%s/issues/%d/comments?per_page=100", path, raw.Number)
				}
				if stream == "review_comments" {
					endpoint = fmt.Sprintf("%s/pulls/%d/comments?per_page=100", path, raw.Number)
				}
				events, complete, err := r.events(ctx, endpoint, stream == "reviews")
				if stream == "reviews" {
					pr.Reviews = events
					pr.Coverage.Reviews = complete
				} else {
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
				}
				if err != nil {
					failures = append(failures, Failure{PR: raw.Number, Stream: stream, Reason: reason(err)})
				}
			}
			if pr.Coverage.Reviews && pr.Coverage.IssueComments && pr.Coverage.ReviewComments {
				checkedAt := observed.Format(time.RFC3339)
				if reused || (r.resume && tracked && priorTrack.ID == raw.ID) {
					checkedAt = priorTrack.CheckedAt
				}
				r.next.Tracked[raw.Number] = TrackedPR{ID: raw.ID, UpdatedAt: raw.Updated, CheckedAt: checkedAt, State: raw.State}
				if !cachedCoverage[raw.ID] {
					r.next.Progress.CoveredPRs++
				}
			} else if cachedCoverage[raw.ID] {
				r.next.Progress.CoveredPRs--
			}
		}
	} else {
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
	public.Population = r.population
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
	if r.incremental {
		public.Collection = r.collectionSummary(eligible, observed)
	}
	if err := schemas.Validate("repository-snapshot", public); err != nil {
		return Public{}, r.next, err
	}
	r.next.Pending = !complete
	return public, r.next, nil
}
