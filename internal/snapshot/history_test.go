package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/analysis"
	"github.com/cpanato/oss-maintainer-insights/internal/github"
)

type memoryHistory map[string]json.RawMessage

func (s memoryHistory) Read(key string, target any) error {
	value, ok := s[key]
	if !ok {
		return fs.ErrNotExist
	}
	return json.Unmarshal(value, target)
}
func (s memoryHistory) Write(key string, value any) error {
	data, err := json.Marshal(value)
	if err == nil {
		s[key] = data
	}
	return err
}

type historyFake struct {
	used, limit int
	paths       []string
	data        map[string]any
	links       map[string]string
}

func (f *historyFake) GetConditional(_ context.Context, path string, target any, _ string) (http.Header, error) {
	path = strings.TrimPrefix(path, "https://api.github.com")
	if f.used >= f.limit {
		return nil, &github.Failure{Reason: "budget"}
	}
	f.used++
	f.paths = append(f.paths, path)
	value, ok := f.data[path]
	if !ok {
		return nil, fmt.Errorf("unexpected fixture path %s", path)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return http.Header{"Link": {f.links[path]}}, json.Unmarshal(data, target)
}
func (f *historyFake) RequestsUsed() int           { return f.used }
func (f *historyFake) RateLimit() github.RateLimit { return github.RateLimit{} }

func historySource(t *testing.T) (*historyFake, analysis.Input) {
	t.Helper()
	data, err := os.ReadFile("../../tests/fixtures/history-input.json")
	if err != nil {
		t.Fatal(err)
	}
	var input analysis.Input
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	f := &historyFake{limit: 100, data: map[string]any{}, links: map[string]string{}}
	f.data["/repos/example/repo"] = map[string]any{"id": 42, "full_name": "example/repo", "private": false}
	prs := []rawPR{}
	for _, pr := range input.PullRequests {
		draft := pr.Draft
		number := int(pr.ID)
		prs = append(prs, rawPR{ID: pr.ID, Number: number, User: &rawActor{ID: pr.Author.ID, Login: pr.Author.Login, Type: pr.Author.Type}, Created: pr.CreatedAt, Updated: input.ObservedAt, Draft: &draft, State: "closed"})
		for _, stream := range []string{"reviews", "issue_comments", "review_comments"} {
			events := []map[string]any{}
			endpoint := fmt.Sprintf("/repositories/42/pulls/%d/reviews?per_page=100", number)
			if stream == "reviews" {
				for _, review := range pr.Reviews {
					events = append(events, map[string]any{"id": mustEventID(review.ID), "user": review.Actor, "submitted_at": review.OccurredAt, "state": review.State})
				}
			} else {
				values := pr.ReviewComments
				endpoint = fmt.Sprintf("/repositories/42/pulls/%d/comments?per_page=100", number)
				if stream == "issue_comments" {
					values = pr.IssueComments
					endpoint = fmt.Sprintf("/repositories/42/issues/%d/comments?per_page=100", number)
				}
				for _, event := range values {
					events = append(events, map[string]any{"id": mustEventID(event.ID), "user": event.Actor, "created_at": event.OccurredAt, "review_commands": event.ReviewCommands, "body": "DO NOT RETAIN\n/lgtm"})
				}
			}
			f.data[endpoint] = events
		}
	}
	f.data["/repositories/42/pulls?state=all&sort=created&direction=desc&per_page=100&page=1"] = prs
	return f, input
}

func mustEventID(value string) int64 {
	var id int64
	_, _ = fmt.Sscan(value, &id)
	return id
}

func TestHistoryCollectsReviewsOnOldPRsAndMatchesSharedMonthlyGolden(t *testing.T) {
	f, input := historySource(t)
	observed, _ := time.Parse(time.RFC3339, input.ObservedAt)
	store := memoryHistory{}
	public, progress, err := CollectHistory(context.Background(), f, Entry{Repository: "example/repo"}, observed, store, false, nil)
	if err != nil || !progress.EnumerationComplete || progress.CoveredPRs != 3 || len(public.Months) != 4 || f.used != 11 {
		t.Fatalf("historical collection: %+v, %v, calls %d", public, err, f.used)
	}
	golden, err := os.ReadFile("../../tests/fixtures/history-expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected History
	if err := json.Unmarshal(golden, &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(public, expected) {
		actual, _ := json.Marshal(public)
		t.Fatalf("history differs from shared TypeScript golden: %s", actual)
	}
	for _, value := range store {
		if strings.Contains(string(value), "DO NOT RETAIN") || strings.Contains(string(value), `"body"`) {
			t.Fatal("unsanitized historical checkpoint")
		}
	}
}

func TestHistoryResumesInsideEventPaginationWithoutLosingOrDuplicatingUnits(t *testing.T) {
	f, input := historySource(t)
	root := "/repositories/42/pulls/2/reviews?per_page=100"
	next := root + "&page=2"
	f.links[root] = "<https://api.github.com" + next + ">; rel=\"next\""
	f.data[next] = []map[string]any{}
	f.limit = 3 // Metadata, listing, and first review page are persisted.
	observed, _ := time.Parse(time.RFC3339, input.ObservedAt)
	store := memoryHistory{}
	_, progress, err := CollectHistory(context.Background(), f, Entry{Repository: "example/repo"}, observed, store, false, nil)
	var failure *github.Failure
	if !errors.As(err, &failure) || failure.Reason != "budget" || progress.CoveredPRs != 0 {
		t.Fatalf("expected checkpointed budget stop: %+v, %v", progress, err)
	}
	f.used, f.limit, f.paths = 0, 100, nil
	public, progress, err := CollectHistory(context.Background(), f, Entry{Repository: "example/repo"}, observed.Add(24*time.Hour), store, false, nil)
	if err != nil || progress.CoveredPRs != 3 || public.CollectedAt != input.ObservedAt || public.Months[2].ReviewActivity.ReviewUnits != 1 || f.used != 10 {
		t.Fatalf("resumption: %+v, %v, calls %d", public, err, f.used)
	}
	for _, path := range f.paths {
		if path == root || strings.Contains(path, "/pulls?state=") {
			t.Fatal("refetched a completed frozen page")
		}
	}
}

func TestHistoryReconcilesDismissedReviewsAndLateEvents(t *testing.T) {
	f, input := historySource(t)
	observed, _ := time.Parse(time.RFC3339, input.ObservedAt)
	store := memoryHistory{}
	if _, _, err := CollectHistory(context.Background(), f, Entry{Repository: "example/repo"}, observed, store, false, nil); err != nil {
		t.Fatal(err)
	}
	f.used = 0
	public, _, err := CollectHistory(context.Background(), f, Entry{Repository: "example/repo"}, observed.Add(24*time.Hour), store, false, nil)
	if err != nil || f.used != 2 || public.Collection.ReusedPRs != 3 {
		t.Fatalf("reuse: %v, %d", err, f.used)
	}
	f.data["/repositories/42/pulls/1/reviews?per_page=100"] = []map[string]any{{"id": 11, "user": analysis.Actor{ID: 201, Login: "reviewer-one", Type: "User"}, "submitted_at": "2026-08-01T00:00:00Z", "state": "DISMISSED"}, {"id": 12, "user": analysis.Actor{ID: 201, Login: "reviewer-one", Type: "User"}, "submitted_at": "2026-09-01T00:00:00Z", "state": "APPROVED"}}
	f.used = 0
	public, _, err = CollectHistory(context.Background(), f, Entry{Repository: "example/repo"}, observed.Add(7*24*time.Hour), store, false, nil)
	if err != nil || f.used != 11 || public.Months[1].ReviewActivity.ReviewUnits != 0 || public.Months[2].ReviewActivity.ReviewUnits != 2 {
		t.Fatalf("reconciliation did not recompute the earliest qualifying month: %+v, %v", public, err)
	}
}

func TestHistoryPaginatesPastCreationWindowAndResumesWithoutDuplicatePRs(t *testing.T) {
	f, input := historySource(t)
	root := "/repositories/42/pulls?state=all&sort=created&direction=desc&per_page=100&page="
	prs := f.data[root+"1"].([]rawPR)
	f.data[root+"1"] = prs[:2]
	f.data[root+"2"] = []rawPR{prs[1], prs[2]}
	f.links[root+"1"] = "<https://api.github.com" + root + "2>; rel=\"next\""
	f.limit = 5
	store := memoryHistory{}
	observed, _ := time.Parse(time.RFC3339, input.ObservedAt)
	_, progress, err := CollectHistory(context.Background(), f, Entry{Repository: "example/repo"}, observed, store, false, nil)
	if err == nil || progress.CoveredPRs != 1 || progress.EnumeratedPRs != 1 {
		t.Fatalf("first page progress: %+v, %v", progress, err)
	}
	f.used, f.limit = 0, 100
	public, progress, err := CollectHistory(context.Background(), f, Entry{Repository: "example/repo"}, observed, store, false, nil)
	if err != nil || progress.EnumeratedPRs != 3 || progress.CoveredPRs != 3 || public.Months[1].ReviewActivity.ReviewUnits != 1 || f.used != 8 {
		t.Fatalf("listing resumption: %+v, %v, calls %d", public, err, f.used)
	}
}

func TestHistoryForcedRefreshAndChangedExclusionsDoNotReplayACompletedGeneration(t *testing.T) {
	for _, scenario := range []string{"force", "exclusions"} {
		t.Run(scenario, func(t *testing.T) {
			f, input := historySource(t)
			observed, _ := time.Parse(time.RFC3339, input.ObservedAt)
			store := memoryHistory{}
			entry := Entry{Repository: "example/repo"}
			if _, _, err := CollectHistory(context.Background(), f, entry, observed, store, false, nil); err != nil {
				t.Fatal(err)
			}
			f.used = 0
			force := scenario == "force"
			expectedPRs, expectedCalls := 3, 11
			if !force {
				entry.BotIDs = []int64{102}
				expectedPRs, expectedCalls = 2, 8
			}
			public, progress, err := CollectHistory(context.Background(), f, entry, observed, store, force, nil)
			if err != nil || progress.CoveredPRs != expectedPRs || public.Collection.RefreshedPRs != expectedPRs || f.used != expectedCalls {
				t.Fatalf("new generation: %+v, %v, calls %d", public, err, f.used)
			}
		})
	}
}

func TestHistoryRejectsNonsequentialOrChangedListingScope(t *testing.T) {
	for _, next := range []string{"/repositories/42/pulls?state=all&sort=created&direction=desc&per_page=100&page=42", "/repositories/99/pulls?state=all&sort=created&direction=desc&per_page=100&page=2"} {
		f, input := historySource(t)
		f.links["/repositories/42/pulls?state=all&sort=created&direction=desc&per_page=100&page=1"] = "<https://api.github.com" + next + ">; rel=\"next\""
		observed, _ := time.Parse(time.RFC3339, input.ObservedAt)
		_, progress, err := CollectHistory(context.Background(), f, Entry{Repository: "example/repo"}, observed, memoryHistory{}, false, nil)
		var failure *github.Failure
		if !errors.As(err, &failure) || failure.Reason != "invalid_response" || progress.CoveredPRs != 0 {
			t.Fatalf("invalid listing scope accepted: %+v, %v", progress, err)
		}
	}
}
