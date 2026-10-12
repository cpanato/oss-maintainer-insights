package snapshot

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/github"
)

type searchItem struct {
	Number      int       `json:"number"`
	User        *rawActor `json:"user"`
	Created     string    `json:"created_at"`
	PullRequest *struct{} `json:"pull_request"`
}
type searchPage struct {
	Total      *int         `json:"total_count"`
	Incomplete *bool        `json:"incomplete_results"`
	Items      []searchItem `json:"items"`
}

// Search only by repository, PR type, draft status, and creation time. Search
// pages are candidates, not a census; current PR details establish eligibility.
func (r *reader) catalogPRs(ctx context.Context, path, repository string, botIDs []int64, observed time.Time) ([]rawPR, Selection, error) {
	bots := map[int64]bool{}
	for _, id := range botIDs {
		bots[id] = true
	}
	candidates := [3][]searchItem{}
	selection := Selection{}
	for band := range 3 {
		end := observed.Add(-time.Duration(band) * 30 * 24 * time.Hour)
		start := end.Add(-30 * 24 * time.Hour)
		query := fmt.Sprintf("repo:%s is:pr draft:false created:%s..%s", repository, start.Format(time.RFC3339), end.Add(-time.Second).Format(time.RFC3339))
		var page searchPage
		next, err := r.get(ctx, "/search/issues?q="+url.QueryEscape(query)+"&sort=created&order=desc&per_page=100", &page)
		if err != nil {
			return nil, selection, err
		}
		if page.Total == nil || page.Incomplete == nil || *page.Incomplete || page.Items == nil || len(page.Items) > 100 || *page.Total < len(page.Items) {
			return nil, selection, &github.Failure{Reason: "invalid_response"}
		}
		selection.Listed += len(page.Items)
		selection.More = selection.More || next != "" || *page.Total > len(page.Items)
		sort.Slice(page.Items, func(i, j int) bool {
			first, _ := timestamp(page.Items[i].Created)
			second, _ := timestamp(page.Items[j].Created)
			if first.Equal(second) {
				return page.Items[i].Number > page.Items[j].Number
			}
			return first.After(second)
		})
		seen := map[int]bool{}
		for _, item := range page.Items {
			created, err := timestamp(item.Created)
			if err != nil || item.Number <= 0 || item.PullRequest == nil || seen[item.Number] || created.Before(start) || !created.Before(end) {
				return nil, selection, &github.Failure{Reason: "invalid_response"}
			}
			seen[item.Number] = true
			user, err := actor(item.User)
			if err != nil {
				return nil, selection, err
			}
			if user == nil || user.Type != "User" || bots[user.ID] {
				continue
			}
			selection.Eligible++
			if len(candidates[band]) < 100 {
				candidates[band] = append(candidates[band], item)
			} else {
				selection.Truncated = true
			}
		}
	}
	selected := []rawPR{}
	seenIDs := map[int64]bool{}
	// Interleave bands so a budget interruption never exhausts one age band first.
	for index := range 100 {
		for band := range 3 {
			if index >= len(candidates[band]) {
				continue
			}
			item := candidates[band][index]
			var pr rawPR
			if _, err := r.get(ctx, fmt.Sprintf("%s/pulls/%d", path, item.Number), &pr); err != nil {
				return nil, selection, err
			}
			if pr.ID <= 0 || pr.ID > 9007199254740991 || pr.Number != item.Number || pr.Draft == nil || (pr.State != "open" && pr.State != "closed") {
				return nil, selection, &github.Failure{Reason: "invalid_response"}
			}
			created, err := timestamp(pr.Created)
			if err != nil {
				return nil, selection, &github.Failure{Reason: "invalid_response"}
			}
			// Search indexes issue creation time, which can differ from the PR timestamp.
			// Detailed PR metadata determines the actual creation band and metric clock.
			end := observed.Add(-time.Duration(band) * 30 * 24 * time.Hour)
			start := end.Add(-30 * 24 * time.Hour)
			if created.Before(start) || !created.Before(end) {
				continue
			}
			if pr.Merged != nil {
				if _, err := timestamp(*pr.Merged); err != nil {
					return nil, selection, err
				}
			}
			user, err := actor(pr.User)
			if err != nil {
				return nil, selection, err
			}
			if *pr.Draft || user == nil || user.Type != "User" || bots[user.ID] {
				continue
			}
			if seenIDs[pr.ID] {
				return nil, selection, &github.Failure{Reason: "invalid_response"}
			}
			seenIDs[pr.ID] = true
			selected = append(selected, pr)
		}
	}
	selection.Selected = len(selected)
	return selected, selection, nil
}
