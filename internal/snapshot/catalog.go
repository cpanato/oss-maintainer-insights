package snapshot

import (
	"context"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/github"
)

// Enumerate every PR page from newest creation time back to the frozen window
// boundary. REST pagination avoids the search API's 1,000-result ceiling.
func (r *reader) catalogPRs(ctx context.Context, path, _ string, botIDs []int64, observed time.Time) ([]rawPR, Selection, error) {
	selection := Selection{}
	selected := []rawPR{}
	seen := map[int64]bool{}
	visited := map[string]bool{}
	start := observed.Add(-90 * 24 * time.Hour)
	endpoint := path + "/pulls?state=all&sort=created&direction=desc&per_page=100"
	for endpoint != "" {
		if visited[endpoint] {
			return nil, selection, &github.Failure{Reason: "invalid_response"}
		}
		visited[endpoint] = true
		var records []rawPR
		next, err := r.get(ctx, endpoint, &records)
		if err != nil {
			return nil, selection, err
		}
		if records == nil || len(records) > 100 || (len(records) == 0 && next != "") {
			return nil, selection, &github.Failure{Reason: "invalid_response"}
		}
		boundary := false
		previousCreated := time.Time{}
		for _, pr := range records {
			if pr.ID <= 0 || pr.ID > 9007199254740991 || pr.Number <= 0 || pr.Draft == nil || (pr.State != "open" && pr.State != "closed") {
				return nil, selection, &github.Failure{Reason: "invalid_response"}
			}
			created, err := timestamp(pr.Created)
			if err != nil || (!previousCreated.IsZero() && created.After(previousCreated)) {
				return nil, selection, &github.Failure{Reason: "invalid_response"}
			}
			previousCreated = created
			if pr.Updated != "" {
				updated, err := timestamp(pr.Updated)
				if err != nil || updated.Before(created) {
					return nil, selection, &github.Failure{Reason: "invalid_response"}
				}
			}
			if seen[pr.ID] {
				continue
			}
			seen[pr.ID] = true
			selection.Listed++
			included, err := r.population.record(pr, start, observed, botIDs)
			if err != nil {
				return nil, selection, err
			}
			if created.Before(start) {
				boundary = true
				continue
			}
			if !created.Before(observed) {
				continue
			}
			if pr.Merged != nil {
				if _, err := timestamp(*pr.Merged); err != nil {
					return nil, selection, err
				}
			}
			if !included {
				continue
			}
			selection.Eligible++
			selected = append(selected, pr)
		}
		r.next.Progress.EnumeratedPRs = max(r.next.Progress.EnumeratedPRs, selection.Listed)
		r.next.Progress.EligiblePRs = max(r.next.Progress.EligiblePRs, selection.Eligible)
		if r.population.Inspected >= r.next.Progress.EnumeratedPRs {
			r.next.Progress.Population = r.population
		}
		if boundary {
			break
		}
		endpoint = next
	}
	selection.Selected = len(selected)
	r.next.Progress.EnumeratedPRs = selection.Listed
	r.next.Progress.EligiblePRs = selection.Eligible
	r.next.Progress.EnumerationComplete = true
	r.next.Progress.Population = r.population
	return selected, selection, nil
}
