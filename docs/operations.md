# Operations and feasibility findings

## Current status

Milestone 2 adds native TypeScript/browser and Go REST clients, a Go feasibility probe, a browser diagnostic, and simulated transport tests. No new external library is required. Historical ingestion, checkpoint persistence, live sample selection, and the website remain later milestones.

The real measurements below establish unauthenticated public endpoint access, bounded cost, and authenticated public cross-repository access with the Actions `GITHUB_TOKEN`. CORS from the deployed Pages origin remains unverified.

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

This is evidence for the **local origin only**. Do not claim deployed-origin validation until the diagnostic is served from `https://maintainerinsights.dev/` and repeated there. Include the diagnostic HTML/JS and built GitHub client at the corresponding relative paths in the first Pages artifact. Serve assets and navigation from the custom domain root.

## Actions credential gate

`.github/workflows/api-probe.yml` deliberately has a manual trigger, empty workflow-level permissions, and only `contents: read` on its job. Fixed public targets and a ten-request cap yield at most thirty HTTP attempts. The Go step receives `github.token` through `GITHUB_TOKEN`, and publishes only the sanitized JSON Lines artifact.

The workflow must exist on `main` before its dispatch can be registered. Run **GitHub API feasibility probe** and inspect `github-api-probe` when rechecking access or capacity. Do not substitute a local GitHub CLI credential or a PAT to claim the workflow-token check passed. Any credential change is a separate operational decision.

### Verified Actions run — 12 October 2026

