# OSS Maintainer Insights Design

Status: Proposal for review (updated for the Go collector decision)
Date: 11 October 2026
Proposed repository: `cpanato/oss-maintainer-insights`
Proposed hosting: `https://cpanato.github.io/oss-maintainer-insights/`

## Summary

Build a public, static website that helps people understand how maintenance work is distributed across open source projects. Visitors can inspect any public GitHub repository through a bounded, unauthenticated browser query. Projects added to a reviewed catalog receive scheduled collection and historical analysis through GitHub Actions.

The main questions are who participates in PR reviews, how concentrated that participation is, and how quickly contributors receive a response. Every result includes its observation window, sample size, freshness, and coverage. The product reports observable activity; it does not identify an authoritative maintainer roster or assign a project health score.

This document proposes the implementation. It does not create a repository or deploy a website. The name is provisional; availability under the intended account and branding conflicts have not been verified.

## Naming

| Repository name           | Rationale                                                | Tradeoff                                                                            |
| ------------------------- | -------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| `oss-maintainer-insights` | Recommended. Clearly describes the audience and subject. | Longer name; UI must explain that maintainers are not inferred from activity alone. |
| `oss-maintenance`         | Emphasizes project maintenance rather than individuals.  | Broad and less descriptive of the analytics.                                        |
| `maintainer-lens`         | Short and memorable.                                     | Requires a subtitle to explain the product.                                         |

Display name: **OSS Maintainer Insights**. Suggested description: **Explore review participation, response times, and maintenance trends in public GitHub repositories.**

## Goals and scope

- Run on GitHub Pages without a continuously running backend.
- Let visitors query public repositories without signing in or supplying tokens.
- Publish reproducible historical metrics for a small, reviewed catalog.
- Explain the denominator and coverage of every metric.
- Make adding a project a normal configuration pull request.

The MVP excludes private repositories, global contributor rankings, inferred bus-factor scores, productivity evaluations, vulnerability analysis, and unlimited historical analysis in the browser. It also excludes automatic PR creation and OAuth. Comparing repositories is a follow-up feature after metric definitions and collection are stable.

## Product flow

1. A visitor enters `owner/repo` or a GitHub repository URL.
2. The app normalizes the input and checks the published catalog manifest.
3. For a catalog project, show precomputed data immediately, including the last successful collection time and coverage. An explicit live refresh can request a recent sample.
4. For an uncatalogued project, query GitHub from the browser within a fixed request budget.
5. Show the recent sample and explain any missing data. Offer a link to contribution instructions and the catalog file for historical monitoring.

Main page: repository input, catalog search, and a short methodology summary. Repository page: summary, review participation, response times, coverage panel, and historical charts when available. Use a query parameter such as `?repo=ossf/scorecard` for shareable URLs; this avoids requiring server route rewrites on project Pages hosting.

## Architecture

| Component   | Proposed implementation                             | Responsibility                                                                                            |
| ----------- | --------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| Website     | Astro with TypeScript and small React islands       | Static layout and interactive repository queries                                                          |
| Charts      | Recharts                                            | Review distribution and monthly trends, with equivalent data tables                                       |
| Analysis    | TypeScript browser package and Go collector package | Versioned metric definitions, normalization, and deterministic calculations verified with shared fixtures |
| Collector   | Go CLI                                              | Authenticated collection in Actions and snapshot generation                                               |
| Persistence | Compact JSON in a dedicated `data` branch           | Aggregates, checkpoints, and bounded normalized event records                                             |
| Automation  | GitHub Actions                                      | Validation, collection, static build, and Pages deployment                                                |

Use TypeScript for browser collection and Go for scheduled collection in Actions, as requested by the project owner. Share versioned JSON Schemas, source fixtures, and expected metric results across both implementations. Every methodology change must pass cross-language parity tests before publication.

Browser requests go directly to `api.github.com`. Scheduled requests run only in Actions; credentials never enter the site bundle. Static catalog JSON is served from the same Pages deployment as the website. The source branch contains code and catalog configuration; the data branch holds generated collection output. Build from a specific source SHA and data SHA, recorded in the manifest.

## Live collection and request budget

Default live query: one repository request and one PR list request, then review requests for up to eight eligible PRs. Limit the entire query to 20 GitHub requests, including pagination, comments, and retries. Fetch serially, support cancellation, and stop early when the remaining API allowance is low. This is a product budget, not an API guarantee.

