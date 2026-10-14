package analysis

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const MethodologyVersion = "1.2.0"
const MinObservations = 5

func utc(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !strings.HasSuffix(value, "Z") || (t.Format("2006-01-02T15:04:05Z") != value && t.Format("2006-01-02T15:04:05.000Z") != value) {
		return time.Time{}, fmt.Errorf("invalid UTC timestamp: %s", value)
	}
	return t, nil
}
func actorValid(actor *Actor) bool {
	return actor == nil || (actor.ID > 0 && actor.ID <= 9007199254740991 && strings.TrimSpace(actor.Login) != "" && (actor.Type == "User" || actor.Type == "Bot"))
}
func qualifies(state string) bool {
	return state == "APPROVED" || state == "CHANGES_REQUESTED" || state == "COMMENTED"
}
func coverage(mode string, enumeration bool, covered, eligible int) string {
	if enumeration && covered == eligible {
		if mode == "live_sample" {
			return "complete_for_sample"
		}
		return "complete_for_window"
	}
	if eligible > 0 && covered == 0 {
		return "unavailable"
	}
	return "partial"
}
func metric(value float64, sufficient bool) Metric {
	if !sufficient {
		return Metric{Status: "insufficient_data"}
	}
	return Metric{Value: &value, Status: "available"}
}

