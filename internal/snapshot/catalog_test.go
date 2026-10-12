package snapshot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/github"
)

type catalogFake struct {
	data                          fixture
	prs                           []rawPR
	used, limit                   int
	malformed, emptyBand, overlap bool
}

func (f *catalogFake) RequestsUsed() int           { return f.used }
func (f *catalogFake) RateLimit() github.RateLimit { return github.RateLimit{} }
func (f *catalogFake) GetConditional(_ context.Context, path string, target any, _ string) (http.Header, error) {
	if f.used >= f.limit {
		return nil, &github.Failure{Reason: "budget"}
	}
	f.used++
	if strings.HasPrefix(path, "https://api.github.com/") {
		parsed, _ := url.Parse(path)
		path = parsed.RequestURI()
	}
	headers := http.Header{}
	var value any = []rawEvent{}
	switch {
	case strings.HasPrefix(path, "/repos/"):
		return headers, json.Unmarshal(f.data.Metadata, target)
	case strings.HasPrefix(path, "/repositories/42/pulls?"):
		parsed, _ := url.Parse(path)
		if parsed.Query().Get("sort") != "created" || parsed.Query().Get("direction") != "desc" || parsed.Query().Get("per_page") != "100" {
			tUnexpectedPolicy()
		}
		records := append([]rawPR(nil), f.prs...)
		sort.Slice(records, func(i, j int) bool { return records[i].Created > records[j].Created })
		if f.emptyBand {
			filtered := []rawPR{}
			for _, pr := range records {
				if pr.Number < 110 || pr.Number >= 120 {
					filtered = append(filtered, pr)
				}
			}
			records = filtered
		}
		page, _ := strconv.Atoi(parsed.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		start := (page - 1) * 100
		if f.overlap && page > 1 {
			start--
		}
		end := min(start+100, len(records))
		if start > len(records) {
			start = len(records)
		}
		value = records[start:end]
		if f.malformed && end > start {
			invalid := append([]rawPR(nil), records[start:end]...)
			invalid[0].Draft = nil
			value = invalid
		}
		if end < len(records) {
			parsed.RawQuery = parsed.Query().Encode()
			query := parsed.Query()
			query.Set("page", strconv.Itoa(page+1))
			parsed.RawQuery = query.Encode()
			headers.Set("Link", "<https://api.github.com"+parsed.String()+">; rel=\"next\"")
		}
	default:
		parts := strings.Split(path, "/")
		number, _ := strconv.Atoi(parts[4])
		for _, pr := range f.prs {
			if pr.Number == number && strings.Contains(path, "/reviews?") && number >= 110 {
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
func tUnexpectedPolicy() { panic("unexpected full-window enumeration policy") }
func catalogSource(t *testing.T) (*catalogFake, time.Time) {
	t.Helper()
	raw, err := os.ReadFile("../../tests/fixtures/catalog-api.json")
	if err != nil {
		t.Fatal(err)
	}
	var source fixture
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	var prs []rawPR
	if err = json.Unmarshal(source.PRs, &prs); err != nil {
		t.Fatal(err)
	}
	observed, err := timestamp(source.Observed)
	if err != nil {
		t.Fatal(err)
	}
	return &catalogFake{data: source, prs: prs, limit: 1300}, observed
}
func largeCatalogSource(t *testing.T, count int) (*catalogFake, time.Time) {
	client, observed := catalogSource(t)
	client.prs = nil
	for index := range count {
		draft := false
		number := 1000 + index
		client.prs = append(client.prs, rawPR{ID: int64(number), Number: number, User: &rawActor{ID: int64(number + 2000), Login: "author", Type: "User"}, Created: observed.Add(-time.Duration(index+1) * time.Hour).Format(time.RFC3339), Draft: &draft, State: "open"})
	}
	return client, observed
}
func TestCatalogWindowAndSharedMetrics(t *testing.T) {
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
	if err = json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("catalog snapshot differs from shared fixture: %s", encoded)
	}
	if state.Policy != CatalogPolicy || len(state.Pages) != 65 || !state.Progress.EnumerationComplete || state.Progress.CoveredPRs != 21 {
		t.Fatal("incorrect collection progress")
	}
	if public.Result.FirstResponse.UnansweredPRs != 7 {
		t.Fatal("unanswered PRs disappeared")
	}
}
func TestCatalogRetainsSparseWindowPeriods(t *testing.T) {
	client, observed := catalogSource(t)
	client.emptyBand = true
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if public.Selection.Selected != 14 || !public.Complete || public.Result.FirstResponse.UnansweredPRs != 7 {
		t.Fatalf("invalid sparse cohort: %+v", public.Selection)
	}
}
func TestCatalogMalformedPageCannotBecomeCompleteWindow(t *testing.T) {
	client, observed := catalogSource(t)
	client.malformed = true
	if _, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{}); err == nil {
		t.Fatal("malformed enumeration accepted")
	}
}
func TestCatalogBudgetPreservesCoverageDenominators(t *testing.T) {
	client, observed := catalogSource(t)
	client.limit = 30
	public, state, err := CollectCatalogWithCheckpoint(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if public.Complete || !state.Pending || public.Requests != 30 || public.Result.ReviewActivity.CoveredPRs != 9 || public.Result.FirstResponse.CoveredPRs != 9 || state.Progress.CoveredPRs != 9 {
		t.Fatalf("budget mislabeled progress: %+v", state.Progress)
	}
}
func TestCatalogFiltersBotsAndDrafts(t *testing.T) {
	client, observed := catalogSource(t)
	client.prs[0].User.Type = "Bot"
	client.prs[7].User.ID = 999
	draft := true
	client.prs[14].Draft = &draft
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo", BotIDs: []int64{999}}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if public.Selection.Selected != 18 || public.Selection.Eligible != 18 || public.Result.CreationCohort.Authors != 18 {
		t.Fatalf("ineligible PRs contributed: %+v", public.Selection)
	}
}

type catalogTransport struct{ source *catalogFake }

func (f catalogTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var value any
	headers, err := f.source.GetConditional(request.Context(), request.URL.RequestURI(), &value, "")
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}
func TestCatalogThroughNativeHTTPClient(t *testing.T) {
	source, observed := largeCatalogSource(t, 330)
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
	if !public.Complete || public.Selection.Selected != 330 || public.Requests != 995 || public.Selection.More || public.Selection.Truncated {
		t.Fatalf("full REST pagination failed: %+v requests=%d", public.Selection, public.Requests)
	}
}
func TestCatalogCreationBoundaryAndLaterPRs(t *testing.T) {
	client, observed := catalogSource(t)
	client.prs[0].Created = observed.Format(time.RFC3339)
	client.prs[1].Created = observed.Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	client.prs[2].Created = observed.Add(-90*24*time.Hour - time.Second).Format(time.RFC3339)
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if public.Selection.Selected != 19 || public.SampleAge.Oldest != client.prs[1].Created {
		t.Fatalf("incorrect half-open window: %+v", public.Selection)
	}
}
func TestCatalogMoreThanOneThousandPRsResumeAcrossRuns(t *testing.T) {
	client, observed := largeCatalogSource(t, 1200)
	client.limit = 1300
	_, state, err := CollectCatalogWithCheckpoint(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{}, nil)
	if err != nil || !state.Pending || state.Progress.EligiblePRs != 1200 || !state.Progress.EnumerationComplete {
		t.Fatalf("enumeration capped at 300/1000: %+v err=%v", state.Progress, err)
	}
	for attempt := range 3 {
		next, _ := largeCatalogSource(t, 1200)
		public, completed, err := CollectCatalogWithCheckpoint(context.Background(), next, Entry{Repository: "example/repo"}, observed.Add(time.Duration(attempt+1)*time.Hour), state, nil)
		if err != nil {
			t.Fatal(err)
		}
		state = completed
		if public.Complete {
			if public.Selection.Selected != 1200 || completed.Pending || public.Result.ReviewActivity.CoveredPRs != 1200 || public.Result.CollectedAt != observed.Format("2006-01-02T15:04:05.000Z") {
				t.Fatal("incorrect completed window")
			}
			return
		}
	}
	t.Fatal("checkpoint made no forward progress")
}
func TestCatalogPaginationOverlapDoesNotDoubleCount(t *testing.T) {
	client, observed := largeCatalogSource(t, 330)
	client.overlap = true
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if public.Selection.Selected != 330 || public.Selection.Listed != 330 {
		t.Fatalf("overlap corrupted population: %+v", public.Selection)
	}
}
func TestCatalogResumeReusesPagesAndRevalidatesVisibility(t *testing.T) {
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
		t.Fatalf("missing checkpoint writes=%d err=%v", writes, err)
	}
	resumed, _ := catalogSource(t)
	public, complete, err := CollectCatalogWithCheckpoint(context.Background(), resumed, Entry{Repository: "example/repo"}, observed.Add(10*24*time.Hour), state, func(progress State) error {
		if progress.Progress.CoveredPRs < state.Progress.CoveredPRs || progress.Progress.EnumeratedPRs < state.Progress.EnumeratedPRs {
			t.Fatalf("resume progress regressed: previous=%+v current=%+v", state.Progress, progress.Progress)
		}
		return nil
	})
	if err != nil || !public.Complete || complete.Pending || public.Requests != 36 || public.Result.CollectedAt != observed.Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("resume failed requests=%d err=%v", public.Requests, err)
	}
	denied, _ := catalogSource(t)
	denied.data.Metadata = json.RawMessage(`{"id":42,"full_name":"example/repo","private":true}`)
	if _, _, err = CollectCatalogWithCheckpoint(context.Background(), denied, Entry{Repository: "example/repo"}, observed, state, nil); err == nil || denied.used != 1 {
		t.Fatal("skipped visibility check")
	}
}
func TestCatalogResumeInvalidatesBotPolicyAndEmptyMetadata(t *testing.T) {
	client, observed := catalogSource(t)
	client.limit = 30
	_, state, _ := CollectCatalogWithCheckpoint(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{}, nil)
	changed, _ := catalogSource(t)
	public, _, err := CollectCatalogWithCheckpoint(context.Background(), changed, Entry{Repository: "example/repo", BotIDs: []int64{999}}, observed, state, nil)
	if err != nil || public.Requests != 65 {
		t.Fatalf("stale exclusion policy reused: %d %v", public.Requests, err)
	}
	before, _ := catalogSource(t)
	before.limit = 0
	_, empty, _ := CollectCatalogWithCheckpoint(context.Background(), before, Entry{Repository: "example/repo"}, observed, State{}, nil)
	fresh, _ := catalogSource(t)
	public, _, err = CollectCatalogWithCheckpoint(context.Background(), fresh, Entry{Repository: "example/repo"}, observed, empty, nil)
	if err != nil || !public.Complete {
		t.Fatal("empty checkpoint blocked acquisition")
	}
}