List PRs with `state=all`, `sort=created`, `direction=desc`, and `per_page=30`. Keep human- or bot-authored PRs created within the preceding 90 days, exclude PRs currently marked draft, and choose the newest eight. Do not replace selected PRs because they have no reviews. If the list page contains more eligible PRs, the sample is truncated. If drafts or missing authors consume the page, disclose the resulting smaller sample rather than fetching unbounded pages.

Fetch submitted reviews for each selected PR. Fetch issue comments and inline review comments only while the remaining budget permits. Follow pagination links; a PR with incomplete event pagination cannot contribute to a metric requiring complete event coverage. Show each metric's eligible PR count separately. If review participation cannot be completed for all selected PRs, show the covered subset and mark it partial.

GitHub currently allows 60 unauthenticated REST requests per hour per originating IP, shared with other clients on that IP. Several live queries can exhaust that allowance. Use response rate-limit headers, obey `Retry-After` and reset times, and handle secondary limits. Avoid search endpoints and GraphQL in live mode. Local caching reduces repeated traffic but cannot remove the shared IP limit. [1, 2]

Cache normalized results in localStorage for one hour using repository ID, query policy, schema version, and methodology version. Show collection time; allow clearing the cache. Handle quota failures and unavailable storage. An aborted or incomplete query must never overwrite a previously complete cache entry as if it were complete.

## Metric definitions

All dates use UTC. Monthly windows are half-open intervals: start inclusive, end exclusive. Identify people by GitHub user ID and use login only as a display label. Exclude accounts typed `Bot` and a versioned explicit bot list from human actor counts and review/comment events. Include their authored PRs so human reviews and responses on automation PRs are represented. Unknown automation remains a documented limitation. Exclude deleted or missing actors from actor-based calculations and disclose exclusions.

| Metric                  | Definition                                                                              | Interpretation                                    |
| ----------------------- | --------------------------------------------------------------------------------------- | ------------------------------------------------- |
| PR authors              | Distinct eligible human authors in the selected creation cohort                         | PR participation, not all contributors            |
| Review participants     | Distinct eligible humans with qualifying submitted reviews                              | Observed review activity, not a maintainer roster |
| Review concentration 80 | Smallest number of reviewers whose review units sum to at least 80% of all review units | Distribution of recorded review participation     |
| Largest reviewer share  | Largest actor review-unit count divided by all qualifying review units                  | Useful alongside the concentration count          |
| First human response    | Earliest qualifying response timestamp minus PR creation timestamp                      | Time to an observed external response             |
| Sample PR outcomes      | Open, closed unmerged, or merged status at observation time for cohort PRs              | Sample outcomes, not repository-wide totals       |

A review unit is one unique reviewer–PR pair with at least one submitted review in state `APPROVED`, `CHANGES_REQUESTED`, or `COMMENTED`. Exclude self-reviews and pending or dismissed reviews. Multiple reviews by the same person on the same PR count once. Historical review units belong to the month of the earliest qualifying review in that pair; this deduplication policy is versioned. Concentration is null when there are no units. Explain that two of two reviewers covering 80% differs materially from two of twenty.

A response is a submitted qualifying review, an issue comment, or an inline review comment by a human other than the PR author. Use the earliest observed eligible timestamp after creation. This measures first human response, not first maintainer response. Require all three event streams to be fully paginated before admitting a PR to this metric. Exclude PRs currently marked draft; time previously spent in draft cannot be recovered reliably in this MVP and may inflate durations.

Show the median for responded PRs together with the number of eligible PRs, the response count, and the number still unanswered. Unanswered PRs have no duration; never treat them as zero or silently omit their count. Small samples below five observations show raw counts and an insufficient-data label rather than a headline median or concentration indicator. This is a display policy, not a statistical confidence threshold.

Historical PR creation cohorts measure author participation, outcomes, and response. Historical review windows measure submitted review activity, including reviews on PRs created before the window. These populations differ and must be labeled separately. Do not reuse creation-cohort samples as complete monthly review activity.

Repository `open_issues_count` includes pull requests; never label it as an issues-only count. Stars and forks may appear as context, not sustainability indicators. Commit counts and last repository update time are not maintenance proxies.

## Scheduled catalog collection