[Run 38230040569](https://github.com/cpanato/oss-maintainer-insights/actions/runs/38230040569), source revision `29bc6d039111000705b0900bc47b58f8764757a9`, completed successfully. Reports were observed at **09:28:32 UTC**. The checked-in workflow supplied `github.token` through `GITHUB_TOKEN`; every report recorded `authenticated: true`.

| Repository                         | Requests | Reported API limit | Remaining allowance at last response | Enumeration                     | Sampled event streams |
| ---------------------------------- | -------- | ------------------ | ------------------------------------ | ------------------------------- | --------------------- |
| `ossf/scorecard`                   | 10       | 5,000              | 4,972                                | Partial: product budget reached | All three complete    |
| `sigstore/cosign`                  | 10       | 5,000              | 4,956                                | Partial: product budget reached | All three complete    |
| `cpanato/github-actions-dashboard` | 6        | 5,000              | 4,950                                | Complete PR enumeration         | All three complete    |

Repository metadata, PR-list pagination, submitted reviews, issue comments, and inline review comments were accessible for all three targets. There were no recorded permission or API rate-limit failures. The two `budget` failures are the configured ten-request cap, not exhausted GitHub allowance. Listed-PR counts and backfill lower bounds matched the local measurements: 600/495, 600/557, and 141/72 respectively.

The headers reported a 5,000-request limit and the same reset timestamp, **10:19:30 UTC**, for these public endpoint calls. This is observed behavior in this execution, not a universal guarantee or a separate 5,000-request allocation per catalog target. GitHub's published ordinary `GITHUB_TOKEN` limit is 1,000 per workflow repository; retain that documented baseline and consult live headers for the actual context. Remaining-allowance differences need not equal this probe's counters because other requests can share the allowance.

This closes the Actions credential-access gate for the tested public endpoints. It does not establish complete historical event coverage, full backfill capacity, or browser CORS from Pages. The sanitized artifact is retained for seven days; this source revision, run link, and summary preserve the operational evidence after artifact expiration.

## GitHub App option

The project owner is open to evaluating a GitHub App if access or request allowance limits the collector. This is an option, not an adopted credential or permission grant.

GitHub documents a minimum of 5,000 REST requests/hour for an installation access token, compared with the ordinary 1,000 requests/hour per workflow repository for `GITHUB_TOKEN`. An App's allowance belongs to its installation; do not treat every catalog repository as a separate budget. Secondary limits still apply, and a larger API allowance does not automatically change the product's configured run cap.

The probe confirmed workflow-token access for all three targets and reported a 5,000-request limit in this run. Continue with that credential for now, and measure pilot backfill and daily reconciliation before introducing an App. If access fails or queued work cannot meet the chosen freshness target, compare a resumable multi-run strategy with an App-backed collector. Before adopting an App, confirm public cross-repository endpoint access, required read permissions, installation scope, short-lived token generation, and private-key storage in Actions secrets. The browser remains unauthenticated; never ship an App key or token in a static artifact. App registration and installation require a separate operational decision with the owner.

## Remaining gates

- Repeat browser diagnostics from the deployed Pages origin, including pagination and visible rate-limit metadata.
- Observe a real throttled response's visible headers if available; simulated tests verify required backoff, but do not manufacture API load to trigger throttling.
- Measure full pilot ingestion and storage before extending coverage, catalog size, or a twelve-month backfill.

## References

Official documentation rechecked on 12 October 2026:

- [REST best practices and rate-limit backoff](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api)
- [REST pagination](https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api)
- [REST rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)
- [CORS and exposed response headers](https://docs.github.com/en/rest/using-the-rest-api/using-cors-and-jsonp-to-make-cross-origin-requests)

## Local live interface

The Astro site serves bounded live queries at `https://maintainerinsights.dev/` with a shareable `?repo=owner/repository` parameter. It uses the same unauthenticated browser transport measured above. One query selects up to eight eligible PRs from the newest 30-record page within 90 days, freezes its observation time, and shares a 20-attempt budget across metadata, listing, pagination, and retries. It reads all selected review streams before spending the remaining budget on issue and inline comments.

For an eight-PR sample, even one page per stream needs 26 requests (metadata, listing, and 24 stream pages), so partial response coverage under the 20-request cap is expected. This limit is intentional: under methodology 1.0.0, GitHub-only review participation could have complete coverage while first response covered fewer PRs. Methodology 1.1.0 requires complete review and both comment streams for combined participation as well. Missing streams are never interpreted as empty streams or unanswered PRs. A complete sample of up to six PRs fits when each stream needs one page and there are no retries.

The local cache accepts only complete, schema-validated normalized inputs and recalculates metrics on read. Entries expire after one hour and include repository ID, query-policy, schema, and methodology versions. Names are aliases; refresh resolves metadata again. Partial results remain visible but are not cached. Storage denial and quota exhaustion do not prevent live queries. Browser state and API-derived labels render as text.

The UI uses Astro with a small TypeScript controller. React islands and a chart library are deferred until historical charts justify them; current review bars have an equivalent HTML table. The project owner selected Playwright for deterministic browser checks. Local preview evidence does not replace the deployed Pages-origin gate above. The first default-branch Pages deployment is recorded below.

## Saved samples, conditional refresh, and Pages

The reviewed catalog in `catalog/repositories.json` contains OpenSSF, CNCF, and community examples. The UI first serves their pre-generated aggregate JSON from its own origin. No GitHub request occurs for a successfully loaded saved sample, even if it is old; its observation time and age remain visible. Missing saved data on a shareable catalog URL does not automatically fall back to the API. An explicit form submission or **Refresh live** can request a live sample. Repositories outside the catalog use live queries and offer the contribution guide for proposing future historical tracking.

`go run ./cmd/collector snapshots` accepts at most 20 entries and divides a 9,000-attempt run cap fairly, within a 90-minute deadline. Each project receives at most 1,300 attempts; the current ten entries receive 900 attempts each (9,000 maximum total). The default 24-hour freshness check skips network access for recent saved samples. `--force` revalidates regardless of age. Conditional requests count against the product attempt cap even when GitHub returns 304; the implementation does not assume a quota exemption to increase its budget.

Sanitized page checkpoints retain only typed identifiers, actors, timestamps, states, pagination links, and ETags. A 304 reuses those records; changed pages replace them after validation and idempotent upserts. Selected reviews and both comment streams are revalidated, including PRs created before the latest run, so late events are not ignored simply because no new PR appeared. These are conditional sample checkpoints, not a completed historical backfill or a resumable monthly event queue. A page-heavy sample may remain partial under the cap.

A failed collection preserves earlier output. Within the same selection policy and methodology, a partial update cannot replace an earlier complete sample. A selection-policy or methodology change triggers revalidation even when the old sample is fresh; a successful result under the new policy replaces the old one, while failures preserve the earlier output. `index.json` records the latest attempt and each repository's updated, fresh, preserved, or unavailable state. Freshness and coverage are separate: serving saved JSON does not imply complete data.

Saved generation uses at most 1,300 attempts per project, divided fairly from a 9,000-attempt execution cap (900 each for ten entries). These bound request attempts, not the number of PRs in the cohort. Enumeration uses 100-record PR pages and stops at the frozen 90-day creation boundary. A 1,200-PR cohort needs at least 12 listing pages and 3,600 event-stream pages, so it intentionally spans executions. Browser queries retain their 20-attempt cap. Pagination, retries, and redirects consume the same attempt budget.

`snapshots.yml` runs hourly and on source/catalog changes on `main`; manual refresh intentionally forces revalidation. PR CI stays offline and does not write data or deploy Pages. Scheduled/source runs skip fresh completed cohorts while resuming pending collection.

The restore step records the exact data revision actually loaded. Publication checks both that source is still current and that the data branch has not advanced, then pushes with an exact `--force-with-lease` against the restored revision. An owner-merged data PR or concurrent writer wins over an older collection, including a race between the ref check and push. Source/data changes cause a safe skipped publication; unrelated push/network failures still fail the job. Cold branch creation uses an empty lease, and unchanged reruns reuse their commit. Public snapshots and sanitized checkpoints are committed together; only public data enters the site.

Immediately before requesting the Pages deployment, the workflow rechecks that the artifact's source/data revisions still match both branches. Superseded artifacts are skipped. Ref checks and authenticated Git pushes use Git transport without spending REST API allowance reserved for collection. Offline tests use temporary Git repositories to exercise publication, concurrent updates, exact leases, cold starts, reruns, and stale artifacts.

The website job copies only the public aggregate artifact and records the source SHA and data commit SHA in `build-manifest.json`. Its JSON validation rejects unknown/internal fields, checkpoints, and symlinks. It uploads a Pages artifact; a separate environment-bound job receives only `pages: write` and `id-token: write`. The `data` branch itself is not a Pages source, and bot pushes are not relied on to trigger publication. Configure **Settings → Pages → GitHub Actions** before the first deployment; Pages is configured and the first default-branch deployment completed in [run 38242138886](https://github.com/cpanato/oss-maintainer-insights/actions/runs/38242138886). Repository visibility is unchanged.

For local review, generate samples or download the PR's `repository-snapshots` artifact into `apps/web/public/data/`, then run `npm run build`. The preview serves those aggregates without API requests. Mandatory CI continues to use fixtures and does not fetch `data` or depend on the live collection run. Historical ingestion, monthly trends, reconciliation queues, and the deployed-origin CORS check remain pending.

The owner selected [jsonschema/v6](https://github.com/santhosh-tekuri/jsonschema) to keep Go generation independent of Node. Publication follows the official [Pages configuration](https://github.com/actions/configure-pages), [artifact packaging](https://github.com/actions/upload-pages-artifact), and [deployment](https://github.com/actions/deploy-pages) actions, pinned to verified release commits.

### Complete creation-window collection

Saved collection uses policy `all-human-prs-created-in-90-days-v1`. The collector enumerates REST PR pages sorted by descending creation time, retaining all eligible human-authored, non-draft PRs in the half-open frozen creation interval. No PR-count limit or review-activity replacement applies. This avoids the search API's 1,000-result ceiling and creation-time differences between issue search and PR records. The UI shows the cohort window, selected creation age bounds, and source coverage. Older eighteen/three-hundred-PR policies remain readable and explicitly labeled as samples.

Each pending checkpoint records its window, method, exclusions, repository identity, normalized pages, and collection progress. Completed pages are reused without another API request during the same pending window; repository visibility and identity are always rechecked. Reviews and both comment streams are processed together per PR so each run advances covered PR counts. Pending windows do not expire on an arbitrary age boundary; changing repository identity, method, selection policy, or exclusions invalidates reuse. Pagination is not an atomic historical snapshot, and later edits cannot be reconstructed.

The public index reports inspected PRs, eligible PRs, complete event coverage, and whether enumeration has finished. No partial full-window result replaces the earlier public snapshot, even during policy migration. The website fetches saved JSON and progress from its own origin without GitHub API calls. Request attempts in a completed snapshot describe its final execution, not total acquisition cost across all resumed executions.

### First saved-sample review run

[PR collection run 38238121807](https://github.com/cpanato/oss-maintainer-insights/actions/runs/38238121807), source `4e5409f7495435b17bd10c894f190fc7a392e232`, generated public samples for all seven catalog entries on **12 October 2026**. The manifest attempt time was **10:44:17 UTC**. Each query used 20 attempts and selected eight PRs, with complete reviews for 8/8 and complete first-response streams for 5/8. All samples are explicitly partial overall because the remaining response streams exceeded the product cap. These observations describe the saved samples, not complete historical windows.

The downloaded aggregate artifact passes the same website JSON validation and is available in the local preview. PR publication, data-branch writes, and Pages deployment were correctly skipped. Internal checkpoints inherit repository/artifact visibility; “internal” means excluded from the website, not secret storage.

## Custom domain asset paths

GitHub Pages is configured with the custom domain `maintainerinsights.dev`. Astro uses `site: "https://maintainerinsights.dev"` and `base: "/"`, so stylesheets, scripts, navigation, and saved JSON resolve from the domain root. Keeping the former `/oss-maintainer-insights/` project prefix produces asset 404s on this domain. PR browser checks run against the same root path before deployment.

## Prow review participation

Methodology 1.1.0 recognizes human `/lgtm` and `/approve` signals in qualifying review bodies and issue/inline comments across repositories using that convention. Go and browser acquisition parse bodies immediately into bounded signals, before storage. Checkpoint version 2 discards version-1 validators so old body-free checkpoints cannot suppress command extraction. The public output contains only aggregate source counts and deduplicated reviewer–PR units, never bodies or individual command records.

The combined metric requires complete pagination of all three event streams. The unchanged 20-request browser cap can therefore produce fewer covered PRs for participation than under the old GitHub-only metric. Extracting commands adds no API endpoints or requests; saved collection now uses the expanded budgets documented above. Legacy 1.0.0 snapshots remain readable with an explicit label while failed recollection preserves them. New snapshots and caches use 1.1.0. Explicit cancellations affect an actor's command signal; this is not a reconstruction of authoritative Prow approvals or labels.

GitHub issue search can report a creation timestamp a second later than the detailed pull request endpoint. Selection uses search to find candidates, then uses detailed PR timestamps for band confirmation, age bounds, and metric clocks. Candidates outside their confirmed creation band are skipped without activity-based replacement. Exact equality between search and detail timestamps is not required.

### Local generation and publication

Run `go run ./cmd/collector snapshots --github-cli --rate-limit-wait=65m` after `gh auth login` to use the native CLI credential store without exporting its token. Repeat the command to continue pending windows; fresh complete windows skip network access. `--force` revalidates completed windows but still resumes pending work. Removing a pending checkpoint starts a new observation window. Valid normalized observations from legacy samples seed the larger window; missing PR pages and streams still come from GitHub.

Generation does not commit or publish by itself. Public aggregates, the progress index, and sanitized checkpoints can be reviewed on the dedicated `data` branch. Only aggregates and the index enter website artifacts. During a source-policy migration, merge the reviewed data update immediately before the source PR to reuse its complete/pending collection on deployment. The existing website serves its previous artifact until the source deploy. The old scheduled collector may overwrite checkpoints it cannot recognize if source migration is delayed.

### Rate limits, scheduling, and recovery

`--rate-limit-wait` defaults to two seconds and accepts up to 65 minutes. Primary exhaustion waits until `X-RateLimit-Reset` when it fits that limit; secondary limits honor `Retry-After` with bounded retry/backoff. Waiting is cancellable and a single execution retains a 90-minute deadline. Checkpoints and the public progress index persist every 25 successful network pages and at repository exit, including interrupted enumeration. Local progress and waits appear on stderr; stdout remains the JSON manifest. Saved document reads allow up to 64 MiB for larger sanitized checkpoints; an unreadable checkpoint is not treated as completed data.

Hourly scheduled runs resume pending work. Complete windows remain fresh for 24 hours, so those runs do not reacquire completed projects every hour. Manual refresh uses `--force`. Workflow collection retains the short wait default, allowing the next scheduled execution to resume instead of spending runner time sleeping through a primary reset.

The collector reserves 250 remaining API requests for publication and deployment. Live collection no longer runs automatically on PRs: CI uses offline fixtures for enumeration, pagination, interruption, rate waiting, privacy, and Go/TypeScript parity. This avoids sharing a scarce installation allowance between PR experiments and production publication. The product attempt cap is separate from GitHub's primary and secondary limits and does not guarantee available API quota.

[Run 38272719930](https://github.com/cpanato/oss-maintainer-insights/actions/runs/38272719930) completed collection but initially exhausted installation quota while the publisher queried the current `main` SHA. After reset, rerunning only failed jobs reused the collection artifacts and completed publication and Pages deployment. Re-running the whole workflow would unnecessarily repeat acquisition. A GitHub App can be evaluated later if sustained throughput needs a larger installation allowance.

### Local full-window validation

On 12 October 2026, local generation reused sanitized sample checkpoints and completed the full creation windows for Scorecard (87 eligible PRs), Allstar (11), and Cosign (68) with only three, two, and three new requests respectively. Kubernetes enumeration inspected 1,900 PR records and found 1,765 eligible PRs in the frozen 14 July–12 October window. A controlled SIGINT after 1,093 attempts preserved complete event coverage for 655 PRs and kept the earlier 298-PR public sample unchanged. The next execution skipped the three fresh complete projects and resumed Kubernetes at the same window. These are acquisition observations; they do not claim the Kubernetes full-window result is complete.

## Incremental catalog refresh

Scheduled collection retains a version-3 private per-PR update timestamp and last event-check time. Every refresh still enumerates the current 90-day creation cohort, so new PRs, draft conversions, exclusions, and entries leaving the window are reflected. New, changed, open, reopened, or incompletely cached PRs revalidate all three streams. Unchanged closed PRs reuse validated cached streams until a seven-day reconciliation target; reconciliation catches changes that do not reliably affect PR metadata. GitHub lists expose `updated_at`, but metadata is a hint rather than a guarantee that every review/comment change updates it. See the [official pull request listing contract](https://docs.github.com/en/rest/pulls/pulls#list-pull-requests).

Version-2 checkpoints remain readable. Legacy listing pages that omitted `updated_at` are fetched unconditionally once to restore the field instead of hiding it behind a 304. No events are reused across repository identity, methodology, or exclusion changes. Partial windows retain their frozen observation time and complete page chains across interruptions; earlier complete public data remains visible until the new cohort is complete. `--force` bypasses freshness and revalidates completed windows; existing pending windows still resume rather than abandoning saved progress.

Public `collection` metadata separates population coverage from event freshness: counts of PRs checked for the window and unchanged closed PRs reused, the earliest event-check time, and the reconciliation target. The website exposes this age. The target is not a guarantee of wall-clock freshness: API limits and pending work can delay completion. Live queries keep their existing bounds and behavior. Metric methodology remains 1.1.0.

The private `_queue.json` rotates the first catalog project each execution, including failed/interrupted runs. This prevents the same leading projects from always taking a shared API allowance. Catalog removals, unknown versions, and malformed queue state safely reset the starting position. The queue and tracked PR metadata stay on `data` under checkpoints and outside Pages artifacts.

## Monthly historical review backfill

`go run ./cmd/collector history` separately enumerates **every PR**, without a creation-date cutoff or population cap. Old PRs can receive reviews within the requested window. All eligible human, non-draft PRs require complete submitted-review, issue-comment, and inline-comment streams before publication. Listing uses REST pagination rather than the search API's 1,000-result ceiling. Source events before the window are retained privately in sanitized checkpoints because earliest-pair deduplication and Prow cancellations require that context.

The collector freezes a 90-day UTC observation window across interruptions and splits it at calendar-month boundaries. The first and last months can be partial and expose their exact half-open windows. Creation counts and first response use each month's creation cohort; historical review units include older PRs and belong to each pair's earliest currently qualifying review month. Repeated pairs in later months do not count again. Outcomes, dismissals, and command cancellations reflect the observed source state, rather than reconstructing an immutable past state.

Private checkpoints are sharded under `checkpoints/history/<owner--repo>/pages/` and `prs/`, with a small cursor advanced only after required pages are validated. A failed or interrupted stream keeps completed pages for replay. Completed generations refresh listing metadata, open/changed PRs, and expired event streams; unchanged closed streams have a seven-day reconciliation target. Repository identity, methodology, and bot-list changes prevent event reuse. Forced completed refreshes revalidate all streams; pending generations still resume. Public collection metadata records event-check age separately from coverage.

The hourly workflow prioritizes current creation cohorts, then runs history with 100 attempts per project, a five-minute overall deadline, and its own persisted rotating queue. With ten projects the historical request ceiling is 1,000 attempts, including metadata, pagination, retries, and conditional requests. The deadline and GitHub limits can stop earlier. Both collectors stop when 250 core requests remain to leave room for publication; concurrent consumers can still reduce that allowance. The initial backfill can take many runs; exhaustive collection on very large repositories can remain pending for a long time. No incomplete monthly series replaces a complete published series.

Public data lives under `snapshots/history/` on `data`; `history/index.json` reports pending coverage and preserved results. Recursive restoration and publication keep both historical directories and per-PR checkpoints across runs. Only public aggregate files enter Pages. Missing historical data never triggers automatic live collection in the browser. Charts and tables use the same monthly aggregates, with dates, population, coverage, event freshness, and methodology visible.

For local work, use a reviewed subset catalog and a separate output/state directory:

```sh
go run ./cmd/collector history --catalog path/to/pilot-catalog.json \
  --output .cache/history-public --state .cache/history-private \
  --github-cli --requests=1300 --timeout=90m --rate-limit-wait=65m
```

Both commands are resumable and do not publish by themselves. Each attempt is bounded; rerun against the same state to continue. Per-document disk checkpoints are limited to 64 MiB by the shared strict reader. Final aggregation currently loads eligible normalized PR records into memory; storage and memory costs must be measured before extending the window beyond 90 days. A GitHub App or a more efficient acquisition strategy may be needed for the largest repositories.

### First historical pilot

On 13 October 2026, local GitHub CLI collection for `ossf/allstar` completed exhaustive REST enumeration of 708 PRs and all event streams for 244 eligible human, non-draft PRs. Three resumable attempts used 100, 426, and 218 requests respectively (744 total); the first stopped at its budget and the second at a timeout. The frozen window is `2026-07-15T15:00:01Z`–`2026-10-13T15:00:01Z`. Four monthly aggregates have creation-cohort counts of 1, 4, 5, and 1, and review-unit counts of 0, 0, 1, and 0. The TypeScript engine independently reproduced all four Go monthly results from the sanitized per-PR records. These counters describe this observed window, not all past maintenance activity.

The September unit is traceable to the qualifying review on [Allstar PR #894](https://github.com/ossf/allstar/pull/894), review event ID `5257038751`, submitted on `2026-09-19T18:35:59Z`. Reviews on PRs predating the target window are included in the acquisition population, even when they produce no new qualifying pair within the window. Shared fixtures additionally exercise older-PR review events in the target month. The complete pilot files remain separate from source changes and require data publication after the historical implementation is merged.

## Methodology 1.2 population migration

Saved creation cohorts and historical review acquisition now include non-draft PRs authored by humans or bots. Missing/deleted authors remain excluded. Bot-authored PRs need event pages even when no human participation is found; bot review/comment actors remain excluded from human metrics. Counts distinguish inspected metadata, eligible PRs, selected live PRs, and complete event coverage. Public snapshots, historical series, and pending progress can carry mutually exclusive author/exclusion categories. Full windows expose duration in days and exact half-open UTC timestamps.

Methodology 1.0/1.1 files remain readable and explicitly disclose their bot-authored PR exclusion. They are preserved until a complete 1.2 replacement is available. Live cache keys and acquisition policy names change; saved freshness skips require current methodology. Historical generation resets population enumeration on migration, while complete unchanged closed 1.1 event shards can be reinterpreted under 1.2 within the existing reconciliation target. New bot-authored, changed, open, and expired PRs require collection. No 1.0 stream reuse is allowed because those records lack Prow extraction. A monthly series cannot mix methodology versions.

## Pages quota recovery

[Run 38589524180](https://github.com/cpanato/oss-maintainer-insights/actions/runs/38589524180) completed collection and build, but Pages deployment creation failed with an installation API rate limit. The previous 100-request reserve was insufficient in this execution; jobs and other API consumers can share the installation allowance. Both Go collectors now stop at 250 remaining requests.

The deployment job checks its own token's core quota before Pages configuration, waits until reset with a five-second buffer, and rechecks for at least 100 requests. Waiting is bounded to 65 minutes, with an 80-minute job timeout covering setup and the existing Pages deployment timeout. The source/data revision guard runs after waiting, so superseded artifacts still cannot deploy. API errors, invalid quota responses or a reset beyond the bound fail without replacing the current site. This preflight reduces primary-quota failures; it cannot reserve allowance against concurrent consumers or handle every secondary limit.

After reset, rerun failed deployment jobs rather than the entire collection. The deployment guard checks restored artifact revisions against current refs before publishing. See [GitHub rate-limit documentation](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api): Actions tokens have a per-repository quota, and quota reads do not consume primary allowance.