// Analyze consumes normalized input. JSON Schema validation is performed at the caller boundary.
func Analyze(input Input) (Result, error) {
	var result Result
	if input.SchemaVersion != 1 || (input.Mode != "historical" && input.Mode != "live_sample") || input.Repository.ID <= 0 || input.Repository.FullName == "" {
		return result, errors.New("invalid input metadata")
	}
	start, err := utc(input.Window.Start)
	if err != nil {
		return result, err
	}
	end, err := utc(input.Window.End)
	if err != nil {
		return result, err
	}
	observed, err := utc(input.ObservedAt)
	if err != nil {
		return result, err
	}
	if !start.Before(end) || end.After(observed) {
		return result, errors.New("window must be nonempty and end at or before observation")
	}
	bots := map[int64]bool{}
	for _, id := range input.BotIDs {
		if id <= 0 || bots[id] {
			return result, errors.New("invalid or duplicate bot ID")
		}
		bots[id] = true
	}
	result = Result{SchemaVersion: 1, MethodologyVersion: MethodologyVersion, Repository: input.Repository, Mode: input.Mode, CollectedAt: input.ObservedAt, Window: input.Window}
	result.ReviewActivity.Distribution = []Reviewer{}
	result.ReviewActivity.Sources = &ReviewSources{}
	prIDs := map[int64]bool{}
	eligible := []PullRequest{}
	createdAt := map[int64]time.Time{}
	for _, pr := range input.PullRequests {
		if pr.ID <= 0 || prIDs[pr.ID] {
			return Result{}, errors.New("invalid or duplicate PR ID")
		}
		prIDs[pr.ID] = true
		created, err := utc(pr.CreatedAt)
		if err != nil {
			return Result{}, err
		}
		if created.After(observed) {
			return Result{}, errors.New("PR created after observation")
		}
		createdAt[pr.ID] = created
		if !actorValid(pr.Author) {
			return Result{}, errors.New("invalid author")
		}
		if pr.Outcome != "open" && pr.Outcome != "closed_unmerged" && pr.Outcome != "merged" {
			return Result{}, errors.New("invalid outcome")
		}
		reviews := make([]Event, 0, len(pr.Reviews))
		for _, r := range pr.Reviews {
			if !qualifies(r.State) && r.State != "PENDING" && r.State != "DISMISSED" {
				return Result{}, errors.New("invalid review state")
			}
			reviews = append(reviews, r.Event)
		}
		for _, stream := range [][]Event{reviews, pr.IssueComments, pr.ReviewComments} {
			ids := map[string]bool{}
			for _, e := range stream {
				if e.ID == "" || ids[e.ID] || !actorValid(e.Actor) {
					return Result{}, errors.New("invalid or duplicate event")
				}
				ids[e.ID] = true
				for _, command := range e.ReviewCommands {
					if command != "lgtm" && command != "approve" && command != "lgtm_cancel" && command != "approve_cancel" {
						return Result{}, errors.New("invalid review command")
					}
				}
				if _, err := utc(e.OccurredAt); err != nil {
					return Result{}, err
				}
			}
		}
		if pr.Draft {
			result.Exclusions.DraftPRs++
		} else if pr.Author == nil {
			result.Exclusions.MissingAuthors++
		} else {
			eligible = append(eligible, pr)
		}
	}
	type streams struct {
		reviews  []Review
		comments []Event
	}
	events := map[int64]streams{}
	human := func(e Event, pr PullRequest) bool {
		if e.Actor == nil {
			result.Exclusions.MissingEventActors++
			return false
		}
		if e.Actor.Type == "Bot" || bots[e.Actor.ID] {
			result.Exclusions.BotEvents++
			return false
		}
		if e.Actor.ID == pr.Author.ID {
			result.Exclusions.SelfEvents++
			return false
		}
		return true
	}
	cohort := []PullRequest{}
	authors := map[int64]bool{}
	for _, pr := range eligible {
		s := streams{}
		for _, r := range pr.Reviews {
			if human(r.Event, pr) && qualifies(r.State) {
				s.reviews = append(s.reviews, r)
			}
		}
		for _, stream := range [][]Event{pr.IssueComments, pr.ReviewComments} {
			for _, e := range stream {
				if human(e, pr) {
					s.comments = append(s.comments, e)
				}
			}
		}
		events[pr.ID] = s
		if !createdAt[pr.ID].Before(start) && createdAt[pr.ID].Before(end) {
			cohort = append(cohort, pr)
			if pr.Author.Type == "User" && !bots[pr.Author.ID] {
				authors[pr.Author.ID] = true
			}
			switch pr.Outcome {
			case "open":
				result.CreationCohort.Outcomes.Open++
			case "merged":
				result.CreationCohort.Outcomes.Merged++
			case "closed_unmerged":
				result.CreationCohort.Outcomes.ClosedUnmerged++
			}
		}
	}
	result.CreationCohort.EligiblePRs = len(cohort)
	result.CreationCohort.Authors = len(authors)
	result.CreationCohort.Coverage = coverage(input.Mode, input.Enumeration.CreationCohort, len(cohort), len(cohort))
	population := eligible
	enumeration := input.Enumeration.ReviewActivity
	if input.Mode == "live_sample" {
		population = cohort
		enumeration = input.Enumeration.CreationCohort
	}
	reviewers := map[int64]Reviewer{}
	labelAt := map[int64]time.Time{}
	labelEvent := map[int64]string{}
	for _, pr := range population {
		if !pr.Coverage.Reviews || !pr.Coverage.IssueComments || !pr.Coverage.ReviewComments {
			continue
		}
		result.ReviewActivity.CoveredPRs++
		pairs := reviewPairs(events[pr.ID].reviews, events[pr.ID].comments, createdAt[pr.ID], observed)
		for _, pair := range pairs {
			r := pair.event
			at, _ := utc(r.OccurredAt)
			if input.Mode == "historical" && (at.Before(start) || !at.Before(end)) {
				continue
			}
			id := r.Actor.ID
			current := reviewers[id]
			if current.Sources == nil {
				current.Sources = &ReviewSources{}
			}
			if pair.github {
				current.Sources.GitHub++
			}
			if pair.prow {
				current.Sources.Prow++
			}
			current.ReviewUnits++
			if current.ReviewUnits == 1 || at.After(labelAt[id]) || (at.Equal(labelAt[id]) && r.ID > labelEvent[id]) {
				current.Actor = *r.Actor
				current.Actor.Login = strings.TrimSpace(current.Actor.Login)
				labelAt[id] = at
				labelEvent[id] = r.ID
			}
			reviewers[id] = current
		}
	}
	for _, r := range reviewers {
		result.ReviewActivity.Distribution = append(result.ReviewActivity.Distribution, r)
		result.ReviewActivity.ReviewUnits += r.ReviewUnits
		result.ReviewActivity.Sources.GitHub += r.Sources.GitHub
		result.ReviewActivity.Sources.Prow += r.Sources.Prow
	}
	distribution := result.ReviewActivity.Distribution
	sort.Slice(distribution, func(i, j int) bool {
		if distribution[i].ReviewUnits == distribution[j].ReviewUnits {
			return distribution[i].Actor.ID < distribution[j].Actor.ID
		}
		return distribution[i].ReviewUnits > distribution[j].ReviewUnits
	})
	count, accumulated := 0, 0
	for accumulated*5 < result.ReviewActivity.ReviewUnits*4 {
		accumulated += distribution[count].ReviewUnits
		count++
	}
	sufficient := result.ReviewActivity.CoveredPRs >= MinObservations && result.ReviewActivity.ReviewUnits > 0
	result.ReviewActivity.Concentration80 = metric(float64(count), sufficient)
	share := 0.0
	if sufficient {
		share = float64(distribution[0].ReviewUnits) / float64(result.ReviewActivity.ReviewUnits)
	}
	result.ReviewActivity.LargestReviewerShare = metric(share, sufficient)
	result.ReviewActivity.EligiblePRs = len(population)
	result.ReviewActivity.Participants = len(distribution)
	result.ReviewActivity.Coverage = coverage(input.Mode, enumeration, result.ReviewActivity.CoveredPRs, len(population))
	durations := []float64{}
	for _, pr := range cohort {
		if !pr.Coverage.Reviews || !pr.Coverage.IssueComments || !pr.Coverage.ReviewComments {
			continue
		}
		result.FirstResponse.CoveredPRs++
		responses := append([]Event{}, events[pr.ID].comments...)
		for _, r := range events[pr.ID].reviews {
			responses = append(responses, r.Event)
		}
		var first time.Time
		for _, e := range responses {
			at, _ := utc(e.OccurredAt)
			if !at.Before(createdAt[pr.ID]) && !at.After(observed) && (first.IsZero() || at.Before(first)) {
				first = at
			}
		}
		if !first.IsZero() {
			durations = append(durations, float64(first.UnixMilli()-createdAt[pr.ID].UnixMilli()))
		}
	}
	sort.Float64s(durations)
	median := 0.0
	if len(durations) >= MinObservations {
		middle := len(durations) / 2
		median = durations[middle]
		if len(durations)%2 == 0 {
			median = (durations[middle-1] + durations[middle]) / 2
		}
	}
	result.FirstResponse.EligiblePRs = len(cohort)
	result.FirstResponse.RespondedPRs = len(durations)
	result.FirstResponse.UnansweredPRs = result.FirstResponse.CoveredPRs - len(durations)
	result.FirstResponse.MedianMS = metric(median, len(durations) >= MinObservations)
	result.FirstResponse.Coverage = coverage(input.Mode, input.Enumeration.CreationCohort, result.FirstResponse.CoveredPRs, len(cohort))
	return result, nil
}
