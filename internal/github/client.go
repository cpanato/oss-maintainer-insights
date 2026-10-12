// Package github implements bounded, read-only GitHub REST requests.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Origin = "https://api.github.com"
const APIVersion = "2022-11-28"
const maxBodyBytes = 8 * 1024 * 1024

type Failure struct {
	Reason  string
	Status  int
	RetryAt time.Time
}

func (e *Failure) Error() string {
	return fmt.Sprintf("GitHub request stopped: %s (HTTP %d)", e.Reason, e.Status)
}

type RateLimit struct {
	Remaining *int       `json:"remaining"`
	Limit     *int       `json:"limit"`
	ResetAt   *time.Time `json:"reset_at"`
}
type Policy struct {
	MaxRequests      int
	Timeout          time.Duration
	MaxRetries       int
	MaxWait          time.Duration
	MinimumRemaining int
}

func DefaultPolicy() Policy {
	return Policy{MaxRequests: 30, Timeout: 10 * time.Second, MaxRetries: 2, MaxWait: 2 * time.Second, MinimumRemaining: 2}
}

type Options struct {
	Policy Policy
	Token  string
	HTTP   *http.Client
	Now    func() time.Time
	Sleep  func(context.Context, time.Duration) error
}
type Client struct {
	policy        Policy
	token         string
	http          *http.Client
	now           func() time.Time
	sleep         func(context.Context, time.Duration) error
	gate          chan struct{}
	used          int
	rate          RateLimit
	blockedUntil  time.Time
	blockedReason string
}

func New(options Options) (*Client, error) {
	p := options.Policy
	if p.MaxRequests < 0 || p.Timeout <= 0 || p.MaxRetries < 0 || p.MaxRetries > 5 || p.MaxWait < 0 || p.MinimumRemaining < 0 || strings.ContainsAny(options.Token, "\r\n") {
		return nil, errors.New("invalid GitHub client policy")
	}
	transport := http.DefaultClient
	if options.HTTP != nil {
		transport = options.HTTP
	}
	httpClient := *transport
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	now := options.Now
	if now == nil {
		now = time.Now
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = wait
	}
	return &Client{policy: p, token: options.Token, http: &httpClient, now: now, sleep: sleep, gate: make(chan struct{}, 1)}, nil
}
func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (c *Client) RequestsUsed() int { c.gate <- struct{}{}; defer func() { <-c.gate }(); return c.used }
func (c *Client) RateLimit() RateLimit {
	c.gate <- struct{}{}
	defer func() { <-c.gate }()
	snapshot := c.rate
	if snapshot.Remaining != nil {
		value := *snapshot.Remaining
		snapshot.Remaining = &value
	}
	if snapshot.Limit != nil {
		value := *snapshot.Limit
		snapshot.Limit = &value
	}
	if snapshot.ResetAt != nil {
		value := *snapshot.ResetAt
		snapshot.ResetAt = &value
	}
	return snapshot
}

