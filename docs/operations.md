# Operations and feasibility findings

## Current status

Milestone 2 adds native TypeScript/browser and Go REST clients, a Go feasibility probe, a browser diagnostic, and simulated transport tests. No new external library is required. Historical ingestion, checkpoint persistence, live sample selection, and the website remain later milestones.

The real measurements below establish unauthenticated public endpoint access and bounded cost. They do **not** establish authenticated cross-repository access with an Actions `GITHUB_TOKEN` or CORS from the deployed Pages origin. Those gates remain pending.

## Request policy

A browser client owns a query's entire budget, capped at twenty explicit REST GET attempts. All pages, redirects, and retries count. Requests are serialized. The default per-request timeout is ten seconds; at most two retries are allowed for rate limits or transient HTTP 500/502/503/504 errors. Failed transport requests and invalid payloads stop immediately. A client instance is not shared between unrelated queries.

Both clients honor `Retry-After` seconds or HTTP dates and primary reset timestamps. A secondary limit without those hints requires at least a minute before retrying, with exponential backoff. The default maximum in-query wait is two seconds: longer cooldowns return a rate-limit failure with a suggested retry time, rather than waiting a minute in the browser. Retain that cooldown for later requests on the same instance. Low remaining allowance also stops further requests. A normal permission-denied 403 is not retried as a rate limit; 404 does not prove that a repository is absent rather than private/inaccessible.

Use only repository REST endpoints on `https://api.github.com`, encode repository components, and follow `Link` URLs rather than inventing page numbers. Reject external redirect/pagination targets and pagination cycles. Browser-managed CORS preflights are not explicit GET attempts and are not visible to this JavaScript counter. Browser requests omit credentials; the Go client accepts a token only through its options, passed from the CLI's `GITHUB_TOKEN` environment variable. Tokens and raw error payloads are never returned in client errors or probe reports.

Manual redirect handling prevents hidden HTTP attempts from escaping the budget. Browser Fetch exposes cross-origin redirects as `opaqueredirect` without readable `Location`; return an explicit redirect failure and use a canonical name or a previously resolved stable repository ID. Go can follow validated same-origin redirects, counting every hop. Do not enable automatic redirects to make renamed inputs appear to work while undercounting requests.

Pagination returns finished pages and `complete: false` with the stop reason if a later request, validation, or pagination link fails. Validate an entire page before committing its records. Successful empty arrays are complete results; malformed arrays, missing metadata, and failed requests are not empty successes. Data acquisition must not admit partial streams into metrics requiring complete coverage.

## Go probe

```sh
go run ./cmd/collector probe --requests=10 ossf/scorecard sigstore/cosign cpanato/github-actions-dashboard
```

This outputs JSON Lines: one report per repository, with at most ten requests per repository by default. Accept one to three targets and a cap from zero to one hundred; enforce a two-minute overall deadline. A zero budget produces a stopped report without network traffic. Private repositories are rejected even when an environment credential could access them.

Enumerate PRs oldest first, including PRs created before the requested ninety-day window. After the first page, sample all three event streams of the first eligible PR, then continue enumeration. Only retain counters, repository identity, the sampled PR number, completeness, rate-limit metadata, and failure reasons. The probe does not persist full API payloads, account details, bodies, or tokens.

The backfill request lower bound counts one metadata request, successfully enumerated list pages, one review-stream request per observed eligible PR, and two additional comment-stream requests per observed eligible creation-cohort PR. It assumes only one page per event stream and no retries. Bot exclusions use account type `Bot` for this experiment; custom bot-list policy is not applied. This is a lower bound under that policy on the observed subset, not a full-project estimate or a capacity guarantee. Sampled PRs and events are not historical coverage of the project.

A completed command can still report failed endpoints or incomplete enumeration. Inspect `failure_reason`, `enumeration_complete`, and each sample stream's `complete` flag. The collector must later implement resumable batches and reconciliation; this probe is not a checkpoint implementation.

## Measured public endpoints — 12 October 2026

Local Go run, without `GITHUB_TOKEN`, observed at **09:02:07 UTC**:

| Repository                         | HTTP attempts | Completed list pages | Listed PRs | Eligible review PRs seen | Eligible ninety-day cohort PRs seen | Enumeration             | Backfill request lower bound |
| ---------------------------------- | ------------- | -------------------- | ---------- | ------------------------ | ----------------------------------- | ----------------------- | ---------------------------- |
| `ossf/scorecard`                   | 10            | 6                    | 600        | 488                      | 0                                   | Partial: budget reached | 495                          |
| `sigstore/cosign`                  | 10            | 6                    | 600        | 550                      | 0                                   | Partial: budget reached | 557                          |
| `cpanato/github-actions-dashboard` | 6             | 2                    | 141        | 23                       | 23                                  | Complete enumeration    | 72                           |

