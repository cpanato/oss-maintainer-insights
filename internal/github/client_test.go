package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
func response(status int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
}
func scripted(t *testing.T, responses []*http.Response, policy Policy) (*Client, *[]*http.Request, *[]time.Duration) {
	t.Helper()
	calls := []*http.Request{}
	waits := []time.Duration{}
	now := time.Unix(100, 0)
	client, err := New(Options{Policy: policy, Now: func() time.Time { return now }, Sleep: func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		now = now.Add(delay)
		return nil
	}, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r)
		if len(responses) == 0 {
			return nil, errors.New("unexpected request")
		}
		result := responses[0]
		responses = responses[1:]
		return result, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls, &waits
}
func failure(t *testing.T, err error, expected string) {
	t.Helper()
	var stopped *Failure
	if !errors.As(err, &stopped) || stopped.Reason != expected {
		t.Fatalf("expected %s, got %v", expected, err)
	}
}
func TestBudgetAndRetries(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxRequests = 1
	client, calls, _ := scripted(t, []*http.Response{response(503, `{}`, nil)}, policy)
	var data []int
	_, err := client.Get(context.Background(), "/repos/a/b/pulls", &data)
	failure(t, err, "budget")
	if len(*calls) != 1 || client.RequestsUsed() != 1 {
		t.Fatal("retry exceeded request budget")
	}
	retry, _, waits := scripted(t, []*http.Response{response(503, `{}`, nil), response(200, `[]`, nil)}, DefaultPolicy())
	if _, err := retry.Get(context.Background(), "/repos/a/b/pulls", &data); err != nil {
		t.Fatal(err)
	}
	if len(*waits) != 1 || (*waits)[0] != time.Second || retry.RequestsUsed() != 2 {
		t.Fatal("missing bounded backoff")
	}
}
func TestRateLimits(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		headers            http.Header
		requests           int
	}{
		{"permission", `{"message":"Resource not accessible by integration"}`, "forbidden", nil, 1},
		{"primary", `{}`, "rate_limit", http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"160"}}, 1},
		{"secondary", `{"message":"You have exceeded a secondary rate limit"}`, "rate_limit", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, calls, _ := scripted(t, []*http.Response{response(403, tc.body, tc.headers)}, DefaultPolicy())
			var data any
			_, err := c.Get(context.Background(), "/repos/a/b", &data)
			failure(t, err, tc.reason)
			if len(*calls) != tc.requests {
				t.Fatal("unexpected retries")
			}
		})
	}
	for _, headers := range []http.Header{
		{"Retry-After": {"1"}},
		{"Retry-After": {time.Unix(101, 0).UTC().Format(http.TimeFormat)}},
		{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"101"}},
	} {
		c, _, waits := scripted(t, []*http.Response{response(429, `{}`, headers), response(200, `[]`, nil)}, DefaultPolicy())
		var data []int
		if _, err := c.Get(context.Background(), "/repos/a/b/pulls", &data); err != nil {
			t.Fatal(err)
		}
		if len(*waits) != 1 || (*waits)[0] != time.Second {
			t.Fatalf("incorrect retry deadline: %v", *waits)
		}
	}
	low, calls, _ := scripted(t, []*http.Response{response(200, `{}`, http.Header{"X-Ratelimit-Remaining": {"2"}, "X-Ratelimit-Reset": {"160"}})}, DefaultPolicy())
	var data any
	if _, err := low.Get(context.Background(), "/repos/a/b", &data); err != nil {
		t.Fatal(err)
	}
	snapshot := low.RateLimit()
	*snapshot.Remaining = 999
	_, err := low.Get(context.Background(), "/repos/a/b", &data)
	failure(t, err, "rate_limit")
	if len(*calls) != 1 {
		t.Fatal("ignored low allowance")
	}
}
func TestSafeRedirectsAndHeaders(t *testing.T) {
	next := Origin + "/repositories/1"
	client, calls, _ := scripted(t, []*http.Response{response(301, `{}`, http.Header{"Location": {next}}), response(200, `{}`, nil)}, DefaultPolicy())
	client.token = "synthetic-test-token"
	var data any
	if _, err := client.Get(context.Background(), "/repos/a/b", &data); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[1].URL.String() != next || (*calls)[0].Header.Get("Authorization") != "Bearer synthetic-test-token" {
		t.Fatal("incorrect redirect or authentication")
	}
	for _, location := range []string{"https://evil.test/repos/a/b", "http://api.github.com/repos/a/b", "https://api.github.com/user"} {
		c, calls, _ := scripted(t, []*http.Response{response(302, `{}`, http.Header{"Location": {location}})}, DefaultPolicy())
		_, err := c.Get(context.Background(), "/repos/a/b", &data)
		failure(t, err, "invalid_input")
		if len(*calls) != 1 {
			t.Fatal("followed unsafe redirect")
		}
	}
}
func TestPaginationLinks(t *testing.T) {
	next := Origin + "/repos/a/b/pulls?page=2"
	actual, err := NextPage(`<` + next + `>; type="application/json"; rel="next", <` + next + `>; rel="last"`)
	if err != nil || actual != next {
		t.Fatalf("incorrect pagination: %s %v", actual, err)
	}
	for _, header := range []string{"broken", `<` + next + `>; rel="next", <` + next + `>; rel="next"`, `<https://evil.test/repos/a/b>; rel="next"`} {
		if _, err := NextPage(header); err == nil {
			t.Fatal("accepted unsafe or malformed pagination")
		}
	}
	if actual, err := NextPage(""); err != nil || actual != "" {
		t.Fatal("empty complete page misclassified")
	}
}
func TestCancellationTimeoutAndSerialization(t *testing.T) {
	var mutex sync.Mutex
	inflight, maximum := 0, 0
	policy := DefaultPolicy()
	policy.Timeout = 5 * time.Millisecond
	transport := roundTrip(func(r *http.Request) (*http.Response, error) {
		mutex.Lock()
		inflight++
		if inflight > maximum {
			maximum = inflight
		}
		mutex.Unlock()
		<-r.Context().Done()
		mutex.Lock()
		inflight--
		mutex.Unlock()
		return nil, r.Context().Err()
	})
	client, err := New(Options{Policy: policy, HTTP: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 3 {
		group.Go(func() {
			var data any
			_, err := client.Get(context.Background(), "/repos/a/b", &data)
			failure(t, err, "timeout")
		})
	}
	group.Wait()
	if maximum != 1 || client.RequestsUsed() != 3 {
		t.Fatal("requests were not serialized")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var data any
	_, err = client.Get(ctx, "/repos/a/b", &data)
	failure(t, err, "cancelled")
	if client.RequestsUsed() != 3 {
		t.Fatal("cancelled request consumed budget")
	}
}
func TestInputAndInvalidResponses(t *testing.T) {
	for _, value := range []string{"a/..", "https://github.com/a/b", "a/b/c"} {
		if _, err := RepositoryPath(value); err == nil {
			t.Fatal("invalid repository accepted")
		}
	}
	client, calls, _ := scripted(t, nil, DefaultPolicy())
	var data any
	_, err := client.Get(context.Background(), "https://evil.test/repos/a/b", &data)
	failure(t, err, "invalid_input")
	if len(*calls) != 0 {
		t.Fatal("invalid URL was fetched")
	}
	malformed, _, _ := scripted(t, []*http.Response{response(200, `broken`, nil)}, DefaultPolicy())
	_, err = malformed.Get(context.Background(), "/repos/a/b", &data)
	failure(t, err, "invalid_response")
	missing, _, _ := scripted(t, []*http.Response{response(404, `{}`, nil)}, DefaultPolicy())
	_, err = missing.Get(context.Background(), "/repos/a/b", &data)
	failure(t, err, "not_found")
}

func TestTransientCooldownIsNotRateLimit(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxWait = 0
	client, calls, _ := scripted(t, []*http.Response{response(503, `{}`, nil)}, policy)
	var data any
	_, err := client.Get(context.Background(), "/repos/a/b", &data)
	failure(t, err, "http")
	_, err = client.Get(context.Background(), "/repos/a/b", &data)
	failure(t, err, "http")
	if len(*calls) != 1 {
		t.Fatal("ignored transient cooldown")
	}
}
