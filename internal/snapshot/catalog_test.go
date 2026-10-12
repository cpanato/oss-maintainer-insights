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
	return nil, json.Unmarshal(encoded, target)
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
	return &catalogFake{data: source, prs: prs, limit: 90}, observed
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
	if state.Policy != CatalogPolicy || len(state.Pages) != 76 {
		t.Fatal("wrong checkpoint policy or requests")
	}
	// Six newest PRs without reviews must remain selected, not replaced by reviewed PRs.
	if public.Result.FirstResponse.UnansweredPRs != 6 {
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
	if public.Selection.Selected != 12 || !public.Complete || public.Result.FirstResponse.UnansweredPRs != 6 {
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
	if public.Selection.Selected != 17 || public.Selection.Eligible != 19 || public.Result.CreationCohort.Authors != 17 {
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
	policy.MaxRequests = 90
	client, err := github.New(github.Options{Policy: policy, HTTP: &http.Client{Transport: catalogTransport{source: source}}})
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := CollectCatalog(context.Background(), client, Entry{Repository: "example/repo"}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if !public.Complete || public.Selection.Selected != 18 || public.Requests != 76 {
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
	if !public.Complete || public.Selection.Selected != 18 || public.SampleAge.Newest != client.prs[0].Created {
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
	if public.Selection.Selected != 17 {
		t.Fatalf("cross-boundary candidate was counted: %+v", public.Selection)
	}
}
