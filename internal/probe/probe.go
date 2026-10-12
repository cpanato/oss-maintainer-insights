// Package probe measures public endpoint access and bounded collection cost.
// Reports contain counts and coverage, never source bodies, actors or credentials.
package probe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/github"
)

type Getter interface {
	Get(context.Context, string, any) (http.Header, error)
	RequestsUsed() int
	RateLimit() github.RateLimit
}
type Stream struct {
	Name          string `json:"name"`
	Requests      int    `json:"requests"`
	Records       int    `json:"records"`
	Complete      bool   `json:"complete"`
	FailureReason string `json:"failure_reason,omitempty"`
	FailureStatus int    `json:"failure_http_status,omitempty"`
}
type Sample struct {
	PRNumber            int      `json:"pr_number"`
	CreatedBeforeWindow bool     `json:"created_before_window"`
	Streams             []Stream `json:"streams"`
}
type Report struct {
	SchemaVersion             int              `json:"schema_version"`
	Mode                      string           `json:"mode"`
	Repository                string           `json:"repository"`
	RepositoryID              int64            `json:"repository_id"`
	ObservedAt                string           `json:"observed_at"`
	Authenticated             bool             `json:"authenticated"`
	RequestedDays             int              `json:"requested_days"`
	Requests                  int              `json:"requests"`
	RateLimit                 github.RateLimit `json:"rate_limit"`
	EnumerationComplete       bool             `json:"enumeration_complete"`
	EnumerationPages          int              `json:"enumeration_pages"`
	ListedPRs                 int              `json:"listed_prs"`
	EligibleReviewPRs         int              `json:"eligible_review_prs"`
	EligibleCohortPRs         int              `json:"eligible_cohort_prs"`
	BackfillRequestLowerBound int              `json:"backfill_request_lower_bound"`
	EventSamples              []Sample         `json:"event_samples"`
	FailureReason             string           `json:"failure_reason,omitempty"`
	FailureStatus             int              `json:"failure_http_status,omitempty"`
}
type pullRequest struct {
	ID        int64     `json:"id"`
	Number    int       `json:"number"`
	CreatedAt time.Time `json:"created_at"`
	Draft     *bool     `json:"draft"`
	User      *struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"user"`
}

func stopped(err error) string {
	var failure *github.Failure
	if errors.As(err, &failure) {
		return failure.Reason
	}
	return "invalid_response"
}
func failureStatus(err error) int {
	var failure *github.Failure
	if errors.As(err, &failure) {
		return failure.Status
	}
	return 0
}
func inspectStream(ctx context.Context, client Getter, path, name string) Stream {
	before := client.RequestsUsed()
	stream := Stream{Name: name}
	seen := map[string]bool{}
	eventIDs := map[int64]bool{}
	for path != "" {
		if seen[path] {
			stream.FailureReason = "pagination_cycle"
			break
		}
		seen[path] = true
		var records []struct {
			ID int64 `json:"id"`
		}
		headers, err := client.Get(ctx, path, &records)
		if err != nil {
			stream.FailureReason = stopped(err)
			stream.FailureStatus = failureStatus(err)
			break
		}
		if records == nil {
			stream.FailureReason = "invalid_response"
			break
		}
		valid := true
		for _, record := range records {
			if record.ID <= 0 || eventIDs[record.ID] {
				valid = false
				break
			}
			eventIDs[record.ID] = true
		}
		if !valid {
			stream.FailureReason = "invalid_response"
			break
		}
		stream.Records += len(records)
		path, err = github.NextPage(headers.Get("Link"))
		if err != nil {
			stream.FailureReason = stopped(err)
			stream.FailureStatus = failureStatus(err)
			break
		}
		if path == "" {
			stream.Complete = true
		}
	}
	stream.Requests = client.RequestsUsed() - before
	return stream
}

// Run enumerates oldest PRs first to include reviews on pre-window PRs.
// Only the first eligible PR has its three streams sampled. A cap never implies completeness.
func Run(ctx context.Context, client Getter, repository string, observed time.Time, authenticated bool) Report {
	report := Report{SchemaVersion: 1, Mode: "feasibility_probe", Repository: repository, ObservedAt: observed.UTC().Format(time.RFC3339), Authenticated: authenticated, RequestedDays: 90, EventSamples: []Sample{}}
	before := client.RequestsUsed()
	path, err := github.RepositoryPath(repository)
	if err != nil {
		report.FailureReason = "invalid_input"
		return report
	}
	var metadata struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Private  *bool  `json:"private"`
	}
	_, err = client.Get(ctx, path, &metadata)
	if err != nil {
		report.FailureReason = stopped(err)
		report.FailureStatus = failureStatus(err)
	} else if metadata.ID <= 0 || metadata.Private == nil {
		report.FailureReason = "invalid_response"
	} else if *metadata.Private {
		report.FailureReason = "private_repository"
	} else {
		report.RepositoryID = metadata.ID
		canonical, canonicalErr := github.RepositoryPath(metadata.FullName)
		if canonicalErr != nil {
			report.FailureReason = "invalid_response"
		} else {
			report.Repository = metadata.FullName
			enumerate(ctx, client, canonical, observed, &report)
		}
	}
	report.Requests = client.RequestsUsed() - before
	report.RateLimit = client.RateLimit()
	if report.RepositoryID > 0 {
		report.BackfillRequestLowerBound = 1 + report.EnumerationPages + report.EligibleReviewPRs + 2*report.EligibleCohortPRs
	}
	return report
}
func enumerate(ctx context.Context, client Getter, path string, observed time.Time, report *Report) {
	next := path + "/pulls?state=all&sort=created&direction=asc&per_page=100"
	seenPages := map[string]bool{}
	ids := map[int64]bool{}
	start := observed.AddDate(0, 0, -90)
	for next != "" {
		if seenPages[next] {
			report.FailureReason = "pagination_cycle"
			return
		}
		seenPages[next] = true
		var prs []pullRequest
		headers, err := client.Get(ctx, next, &prs)
		if err != nil {
			report.FailureReason = stopped(err)
			report.FailureStatus = failureStatus(err)
			return
		}
		if prs == nil {
			report.FailureReason = "invalid_response"
			return
		}
		for _, pr := range prs {
			if pr.ID <= 0 || pr.Number <= 0 || pr.CreatedAt.IsZero() || pr.CreatedAt.After(observed) || pr.Draft == nil || ids[pr.ID] {
				report.FailureReason = "invalid_response"
				return
			}
			ids[pr.ID] = true
		}
		report.EnumerationPages++
		report.ListedPRs += len(prs)
		for _, pr := range prs {
			if *pr.Draft || pr.User == nil || pr.User.ID <= 0 || pr.User.Type != "User" {
				continue
			}
			report.EligibleReviewPRs++
			if !pr.CreatedAt.Before(start) && pr.CreatedAt.Before(observed) {
				report.EligibleCohortPRs++
			}
			if len(report.EventSamples) == 0 {
				number := strconv.Itoa(pr.Number)
				sample := Sample{PRNumber: pr.Number, CreatedBeforeWindow: pr.CreatedAt.Before(start), Streams: []Stream{}}
				for _, endpoint := range []struct{ name, path string }{{"reviews", path + "/pulls/" + number + "/reviews?per_page=100"}, {"issue_comments", path + "/issues/" + number + "/comments?per_page=100"}, {"review_comments", path + "/pulls/" + number + "/comments?per_page=100"}} {
					sample.Streams = append(sample.Streams, inspectStream(ctx, client, endpoint.path, endpoint.name))
				}
				report.EventSamples = append(report.EventSamples, sample)
			}
		}
		next, err = github.NextPage(headers.Get("Link"))
		if err != nil {
			report.FailureReason = stopped(err)
			report.FailureStatus = failureStatus(err)
			return
		}
		if next == "" {
			report.EnumerationComplete = true
		}
	}
}

// ValidateRepository rejects unsupported inputs before any HTTP request.
func ValidateRepository(value string) error {
	if _, err := github.RepositoryPath(value); err != nil {
		return fmt.Errorf("invalid probe target: %w", err)
	}
	return nil
}
