package probe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/oss-maintainer-insights/internal/github"
)

type fakeResponse struct {
	body string
	link string
	err  error
}
type fakeClient struct {
	responses []fakeResponse
	used      int
	paths     []string
	max       int
}

func (c *fakeClient) Get(_ context.Context, path string, destination any) (http.Header, error) {
	if c.used >= c.max {
		return nil, &github.Failure{Reason: "budget"}
	}
	c.used++
	c.paths = append(c.paths, path)
	if len(c.responses) == 0 {
		return nil, errors.New("unexpected request")
	}
	next := c.responses[0]
	c.responses = c.responses[1:]
	if next.err != nil {
		return nil, next.err
	}
	if err := json.Unmarshal([]byte(next.body), destination); err != nil {
		return nil, err
	}
	header := make(http.Header)
	if next.link != "" {
		header.Set("Link", next.link)
	}
	return header, nil
}
func (c *fakeClient) RequestsUsed() int           { return c.used }
func (c *fakeClient) RateLimit() github.RateLimit { return github.RateLimit{} }

var observed = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

const metadata = `{"id":123,"full_name":"example/project","private":false}`
const oldPR = `{"id":1,"number":1,"created_at":"2020-01-01T00:00:00Z","draft":false,"user":{"id":100,"type":"User"}}`
const newPR = `{"id":2,"number":2,"created_at":"2026-10-01T00:00:00Z","draft":false,"user":{"id":101,"type":"User"}}`

func TestEnumeratesOldPRsAndMeasuresAllStreams(t *testing.T) {
	next := github.Origin + "/repos/example/project/pulls?page=2"
	events := github.Origin + "/repos/example/project/pulls/1/reviews?page=2"
	c := &fakeClient{max: 20, responses: []fakeResponse{
		{body: metadata}, {body: "[" + oldPR + "]", link: "<" + next + ">; rel=\"next\""},
		{body: `[{"id":11,"body":"never-publish-comment-body"}]`, link: "<" + events + ">; rel=\"next\""}, {body: `[{"id":12}]`},
		{body: `[]`}, {body: `[]`}, {body: "[" + newPR + "]"},
	}}
	report := Run(context.Background(), c, "example/project", observed, true)
	if !report.EnumerationComplete || report.ListedPRs != 2 || report.EligibleReviewPRs != 2 || report.EligibleCohortPRs != 1 || report.Requests != 7 || report.BackfillRequestLowerBound != 7 {
		t.Fatalf("incorrect report: %+v", report)
	}
	if len(report.EventSamples) != 1 || !report.EventSamples[0].CreatedBeforeWindow || report.EventSamples[0].Streams[0].Records != 2 || !report.EventSamples[0].Streams[0].Complete {
		t.Fatalf("incorrect streams: %+v", report.EventSamples)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "never-publish") || strings.Contains(string(encoded), "user") {
		t.Fatal("source content leaked into report")
	}
}
func TestBudgetPreservesEnumerationAndPartialStreams(t *testing.T) {
	c := &fakeClient{max: 3, responses: []fakeResponse{{body: metadata}, {body: "[" + oldPR + "]", link: "<" + github.Origin + "/repos/example/project/pulls?page=2>; rel=\"next\""}, {body: `[{"id":11}]`, link: "<" + github.Origin + "/repos/example/project/pulls/1/reviews?page=2>; rel=\"next\""}}}
	report := Run(context.Background(), c, "example/project", observed, false)
	if report.EnumerationComplete || report.FailureReason != "budget" || report.ListedPRs != 1 || report.Requests != 3 {
		t.Fatalf("incorrect partial report: %+v", report)
	}
	streams := report.EventSamples[0].Streams
	if streams[0].Complete || streams[0].Records != 1 || streams[0].FailureReason != "budget" || streams[1].FailureReason != "budget" {
		t.Fatalf("stream failures missing: %+v", streams)
	}
}
func TestRejectsPrivateRepositoriesAndInvalidMetadata(t *testing.T) {
	for _, tc := range []struct{ body, reason string }{{`{"id":123,"full_name":"example/project","private":true}`, "private_repository"}, {`{"id":123,"full_name":"example/project"}`, "invalid_response"}, {`{"id":123,"full_name":"https://evil.test/repo","private":false}`, "invalid_response"}} {
		c := &fakeClient{max: 10, responses: []fakeResponse{{body: tc.body}}}
		report := Run(context.Background(), c, "example/project", observed, true)
		if report.FailureReason != tc.reason || len(c.paths) != 1 {
			t.Fatalf("unexpected access: %+v", report)
		}
	}
}
func TestExcludesDraftBotsAndMissingActorsFromCostModel(t *testing.T) {
	c := &fakeClient{max: 10, responses: []fakeResponse{{body: metadata}, {body: `[
 {"id":1,"number":1,"created_at":"2020-01-01T00:00:00Z","draft":true,"user":{"id":1,"type":"User"}},
 {"id":2,"number":2,"created_at":"2020-01-01T00:00:00Z","draft":false,"user":{"id":2,"type":"Bot"}},
 {"id":3,"number":3,"created_at":"2020-01-01T00:00:00Z","draft":false,"user":null}]`}}}
	report := Run(context.Background(), c, "example/project", observed, false)
	if !report.EnumerationComplete || report.EligibleReviewPRs != 0 || len(report.EventSamples) != 0 || report.BackfillRequestLowerBound != 2 {
		t.Fatalf("incorrect exclusions: %+v", report)
	}
}
func TestSuccessfulEmptyRepositoryAndInvalidEnumeration(t *testing.T) {
	c := &fakeClient{max: 10, responses: []fakeResponse{{body: metadata}, {body: `[]`}}}
	report := Run(context.Background(), c, "example/project", observed, false)
	if !report.EnumerationComplete || report.ListedPRs != 0 || report.FailureReason != "" {
		t.Fatal("empty success misclassified")
	}
	for _, body := range []string{`null`, "[" + oldPR + "," + oldPR + "]", `[{"id":1,"number":1,"created_at":"2020-01-01T00:00:00Z"}]`} {
		c := &fakeClient{max: 10, responses: []fakeResponse{{body: metadata}, {body: body}}}
		report := Run(context.Background(), c, "example/project", observed, false)
		if report.EnumerationComplete || report.FailureReason != "invalid_response" {
			t.Fatalf("invalid enumeration accepted: %+v", report)
		}
	}
}

func TestPreservesHTTPFailureStatus(t *testing.T) {
	c := &fakeClient{max: 10, responses: []fakeResponse{{err: &github.Failure{Reason: "forbidden", Status: 403}}}}
	report := Run(context.Background(), c, "example/project", observed, true)
	if report.FailureReason != "forbidden" || report.FailureStatus != 403 || report.Requests != 1 {
		t.Fatalf("lost HTTP failure evidence: %+v", report)
	}
}
