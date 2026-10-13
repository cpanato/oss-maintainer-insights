package snapshot

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type incrementalFake struct {
	*catalogFake
	paths []string
	etags map[string]string
}

func (f *incrementalFake) GetConditional(ctx context.Context, path string, target any, etag string) (http.Header, error) {
	f.paths = append(f.paths, path)
	f.etags[path] = etag
	return f.catalogFake.GetConditional(ctx, strings.ReplaceAll(path, "/repositories/99/", "/repositories/42/"), target, etag)
}

func incrementalSource(t *testing.T) (*incrementalFake, time.Time) {
	t.Helper()
	f, observed := catalogSource(t)
	f.prs = nil
	f.limit = 100
	draft := false
	for index := range 5 {
		f.prs = append(f.prs, rawPR{ID: int64(2000 + index), Number: 110 + index,
			User:    &rawActor{ID: int64(500 + index), Login: "author", Type: "User"},
			Created: observed.Add(-time.Duration(index+1) * 24 * time.Hour).Format(time.RFC3339),
			Updated: observed.Add(-time.Hour).Format(time.RFC3339), Draft: &draft, State: "closed"})
	}
	return &incrementalFake{catalogFake: f, etags: map[string]string{}}, observed
}

func initialIncremental(t *testing.T) (*incrementalFake, time.Time, Public, State) {
	t.Helper()
	f, observed := incrementalSource(t)
	public, state, err := CollectCatalogIncrementalWithCheckpoint(context.Background(), f, Entry{Repository: "example/repo"}, observed, State{}, nil)
	if err != nil || !public.Complete || public.Requests != 17 || state.Version != 3 || len(state.Tracked) != 5 || public.Collection.RefreshedPRs != 5 {
		t.Fatalf("initial collection: %+v, %v", public, err)
	}
	f.used = 0
	f.paths = nil
	return f, observed, public, state
}

func TestIncrementalClosedPRsReuseEventsAndDiscloseTheirAge(t *testing.T) {
	f, observed, prior, state := initialIncremental(t)
	public, _, err := CollectCatalogIncrementalWithCheckpoint(context.Background(), f, Entry{Repository: "example/repo"}, observed.Add(24*time.Hour), state, nil)
	if err != nil || !public.Complete || public.Requests != 2 || public.Collection.ReusedPRs != 5 || public.Collection.RefreshedPRs != 0 || public.Collection.EventsCheckedSince != observed.Format(time.RFC3339) {
		t.Fatalf("reuse: %+v, %v", public, err)
	}
	if public.Result.ReviewActivity.ReviewUnits != prior.Result.ReviewActivity.ReviewUnits {
		t.Fatal("cache reuse changed metrics")
	}
}

func TestIncrementalOpenChangedReopenedAndIncompletePRsRefreshAllStreams(t *testing.T) {
	for _, scenario := range []string{"open", "changed", "reopened", "missing-stream", "future-check"} {
		t.Run(scenario, func(t *testing.T) {
			f, observed, _, state := initialIncremental(t)
			next := observed.Add(24 * time.Hour)
			switch scenario {
			case "open":
				f.prs[0].State = "open"
				tracked := state.Tracked[110]
				tracked.State = "open"
				state.Tracked[110] = tracked
			case "changed":
				f.prs[0].Updated = next.Add(-time.Minute).Format(time.RFC3339)
			case "reopened":
				f.prs[0].State = "open"
			case "missing-stream":
				delete(state.Pages, "/repositories/42/pulls/110/reviews?per_page=100")
			case "future-check":
				tracked := state.Tracked[110]
				tracked.CheckedAt = next.Add(time.Hour).Format(time.RFC3339)
				state.Tracked[110] = tracked
			}
			public, _, err := CollectCatalogIncrementalWithCheckpoint(context.Background(), f, Entry{Repository: "example/repo"}, next, state, nil)
			if err != nil || !public.Complete || public.Requests != 5 || public.Collection.RefreshedPRs != 1 || public.Collection.ReusedPRs != 4 {
				t.Fatalf("refresh: %+v, %v", public, err)
			}
			for _, endpoint := range []string{"/pulls/110/reviews?", "/issues/110/comments?", "/pulls/110/comments?"} {
				found := false
				for _, path := range f.paths {
					found = found || strings.Contains(path, endpoint)
				}
				if !found {
					t.Fatalf("missing refresh for %s", endpoint)
				}
			}
		})
	}
}