All three sampled PRs' review, issue-comment, and inline-comment streams finished. The samples were PR #1 in scorecard, PR #3 in cosign, and PR #115 in github-actions-dashboard. The first two PRs predate the ninety-day window. Completed enumeration for github-actions-dashboard does **not** mean its full historical event coverage was collected: only one PR's streams were sampled.

The observed-subset lower bounds sum to **1,124 requests**, already above the proposed 700-request catalog run cap. The larger projects' enumeration is still incomplete. Therefore a complete initial backfill cannot be assumed to fit in one run; pilot the smaller project first and plan resumable queued work for the rest. Later events, pagination, custom exclusions, and reconciliation will change actual cost. Unobserved creation cohorts in the first two repositories are not empty cohorts: the oldest-first bounded scan has not reached recent PRs.

## Browser diagnostic

Build packages, serve the repository root, and open `/diagnostics/github.html`. Its native module imports `packages/github/dist/index.js`. The form accepts a repository name or supported GitHub repository URL, provides cancellation, renders text rather than HTML from API values, and shows the current origin and visible headers. It reads repository metadata and one PR-list page with `per_page=1` using a two-request cap. Use a project with at least two PRs to confirm pagination headers.

A real headless Chrome run from a local HTTP origin on **12 October 2026 at 09:03:56 UTC** completed both requests against `ossf/scorecard`. JavaScript could read `Link`, `x-ratelimit-limit`, `x-ratelimit-remaining`, and `x-ratelimit-reset`. The visible limit was 60; remaining allowance changed from 24 to 23. `Retry-After` was absent on these successful responses, so this run does not demonstrate its CORS exposure on a throttled response. Preflight policy and browser CORS worked for the headers actually used by the client.

This is evidence for the **local origin only**. Do not claim deployed-origin validation until the diagnostic is served from `https://cpanato.github.io/oss-maintainer-insights/` and repeated there. Include the diagnostic HTML/JS and built GitHub client at the corresponding relative paths in the first Pages artifact. Preserve the project base prefix.

## Actions credential gate

`.github/workflows/api-probe.yml` deliberately has a manual trigger, empty workflow-level permissions, and only `contents: read` on its job. Fixed public targets and a ten-request cap yield at most thirty HTTP attempts. The Go step receives `github.token` through `GITHUB_TOKEN`, and publishes only the sanitized JSON Lines artifact.

The workflow must exist on `main` before its dispatch can be registered. After merging this milestone, run **GitHub API feasibility probe**, download `github-api-probe`, and record endpoint access, response status/failure reasons, observed allowance, and costs here. Until that evidence exists, cross-repository access with `GITHUB_TOKEN` remains unverified. Do not substitute the local GitHub CLI credential or a PAT to claim this check passed. Any credential change is a separate operational decision.

## GitHub App option

The project owner is open to evaluating a GitHub App if access or request allowance limits the collector. This is an option, not an adopted credential or permission grant.

GitHub documents a minimum of 5,000 REST requests/hour for an installation access token, compared with the ordinary 1,000 requests/hour per workflow repository for `GITHUB_TOKEN`. An App's allowance belongs to its installation; do not treat every catalog repository as a separate budget. Secondary limits still apply, and a larger API allowance does not automatically change the product's 700-request run cap.

First verify the workflow token using the probe, then measure pilot backfill and daily reconciliation. If access fails or queued work cannot meet the chosen freshness target, compare a resumable multi-run strategy with an App-backed collector. Before adopting an App, confirm public cross-repository endpoint access, required read permissions, installation scope, short-lived token generation, and private-key storage in Actions secrets. The browser remains unauthenticated; never ship an App key or token in a static artifact. App registration and installation require a separate operational decision with the owner.

## Remaining gates

- Execute the Go probe in Actions with the workflow token and review the artifact.
- Repeat browser diagnostics from the deployed Pages origin, including pagination and visible rate-limit metadata.
- Observe a real throttled response's visible headers if available; simulated tests verify required backoff, but do not manufacture API load to trigger throttling.
- Measure full pilot ingestion and storage before extending coverage, catalog size, or a twelve-month backfill.

## References

Official documentation rechecked on 12 October 2026:

- [REST best practices and rate-limit backoff](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api)
- [REST pagination](https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api)
- [REST rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)
- [CORS and exposed response headers](https://docs.github.com/en/rest/using-the-rest-api/using-cors-and-jsonp-to-make-cross-origin-requests)
