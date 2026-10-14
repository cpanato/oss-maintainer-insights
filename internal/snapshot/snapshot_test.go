package snapshot

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/github"
)

type fixture struct {
	Observed   string          `json:"observed_at"`
	Repository string          `json:"repository"`
	Metadata   json.RawMessage `json:"metadata"`
	PRs        json.RawMessage `json:"pull_requests"`
	Reviews    json.RawMessage `json:"reviews"`
	Issues     json.RawMessage `json:"issue_comments"`
	Inline     json.RawMessage `json:"review_comments"`
}
type fake struct {
	data        fixture
	used        int
	limit       int
	conditional int
}

func (f *fake) GetConditional(_ context.Context, path string, destination any, etag string) (http.Header, error) {
	if f.used >= f.limit {
		return nil, &github.Failure{Reason: "budget"}
	}
	f.used++
	headers := http.Header{"Etag": {`"fixture"`}}
	if etag == `"fixture"` {
		f.conditional++
		return headers, github.ErrNotModified
	}
	data := f.data.Inline
	if strings.HasPrefix(path, "/repos/") {
		data = f.data.Metadata
	} else if strings.HasSuffix(path, "/pulls?state=all&sort=created&direction=desc&per_page=30") {
		data = f.data.PRs
	} else if strings.Contains(path, "/reviews?") {
		data = f.data.Reviews
	} else if strings.Contains(path, "/issues/") {
		data = f.data.Issues
	}
	return headers, json.Unmarshal(data, destination)
}
func (f *fake) RequestsUsed() int           { return f.used }
func (f *fake) RateLimit() github.RateLimit { return github.RateLimit{} }
func load(t *testing.T) (fixture, time.Time) {
	t.Helper()
	data, err := os.ReadFile("../../tests/fixtures/live-api.json")
	if err != nil {
		t.Fatal(err)
	}
	var source fixture
	if err = json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	observed, err := time.Parse(time.RFC3339, source.Observed)
	if err != nil {
		t.Fatal(err)
	}
	return source, observed
}
func TestSharedSnapshotAndConditionalReplay(t *testing.T) {
	source, observed := load(t)
	client := &fake{data: source, limit: 20}
	public, state, err := Collect(context.Background(), client, Entry{Repository: source.Repository}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("../../tests/fixtures/live-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err = json.Unmarshal(actual, &a); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(expected, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go snapshot differs from shared TypeScript golden: %s", actual)
	}
	checkpoint, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(checkpoint), "DO NOT RETAIN") || strings.Contains(string(actual), "submitted_at") || strings.Contains(string(actual), "occurred_at") {
		t.Fatal("source payload leaked")
	}
	next := &fake{data: source, limit: 20}
	replay, _, err := Collect(context.Background(), next, Entry{Repository: source.Repository}, observed, state)
	if err != nil {
		t.Fatal(err)
	}
	if next.conditional != 20 || !reflect.DeepEqual(public, replay) {
		t.Fatal("conditional replay did not preserve sample")
	}
}
func TestPartialStreamsAreNotEmptySuccess(t *testing.T) {
	source, observed := load(t)
	client := &fake{data: source, limit: 4}
	public, _, err := Collect(context.Background(), client, Entry{Repository: source.Repository}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if public.Complete || public.Requests != 4 || public.Result.ReviewActivity.CoveredPRs != 0 || public.Result.FirstResponse.UnansweredPRs != 0 || public.Result.FirstResponse.CoveredPRs != 0 {
		t.Fatalf("incorrect partial coverage: %+v", public)
	}
}
func TestCheckpointPolicyMismatchRefetches(t *testing.T) {
	source, observed := load(t)
	first := &fake{data: source, limit: 20}
	_, state, err := Collect(context.Background(), first, Entry{Repository: source.Repository}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	state.Policy = "old-policy"
	client := &fake{data: source, limit: 20}
	_, _, err = Collect(context.Background(), client, Entry{Repository: source.Repository}, observed, state)
	if err != nil {
		t.Fatal(err)
	}
	if client.conditional != 0 {
		t.Fatal("reused incompatible checkpoints")
	}
}

func TestSavedBudgetCompletesEightSinglePagePRs(t *testing.T) {
	source, observed := load(t)
	var prs []rawPR
	if err := json.Unmarshal(source.PRs, &prs); err != nil {
		t.Fatal(err)
	}
	for number := 7; number <= 8; number++ {
		added := prs[0]
		added.ID = int64(100 + number)
		added.Number = number
		user := *added.User
		user.ID = int64(number)
		user.Login = "additional-author"
		added.User = &user
		prs = append(prs, added)
	}
	data, err := json.Marshal(prs)
	if err != nil {
		t.Fatal(err)
	}
	source.PRs = data
	client := &fake{data: source, limit: 30}
	public, _, err := Collect(context.Background(), client, Entry{Repository: source.Repository}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if !public.Complete || public.Requests != 26 || public.Result.FirstResponse.CoveredPRs != 8 || public.Result.ReviewActivity.CoveredPRs != 8 {
		t.Fatalf("saved sample incomplete: %+v", public)
	}
}

func TestCommandAcquisitionPrivacyAndConditionalReplay(t *testing.T) {
	source, observed := load(t)
	source.Issues = json.RawMessage(`[{"id":999,"user":{"id":901,"login":"prow-reviewer","type":"User"},"created_at":"2026-10-12T11:00:00Z","body":"/approve\nDO NOT RETAIN"}]`)
	client := &fake{data: source, limit: 23}
	public, state, err := Collect(context.Background(), client, Entry{Repository: source.Repository}, observed, State{})
	if err != nil {
		t.Fatal(err)
	}
	if public.Result.ReviewActivity.Sources.Prow != 7 || public.Result.ReviewActivity.ReviewUnits != 14 {
		t.Fatalf("commands not acquired: %+v", public.Result.ReviewActivity)
	}
	checkpoint, _ := json.Marshal(state)
	aggregate, _ := json.Marshal(public)
	if strings.Contains(string(checkpoint), "DO NOT RETAIN") || strings.Contains(string(checkpoint), `"body"`) || strings.Contains(string(aggregate), "review_commands") {
		t.Fatal("raw bodies or individual command events leaked")
	}
	replay, _, err := Collect(context.Background(), &fake{data: source, limit: 23}, Entry{Repository: source.Repository}, observed, state)
	if err != nil || !reflect.DeepEqual(replay, public) {
		t.Fatal("304 replay lost normalized command signals")
	}
	state.Version = 1
	fresh := &fake{data: source, limit: 23}
	if _, _, err := Collect(context.Background(), fresh, Entry{Repository: source.Repository}, observed, state); err != nil {
		t.Fatal(err)
	}
	if fresh.conditional != 0 {
		t.Fatal("legacy body-free checkpoint suppressed command extraction")
	}
}