var repository = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?/[a-zA-Z0-9_.-]{1,100}$`)

func RepositoryPath(value string) (string, error) {
	if !repository.MatchString(value) {
		return "", errors.New("repository must be owner/repo")
	}
	parts := strings.Split(value, "/")
	if parts[1] == "." || parts[1] == ".." {
		return "", errors.New("invalid repository name")
	}
	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), nil
}
func target(value string) (*url.URL, error) {
	base, _ := url.Parse(Origin)
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, &Failure{Reason: "invalid_input"}
	}
	u := base.ResolveReference(parsed)
	if u.Scheme != "https" || u.Host != "api.github.com" || u.User != nil || u.Fragment != "" || (!strings.HasPrefix(u.Path, "/repos/") && !strings.HasPrefix(u.Path, "/repositories/")) {
		return nil, &Failure{Reason: "invalid_input"}
	}
	return u, nil
}
func number(headers http.Header, key string) *int {
	raw := headers.Get(key)
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return nil
	}
	return &value
}
func (c *Client) setRate(headers http.Header) {
	c.rate = RateLimit{Remaining: number(headers, "X-RateLimit-Remaining"), Limit: number(headers, "X-RateLimit-Limit")}
	if epoch := number(headers, "X-RateLimit-Reset"); epoch != nil {
		reset := time.Unix(int64(*epoch), 0).UTC()
		c.rate.ResetAt = &reset
	}
}

// Get counts each HTTP attempt, including same-origin redirects and retries.
// It never follows redirects automatically or sends credentials outside api.github.com.
func (c *Client) Get(ctx context.Context, path string, destination any) (http.Header, error) {
	select {
	case <-ctx.Done():
		return nil, &Failure{Reason: "cancelled"}
	case c.gate <- struct{}{}:
	}
	defer func() { <-c.gate }()
	u, err := target(path)
	if err != nil {
		return nil, err
	}
	redirects := map[string]bool{}
	retries := 0
	for {
		if ctx.Err() != nil {
			return nil, &Failure{Reason: "cancelled"}
		}
		if c.used >= c.policy.MaxRequests {
			return nil, &Failure{Reason: "budget"}
		}
		if delay := c.blockedUntil.Sub(c.now()); delay > 0 {
			if delay > c.policy.MaxWait {
				return nil, &Failure{Reason: c.blockedReason, RetryAt: c.blockedUntil}
			}
			if err := c.sleep(ctx, delay); err != nil {
				return nil, &Failure{Reason: "cancelled"}
			}
		}
		if c.rate.Remaining != nil && *c.rate.Remaining <= c.policy.MinimumRemaining {
			if c.rate.ResetAt != nil && !c.now().Before(*c.rate.ResetAt) {
				c.rate = RateLimit{}
			} else {
				retry := time.Time{}
				if c.rate.ResetAt != nil {
					retry = *c.rate.ResetAt
				}
				return nil, &Failure{Reason: "rate_limit", RetryAt: retry}
			}
		}
		requestCtx, cancel := context.WithTimeout(ctx, c.policy.Timeout)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, u.String(), nil)
		if err != nil {
			cancel()
			return nil, &Failure{Reason: "invalid_input"}
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", APIVersion)
		request.Header.Set("User-Agent", "oss-maintainer-insights/0.1.0")
		if c.token != "" {
			request.Header.Set("Authorization", "Bearer "+c.token)
		}
		c.used++
		response, err := c.http.Do(request)
		if err != nil {
			timedOut := requestCtx.Err() == context.DeadlineExceeded
			cancel()
			reason := "network"
			if ctx.Err() != nil {
				reason = "cancelled"
			} else if timedOut {
				reason = "timeout"
			}
			return nil, &Failure{Reason: reason}
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
		closeErr := response.Body.Close()
		timedOut := requestCtx.Err() == context.DeadlineExceeded
		cancel()
		if ctx.Err() != nil {
			return nil, &Failure{Reason: "cancelled"}
		}
		if timedOut {
			return nil, &Failure{Reason: "timeout"}
		}
		if readErr != nil || closeErr != nil {
			return nil, &Failure{Reason: "network"}
		}
		if len(data) > maxBodyBytes {
			return nil, &Failure{Reason: "invalid_response"}
		}
		c.setRate(response.Header)
		status := response.StatusCode
		if status == 301 || status == 302 || status == 303 || status == 307 || status == 308 {
			location := response.Header.Get("Location")
			if location == "" || redirects[u.String()] {
				return nil, &Failure{Reason: "redirect", Status: status}
			}
			redirects[u.String()] = true
			locationURL, err := url.Parse(location)
			if err != nil {
				return nil, &Failure{Reason: "redirect", Status: status}
			}
			u, err = target(u.ResolveReference(locationURL).String())
			if err != nil {
				return nil, err
			}
			continue
		}
		if status == http.StatusOK {
			if err := json.Unmarshal(data, destination); err != nil {
				return nil, &Failure{Reason: "invalid_response", Status: status}
			}
			return response.Header.Clone(), nil
		}
		var message struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &message)
		lower := strings.ToLower(message.Message)
		retryAfter := response.Header.Get("Retry-After")
		limited := status == 429 || (status == 403 && (retryAfter != "" || (c.rate.Remaining != nil && *c.rate.Remaining == 0) || strings.Contains(lower, "rate limit") || strings.Contains(lower, "abuse detection")))
		if limited {
			c.blockedReason = "rate_limit"
			deadline := c.now().Add(time.Minute * time.Duration(1<<retries))
			if seconds, err := strconv.ParseFloat(retryAfter, 64); err == nil && seconds >= 0 && seconds <= 86400 {
				deadline = c.now().Add(time.Duration(seconds * float64(time.Second)))
			} else if date, err := http.ParseTime(retryAfter); err == nil {
				deadline = date
			} else if c.rate.Remaining != nil && *c.rate.Remaining == 0 && c.rate.ResetAt != nil {
				deadline = *c.rate.ResetAt
			}
			c.blockedUntil = deadline
			if minimum := c.now().Add(time.Second); c.blockedUntil.Before(minimum) {
				c.blockedUntil = minimum
			}
			if c.rate.Remaining != nil && *c.rate.Remaining == 0 && c.rate.ResetAt != nil && c.blockedUntil.Before(*c.rate.ResetAt) {
				c.blockedUntil = *c.rate.ResetAt
			}
			if retries >= c.policy.MaxRetries || c.blockedUntil.Sub(c.now()) > c.policy.MaxWait {
				return nil, &Failure{Reason: "rate_limit", Status: status, RetryAt: c.blockedUntil}
			}
		} else if (status == 500 || status == 502 || status == 503 || status == 504) && retries < c.policy.MaxRetries {
			c.blockedReason = "http"
			c.blockedUntil = c.now().Add(time.Second * time.Duration(1<<retries))
			if c.blockedUntil.Sub(c.now()) > c.policy.MaxWait {
				return nil, &Failure{Reason: "http", Status: status}
			}
		} else {
			reason := "http"
			switch status {
			case 403:
				reason = "forbidden"
			case 404:
				reason = "not_found"
			}
			return nil, &Failure{Reason: reason, Status: status}
		}
		retries++
	}
}

var links = regexp.MustCompile(`<([^>]+)>([^<]*)`)
var relation = regexp.MustCompile(`;\s*rel\s*=\s*(?:"([^"]+)"|([^;\s,]+))`)

func NextPage(header string) (string, error) {
	if header == "" {
		return "", nil
	}
	matches := links.FindAllStringSubmatch(header, -1)
	if len(matches) == 0 {
		return "", &Failure{Reason: "invalid_response"}
	}
	next := ""
	for _, match := range matches {
		rel := relation.FindStringSubmatch(match[2])
		if rel == nil {
			return "", &Failure{Reason: "invalid_response"}
		}
		for _, value := range strings.Fields(rel[1] + rel[2]) {
			if value == "next" {
				if next != "" {
					return "", &Failure{Reason: "invalid_response"}
				}
				next = match[1]
			}
		}
	}
	if next != "" {
		if _, err := target(next); err != nil {
			return "", err
		}
	}
	return next, nil
}
