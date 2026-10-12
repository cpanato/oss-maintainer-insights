package snapshot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/github"
)

type catalogFake struct {
	searchOffset time.Duration
	data         fixture
	prs          []rawPR
	used         int
	limit        int
	incomplete   bool
	emptyBand    bool
}

func (f *catalogFake) RequestsUsed() int           { return f.used }
func (f *catalogFake) RateLimit() github.RateLimit { return github.RateLimit{} }
func (f *catalogFake) GetConditional(_ context.Context, path string, target any, _ string) (http.Header, error) {
	if f.used >= f.limit {
		return nil, &github.Failure{Reason: "budget"}
	}
	f.used++
	headers := http.Header{}
	var value any = []rawEvent{}
	switch {
	case strings.HasPrefix(path, "/repos/"):
		return nil, json.Unmarshal(f.data.Metadata, target)
	case strings.HasPrefix(path, "/search/issues?"):
		parsed, _ := url.Parse(path)
		query := parsed.Query().Get("q")
		if !strings.Contains(query, "repo:example/repo is:pr draft:false") || parsed.Query().Get("sort") != "created" || parsed.Query().Get("order") != "desc" {
			panic("unexpected search policy")
		}
		fields := strings.Fields(query)
		bounds := strings.Split(strings.TrimPrefix(fields[3], "created:"), "..")
		start, _ := timestamp(bounds[0])
		end, _ := timestamp(bounds[1])
		end = end.Add(time.Second)
		items := []searchItem{}
		for _, pr := range f.prs {
			created, _ := timestamp(pr.Created)
			created = created.Add(f.searchOffset)
			if !created.Before(start) && created.Before(end) {
				items = append(items, searchItem{Number: pr.Number, User: pr.User, Created: created.Format(time.RFC3339), PullRequest: &struct{}{}})
			}
		}
		if f.emptyBand && len(items) > 0 && items[0].Number >= 110 && items[0].Number < 120 {
			items = []searchItem{}
		}
		total := len(items)
		if len(items) > 100 {
			items = items[:100]
			headers.Set("Link", "<https://api.github.com"+path+"&page=2>; rel=\"next\"")
		}
		value = searchPage{Total: &total, Incomplete: &f.incomplete, Items: items}
	default:
		parts := strings.Split(path, "/")
		number, _ := strconv.Atoi(parts[4])
		for _, pr := range f.prs {
			if pr.Number != number {
				continue
			}
			if len(parts) == 5 {
				value = pr
			} else if strings.Contains(path, "/reviews?") && number >= 110 {
				created, _ := timestamp(pr.Created)
				submitted := created.Add(time.Hour).Format(time.RFC3339)
				value = []rawEvent{{ID: int64(number + 5000), User: &rawActor{ID: 10, Login: "reviewer", Type: "User"}, Submitted: &submitted, State: "APPROVED"}}
			}
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return headers, json.Unmarshal(encoded, target)
}
func catalogSource(t *testing.T) (*catalogFake, time.Time) {
	t.Helper()
	raw, err := os.ReadFile("../../tests/fixtures/catalog-api.json")
	if err != nil {
		t.Fatal(err)
	}
	var source fixture
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	var prs []rawPR
	if err := json.Unmarshal(source.PRs, &prs); err != nil {
		t.Fatal(err)
	}
	observed, err := timestamp(source.Observed)
	if err != nil {
		t.Fatal(err)
	}
	return &catalogFake{data: source, prs: prs, limit: 1300}, observed
}
func TestCatalogBandsAndSharedMetrics(t *testing.T) {
	client, observed := catalogSource(t)
	public, state, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("../../tests/fixtures/catalog-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(public)
	var actual, want any
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("catalog snapshot differs from shared fixture: %s", encoded)
	}
	if state.Policy != CatalogPolicy || len(state.Pages) != 88 {
		t.Fatal("wrong checkpoint policy or requests")
	}
	// Seven newest PRs without reviews must remain selected, not replaced by reviewed PRs.
	if public.Result.FirstResponse.UnansweredPRs != 7 {
		t.Fatal("unanswered PRs disappeared from the sample")
	}
}
func TestCatalogEmptyBandDoesNotBorrowFromAnother(t *testing.T) {
	client, observed := catalogSource(t)
	client.emptyBand = true
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if public.Selection.Selected != 14 || !public.Complete || public.Result.FirstResponse.UnansweredPRs != 7 {
		t.Fatalf("empty band filled from another: %+v", public.Selection)
	}
}
func TestCatalogIncompleteSearchCannotBecomeCompleteSample(t *testing.T) {
	client, observed := catalogSource(t)
	client.incomplete = true
	if _, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{}); err == nil {
		t.Fatal("incomplete search accepted")
	}
}
func TestCatalogBudgetPreservesCoverageDenominators(t *testing.T) {
	client, observed := catalogSource(t)
	client.limit = 30
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if public.Complete || public.Requests != 30 || public.Result.ReviewActivity.CoveredPRs != 0 || public.Result.FirstResponse.CoveredPRs != 0 || public.Result.FirstResponse.UnansweredPRs != 0 {
		t.Fatalf("budget mislabeled coverage: %+v", public)
	}
}

func TestCatalogFiltersBotsAndConfirmsCurrentDraftStatus(t *testing.T) {
	client, observed := catalogSource(t)
	client.prs[0].User.Type = "Bot"
	client.prs[7].User.ID = 999
	draft := true
	client.prs[14].Draft = &draft
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo", BotIDs: []int64{999}}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	// Bots are removed before candidate selection; a candidate that became a draft
	// is removed on detail confirmation without selecting a replacement by activity.
	if public.Selection.Selected != 18 || public.Selection.Eligible != 19 || public.Result.CreationCohort.Authors != 18 {
		t.Fatalf("ineligible authors/drafts contributed: %+v", public.Selection)
	}
}

type catalogTransport struct{ source *catalogFake }

func (f catalogTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var value any
	_, err := f.source.GetConditional(request.Context(), request.URL.RequestURI(), &value, "")
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}
func TestCatalogThroughNativeHTTPClient(t *testing.T) {
	source, observed := catalogSource(t)
	policy := github.DefaultPolicy()
	policy.MaxRequests = 1300
	client, err := github.New(github.Options{Policy: policy, HTTP: &http.Client{Transport: catalogTransport{source: source}}})
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if !public.Complete || public.Selection.Selected != 21 || public.Requests != 88 {
		t.Fatalf("native HTTP acquisition failed: %+v", public)
	}
}

func TestCatalogUsesDetailedCreationTimeWhenSearchDiffers(t *testing.T) {
	client, observed := catalogSource(t)
	client.searchOffset = time.Second
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if !public.Complete || public.Selection.Selected != 21 || public.SampleAge.Newest != client.prs[0].Created {
		t.Fatalf("search timestamp mismatch rejected a valid sample: %+v", public)
	}
}
func TestCatalogDoesNotPlaceDetailedPRInWrongAgeBand(t *testing.T) {
	client, observed := catalogSource(t)
	client.searchOffset = time.Second
	// Search places this at the start of the newest band; actual PR creation
	// precedes that boundary. It must not silently enter the wrong band.
	client.prs[0].Created = observed.Add(-30*24*time.Hour - time.Second).Format(time.RFC3339)
	client.prs = append(client.prs[:6], client.prs[7:]...)
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if public.Selection.Selected != 19 {
		t.Fatalf("cross-boundary candidate was counted: %+v", public.Selection)
	}
}

func TestCatalogThreeHundredPRsAcrossBands(t *testing.T) {
	client, observed := catalogSource(t)
	client.prs = nil
	for band := range 3 {
		for index := range 110 {
			draft := false
			number := 1000 + band*110 + index
			client.prs = append(client.prs, rawPR{ID: int64(number), Number: number, User: &rawActor{ID: int64(number + 2000), Login: "author", Type: "User"}, Created: observed.Add(-time.Duration(band*30+1)*24*time.Hour - time.Duration(index)*time.Minute).Format(time.RFC3339), Draft: &draft, State: "open"})
		}
	}
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if !public.Complete || public.Selection.Selected != 300 || public.Selection.Listed != 300 || !public.Selection.More || public.Requests != 1204 || public.Result.ReviewActivity.CoveredPRs != 300 {
		t.Fatalf("expanded collection failed: selection=%+v requests=%d complete=%v", public.Selection, public.Requests, public.Complete)
	}
	if public.SampleAge.Newest != client.prs[0].Created || public.SampleAge.Oldest != client.prs[319].Created {
		t.Fatalf("age-band sampling changed: %+v", public.SampleAge)
	}
}

func TestCatalogResumeReusesCompletedPagesAndRevalidatesVisibility(t *testing.T) {
	client, observed := catalogSource(t)
	client.limit = 30
	writes := 0
	_, state, err := CollectCatalogWithCheckpoint(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{}, func(progress State) error {
		writes++
		if !progress.Pending || len(progress.Pages) == 0 {
			t.Fatal("missing progress")
		}
		return nil
	})
	if err != nil || !state.Pending || writes != 30 {
		t.Fatalf("missing pending checkpoint: writes=%d err=%v", writes, err)
	}
	resumed, _ := catalogSource(t)
	public, complete, err := CollectCatalogWithCheckpoint(context.Background(), resumed, Entry{Repository: "example/repo"}, observed.Add(time.Hour), state, nil)
	if err != nil || !public.Complete || complete.Pending || public.Requests != 59 || public.Result.CollectedAt != observed.Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("resume failed: requests=%d pending=%v err=%v", public.Requests, complete.Pending, err)
	}
	denied, _ := catalogSource(t)
	denied.data.Metadata = json.RawMessage(`{"id":42,"full_name":"example/repo","private":true}`)
	if _, _, err = CollectCatalogWithCheckpoint(context.Background(), denied, Entry{Repository: "example/repo"}, observed, state, nil); err == nil || denied.used != 1 {
		t.Fatal("resume skipped repository visibility check")
	}
}
func TestCatalogResumeInvalidatesChangedBotPolicy(t *testing.T) {
	client, observed := catalogSource(t)
	client.limit = 30
	_, state, _ := CollectCatalogWithCheckpoint(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{}, nil)
	changed, _ := catalogSource(t)
	public, _, err := CollectCatalogWithCheckpoint(context.Background(), changed, Entry{Repository: "example/repo", BotIDs: []int64{999}}, observed, state, nil)
	if err != nil || public.Requests != 88 {
		t.Fatalf("stale exclusion policy reused: requests=%d err=%v", public.Requests, err)
	}
}

func TestCatalogResumeIgnoresCheckpointBeforeMetadata(t *testing.T) {
	client, observed := catalogSource(t)
	client.limit = 0
	_, state, err := CollectCatalogWithCheckpoint(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{}, nil)
	if err == nil || !state.Pending || state.RepositoryID != 0 {
		t.Fatal("expected interruption before metadata")
	}
	fresh, _ := catalogSource(t)
	public, _, err := CollectCatalogWithCheckpoint(context.Background(), fresh, Entry{Repository: "example/repo"}, observed, state, nil)
	if err != nil || !public.Complete || public.Requests != 88 {
		t.Fatalf("empty checkpoint blocked acquisition: requests=%d err=%v", public.Requests, err)
	}
}