Initial target: three public projects, for example `ossf/scorecard`, `sigstore/cosign`, and `cpanato/github-actions-dashboard`. These are proposed seed entries, not claimed endorsements. Start with a 90-day backfill and daily updates. Extend to 12 months only after measuring actual request costs and storage.

The collector uses a workflow credential to read public endpoints. Start by testing public cross-repository access with `GITHUB_TOKEN`; its granted repository permissions are scoped to the workflow repository. Its ordinary REST allowance is currently 1,000 requests per hour per workflow repository, not a separate budget for each catalog target. If cross-repository endpoint access or scale requires another credential, document a GitHub App or minimally privileged token as an explicit operational change. [1, 7]

For a backfill, enumerate every PR, including older PRs that may have received reviews during the target window. Fetch event streams and retain only the normalized records needed for calculations. Exhaustive review history can be expensive because submitted reviews are fetched per PR. A bounded backfill is acceptable only if marked partial; a request cap cannot imply complete historical coverage. Small projects should reach complete coverage first; large projects may remain pending or unsupported until a measured strategy is available.

Daily updates scan recently updated PRs with an overlap, refresh open tracked PRs, and periodically reconcile older PRs for late reviews and edits. Deduplicate by source event ID and actor–PR pairs. Upsert records; recalculate affected months when source state changes. A checkpoint advances only after all required pages are fetched and validated. A failed batch retains its prior checkpoint for safe retries. Deleted events and stale update discovery mean periodic reconciliation remains necessary.

Proposed initial run cap: 700 API requests and 30 minutes across the catalog. These are operational starting values to tune, not capacity claims. Queue unfinished projects for later runs; record backlog and coverage. Begin with serialized API requests and bounded retries. Preserve the last successful published dataset on collection failure and expose stale status.

Persist compact normalized identifiers, actors, timestamps, and event types only. Discard comment bodies, PR bodies, diffs, and email addresses. Publish aggregates and coverage; keep normalized intermediate records out of the Pages build. A public data branch remains publicly readable, so intermediate records must also be suitable for public redistribution. Avoid retaining full API payloads in Git history.

## Data contract

Use versioned JSON Schema and validate every output before publishing. A manifest maps stable repository IDs to their current owner/name and dataset paths. Record schema version, methodology version, collection mode, source revision, last successful collection, requested period, actual coverage, exclusions, and failure reasons.

Illustrative metric object:

```json
{
  "schema_version": 1,
  "methodology_version": "1.0.0",
  "repository": { "id": 123, "full_name": "example/project" },
  "mode": "live_sample",
  "collected_at": "2026-10-11T17:00:00Z",
  "cohort": { "selection": "newest_eligible_prs", "max_prs": 8 },
  "metrics": {
    "review_concentration_80": {
      "value": null,
      "status": "insufficient_data",
      "review_units": 0,
      "covered_prs": 8,
      "pagination_complete": true
    }
  }
}
```

Coverage statuses: `complete_for_sample`, `complete_for_window`, `partial`, `insufficient_data`, and `unavailable`. An empty successful result is distinct from a failed request. Historical completeness requires finished enumeration and required event pagination, with the latest reconciliation timestamp disclosed. Methodology changes require recomputation or a visible break in historical series.

## Catalog contributions

`catalog/repositories.json` contains objects with canonical `owner/repo` and optional explicit bot exclusions. A PR template requests the repository, a brief reason to monitor it, and acknowledgement that collection may be deferred for capacity.

PR CI validates syntax, duplicates, canonical repository resolution, public visibility, schema, and configuration size. Network checks can be inconclusive when rate limited and must support retry. CI must not execute code from submitted target repositories. Use ordinary `pull_request` checks without secrets; do not use `pull_request_target` to execute untrusted PR content.

After review and merge, the next scheduled or manual run collects data. The site shows pending status until a valid dataset exists. The UI links to contribution instructions rather than promising that opening a PR automatically adds a project.

## Deployment and operations

The collection workflow writes validated output to the data branch, then builds and deploys the site within the same workflow or through an explicitly invoked reusable workflow. Do not depend on a bot push triggering a second workflow. Source changes run a separate build using the latest validated data revision.