func TestIncrementalReconciliationRechecksUnchangedClosedPRs(t *testing.T) {
	f, observed, _, state := initialIncremental(t)
	public, _, err := CollectCatalogIncrementalWithCheckpoint(context.Background(), f, Entry{Repository: "example/repo"}, observed.Add(reconciliationInterval), state, nil)
	if err != nil || !public.Complete || public.Requests != 17 || public.Collection.RefreshedPRs != 5 || public.Collection.ReusedPRs != 0 {
		t.Fatalf("reconciliation: %+v, %v", public, err)
	}
}

func TestIncrementalNewPRsAndRollingWindowMembership(t *testing.T) {
	for _, add := range []bool{true, false} {
		t.Run(strconv.FormatBool(add), func(t *testing.T) {
			f, observed, _, state := initialIncremental(t)
			if add {
				pr := f.prs[0]
				pr.ID, pr.Number = 3000, 200
				pr.Created = observed.Add(12 * time.Hour).Format(time.RFC3339)
				pr.Updated = pr.Created
				f.prs = append(f.prs, pr)
			} else {
				f.prs[0].Created = observed.Add(-91 * 24 * time.Hour).Format(time.RFC3339)
			}
			public, _, err := CollectCatalogIncrementalWithCheckpoint(context.Background(), f, Entry{Repository: "example/repo"}, observed.Add(24*time.Hour), state, nil)
			selected, requests := 4, 2
			if add {
				selected, requests = 6, 5
			}
			if err != nil || !public.Complete || public.Selection.Selected != selected || public.Requests != requests {
				t.Fatalf("membership: %+v, %v", public, err)
			}
		})
	}
}

func TestIncrementalLegacyCheckpointFetchesMissingUpdateMetadata(t *testing.T) {
	f, observed, _, state := initialIncremental(t)
	state.Version, state.Tracked = 2, nil
	endpoint := "/repositories/42/pulls?state=all&sort=created&direction=desc&per_page=100"
	page := state.Pages[endpoint]
	var prs []rawPR
	if err := json.Unmarshal(page.Value, &prs); err != nil {
		t.Fatal(err)
	}
	for index := range prs {
		prs[index].Updated = ""
	}
	var err error
	page.Value, err = json.Marshal(prs)
	if err != nil {
		t.Fatal(err)
	}
	page.ETag = "legacy-validator"
	state.Pages[endpoint] = page
	public, _, err := CollectCatalogIncrementalWithCheckpoint(context.Background(), f, Entry{Repository: "example/repo"}, observed.Add(24*time.Hour), state, nil)
	if err != nil || !public.Complete || public.Requests != 17 || f.etags[endpoint] != "" {
		t.Fatalf("migration: %+v, %v", public, err)
	}
}

func TestIncrementalExclusionAndRepositoryIdentityChangesDoNotReuseEvents(t *testing.T) {
	for _, scenario := range []string{"bots", "repository"} {
		t.Run(scenario, func(t *testing.T) {
			f, observed, _, state := initialIncremental(t)
			entry := Entry{Repository: "example/repo"}
			if scenario == "bots" {
				entry.BotIDs = []int64{10}
			} else {
				f.data.Metadata = json.RawMessage(`{"id":99,"full_name":"example/repo","private":false}`)
			}
			public, _, err := CollectCatalogIncrementalWithCheckpoint(context.Background(), f, entry, observed.Add(24*time.Hour), state, nil)
			if err != nil || !public.Complete || public.Requests != 17 {
				t.Fatalf("identity: %+v, %v", public, err)
			}
			if scenario == "bots" && public.Result.ReviewActivity.ReviewUnits != 0 {
				t.Fatal("changed bot exclusions were ignored")
			}
		})
	}
}

func TestIncrementalInterruptedWindowResumesWithoutLosingFreshness(t *testing.T) {
	f, observed := incrementalSource(t)
	f.limit = 5
	partial, state, err := CollectCatalogIncrementalWithCheckpoint(context.Background(), f, Entry{Repository: "example/repo"}, observed, State{}, nil)
	if err != nil || partial.Complete || !state.Pending || len(state.Tracked) != 1 {
		t.Fatalf("partial: %+v, %v", partial, err)
	}
	f.used, f.limit = 0, 100
	public, next, err := CollectCatalogIncrementalWithCheckpoint(context.Background(), f, Entry{Repository: "example/repo"}, observed.Add(24*time.Hour), state, nil)
	if err != nil || !public.Complete || next.Pending || public.Requests != 13 || public.Collection.RefreshedPRs != 5 || public.Result.CollectedAt != observed.Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("resume: %+v, %v", public, err)
	}
}