Separate read-only collection, narrowly scoped data writing, and Pages deployment permissions. Grant deployment jobs `pages:write` and `id-token:write` as required by the Pages deployment setup. Pin third-party actions to reviewed commit SHAs. Never expose tokens in JSON, logs, browser code, or build artifacts.

Use `/oss-maintainer-insights/` as the Astro base path; test asset and JSON paths under that prefix. Publish a complete artifact atomically with its manifest. Keep the deployment reproducible from source and data revisions. Pages currently limits published sites to 1 GB; storage and workflow limits must be measured before increasing catalog size. Free infrastructure is a goal, not an unlimited capacity promise. [6]

Repository names and API content are untrusted inputs. Parse supported GitHub URLs, validate owner/repo, restrict fetches to GitHub API and same-origin datasets, encode URL segments, and render strings as text. Support repository renames by stable ID; distinguish an unavailable or private repository from an empty one.

## User experience and accessibility

Show partial results as they arrive, with a cancel control and explicit collection status. Provide readable error states for invalid input, 404, restricted access, timeout, API limits, and stale catalog data. Preserve useful completed metrics if another metric fails.

Every chart has a table alternative and keyboard-accessible controls. Never communicate status using color alone. Display clear labels such as “Recent sample of 8 PRs” and “Historical coverage incomplete.” Avoid red or green project health badges and cross-project rankings. Comparison later requires matching methodology, time window, population, and coverage.

## Validation and acceptance criteria

- Fixture tests cover deduplication, review states, self-responses, bot exclusions, actor renames, zero denominators, UTC month boundaries, and unanswered PRs.
- Collection tests simulate pagination, interrupted checkpoints, 403/429 backoff, deleted actors, partial event streams, and late events on old PRs.
- Browser tests verify direct API CORS behavior and exposed pagination/rate-limit headers from the deployed origin, the 20-request cap, cancellation, cache failures, and Pages base paths.
- End-to-end checks cover one tiny project, one moderately active project, and a high-volume project that exceeds the budget.
- A manual audit traces one published project's figures back to source PRs and review events.
- The build contains no credentials or raw comment bodies; all published JSON passes schema validation.

MVP is complete when visitors can inspect a public repository without auth, see honest coverage and errors, browse at least three catalog projects, and follow a working contribution path. A failed scheduled collection must preserve the last successful data and visibly mark its age. Full 12-month coverage is not an MVP acceptance requirement.

## Delivery sequence

1. Agree on the provisional name and metric definitions; create schemas, Go/TypeScript analysis engines, shared fixtures, and PR CI.
2. Build the static shell and bounded live queries; validate API behavior on Pages early.
3. Add catalog configuration, the collector, checkpoint persistence, and one small project.
4. Add daily Go collection, reproducible Pages deployment, and the proposed seed catalog.
5. Measure cost and coverage before adding long backfills, more projects, or comparisons.

## Alternatives and unresolved decisions

A catalog-only site would improve consistency and reduce API errors but would remove arbitrary repository lookup. A browser-only site would be simpler but cannot provide dependable history. A backend or GitHub App service could improve throughput but adds operational scope. The hybrid approach preserves immediate discovery and scheduled historical analysis.

Before implementation, settle the final name, license, and initial catalog. Before expanding, measure backfill request cost, decide a catalog admission budget, and validate any alternative credential. Contributor return rates, merge concentration, issue triage, and survival-analysis response statistics are candidates for later designs.

This proposal makes no claim of being the first maintenance analytics tool. A competitor review remains separate work; the intended distinction is transparent, bounded live analysis combined with a community-maintained historical catalog.

## References

Official GitHub documentation checked on 11 October 2026. Policies and limits should be rechecked during implementation.

1. [REST API rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)
2. [REST API best practices](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api)
3. [Pull request endpoints](https://docs.github.com/en/rest/pulls/pulls)
4. [Submitted review endpoints](https://docs.github.com/en/rest/pulls/reviews)
5. [Issue comment endpoints](https://docs.github.com/en/rest/issues/comments) and [inline review comment endpoints](https://docs.github.com/en/rest/pulls/comments)
6. [GitHub Pages limits](https://docs.github.com/en/pages/getting-started-with-github-pages/github-pages-limits)
7. [GITHUB_TOKEN scope](https://docs.github.com/en/actions/concepts/security/github_token) and [workflow authentication](https://docs.github.com/actions/reference/authentication-in-a-workflow)
