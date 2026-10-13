# OSS Maintainer Insights Implementation Plan

Status: execution plan; milestones 1–2 implemented, Actions credential access verified, Pages-origin validation pending
Date: 11 October 2026
Reference: [design document](oss-maintainer-insights-design.md)

Deliver the hybrid MVP: unauthenticated browser queries and published historical datasets for a reviewed catalog. Organize implementation into reviewable milestones with acceptance criteria. This plan does not deploy the site.

## Starting point and decisions

The repository initially contained a README, the Apache 2.0 license, and the design proposal. Keep the current name provisionally and the existing license. Use Astro, TypeScript, small React islands, and Recharts for the website. Implement the collector and its Actions execution in Go, as requested. Keep every repository artifact in English. Consult the project owner with alternatives before adopting an external library when the choice is uncertain.

Use versioned JSON Schemas and shared source fixtures and expected results to keep Go and TypeScript calculations aligned. Each methodology change must update both engines and pass cross-language tests. Neither analysis engine depends on network, storage, or the current clock.

Resolve these methodology details in contracts and examples:

- Separate coverage, sample sufficiency, and freshness. Complete pagination can coexist with insufficient observations. A failed collection is different from a successful empty result.
- Use fully covered eligible PRs as the five-observation threshold for review concentration and largest share; use responded PRs for the median. Always expose review units, distinct reviewers, eligible PRs, covered PRs, and unanswered counts. This is a display policy, not a confidence threshold.
- Ignore response events before PR creation or after observation. Reject invalid or missing timestamps at normalization. Historical review pairs belong to the earliest currently qualifying review's month; reconciliation may change that month when source states change.
- Define normalized-record retention, reconciliation frequency, and acceptable lag after measuring collection cost. Aggregated history does not guarantee complete retained event history.
- Treat the three proposed seed repositories as candidates until access and cost are measured.

## Proposed structure

```text
apps/web/                  # Astro, React islands, charts and tables (milestone 3)
packages/analysis/         # TypeScript normalization, contracts and metrics
packages/github/           # Browser REST client and request policy (milestone 2)
cmd/collector/             # Go CLI; offline analysis first, collection later
internal/analysis/         # Go contracts and deterministic metrics
internal/github/           # Scheduled GitHub client (milestone 4)
internal/collector/        # Backfill, checkpoints and reconciliation (milestone 4)
schemas/                   # Versioned JSON Schemas
tests/fixtures/            # Shared synthetic inputs and golden results
catalog/repositories.json  # Reviewed catalog (milestone 4)
docs/methodology.md
docs/operations.md         # Operational findings (milestone 2)
.github/workflows/
```

Use npm workspaces with one frozen lockfile and a Go module. Keep normalized records, checkpoints, and published aggregates in separate contracts. Only aggregates enter the website build. Live collection runs in the browser; scheduled collection runs as a Go binary in Actions.

## Milestones and acceptance criteria

### Milestone 1 — Contracts, metrics and PR CI

Create workspaces, lint/format/typecheck/test/build commands, schemas, and fixtures. Implement stable-ID identity, bot and missing-actor exclusions, reviewer–PR deduplication, 80% concentration, largest share, authors, outcomes, and first human response. Inject the observation time and label creation cohorts and review windows separately.

Deliver `.github/workflows/ci.yml` alongside the first executable code. Add Go analysis and an offline `collector analyze` command; authenticated collection is a later milestone. The offline command consumes prevalidated normalized input and rejects unknown fields and invalid metric inputs. Full JSON Schema validation at the Go collection/publishing boundary remains a milestone 4 requirement.

**Complete when:** fixtures cover repeated reviews, nonqualifying states, self events, account renames, bots, zero denominators, the five-observation policy, unanswered PRs, and UTC month boundaries. Schemas reject invalid records. Both engines match the shared golden dataset and scenario-based parity checks. CI runs on PRs, including forks, and failures produce a failing check.

### Milestone 2 — GitHub client and feasibility experiment

Implement serial browser requests, `Link` pagination, timeout, cancellation, centralized budget accounting, and bounded retries. Every request, including retries, consumes the budget. Share contracts with the Go client rather than attempting to share transport implementation across languages.

Prepare a minimal browser diagnostic and a manual Go Actions experiment to verify:

- CORS and exposed pagination/rate-limit headers from the Pages origin.
- Public cross-repository endpoint access with `GITHUB_TOKEN`.
- Cost per PR and complete enumeration cost, including PRs older than the 90-day window.

Recheck official API documentation during implementation and record evidence in `docs/operations.md`. Simulated tests do not replace real origin/access verification. If Pages is not configured, keep the deployed-origin check as an explicit launch gate.

**Complete when:** tests cover pagination, 403/429, `Retry-After`, reset, secondary limits, timeout, and abort. Permission failures are distinguished from rate limits. Access/cost evidence or an external blocker is recorded. Reaching a cap never implies historical completeness.

### Milestone 3 — Live query and accessible interface

Create the Astro shell with `/oss-maintainer-insights/` as base, repository input normalization, ID resolution, and shareable `?repo=...` URLs. Select from the first page of 30 PRs, restricted to the latest 90 days and up to eight human-authored non-draft PRs. Do not replace PRs without reviews.

Enforce 20 requests per query and prioritize selected PR reviews before additional comment streams. Admit first-response measurements only with all three streams complete. Show progress, cancellation, available metrics, denominators, and incomplete coverage reasons.

Add a one-hour cache keyed by repository ID, query policy, and contract/methodology versions. Network name resolution also consumes budget. Handle unavailable storage and quota errors; incomplete results never masquerade as complete cache entries.

**Complete when:** queries work without tokens; browser tests cover selection, budget including retries, cancellation, cache failures, invalid input, 404, restricted access, empty results, and prefixed asset/data paths. Charts have equivalent tables and keyboard-accessible controls; statuses do not rely on color.

### Milestone 4 — Go collector and one pilot project

Add validated catalog configuration, authenticated Go collection, and persistence on the `data` branch. Separate PR enumeration, event ingestion, and aggregation. Implement 90-day backfill, resumable checkpoints, and schema-validated datasets/manifests.

Enumerate old PRs for historical review activity. Retain only required identifiers, actors, timestamps, and states; discard bodies, diffs, emails, and full payloads. Resume incomplete batches with idempotent upserts; advance checkpoints only after every required page is fetched and validated. Choose Go JSON Schema tooling with the owner if library selection is uncertain.

**Complete when:** one pilot's metrics can be traced to source events. Interrupted/resumed collection neither duplicates units nor loses pages. Coverage records enumeration, pending streams, period, and reconciliation. Late events and dismissed reviews recalculate affected months. The website reads static datasets and pending states without live API requests.

### Milestone 5 — Daily updates and resilient publication

Implement overlap-based daily updates, refresh tracked open PRs, and rotate reconciliation of old PRs. Share the initial 700-request/30-minute budget across projects with a persisted queue so the first project cannot starve the others. Publish backlog and coverage.

Extend existing PR CI and create separate collection and publishing workflows. Run collection as Go. Separate read, data-write, and deploy permissions/jobs. Explicitly invoke build/deploy after data writes instead of relying on bot pushes triggering another workflow. Serialize data writers and prevent older deployments replacing newer ones.

Pin source and data SHAs in the build manifest, avoiding a circular reference to a data commit containing its own SHA. Publish datasets and manifest atomically. Preserve the last valid dataset on failure while recording attempt status and exposing age. Successful projects may advance when another fails.

**Complete when:** simulated collection, validation, and publishing failures preserve usable data. Reruns are safe; artifacts are reproducible from both revisions. Pages paths work, all public JSON validates, and artifacts exclude credentials and internal records. Actual Pages configuration/deployment is a separate operational step subject to available authorization.

### Milestone 6 — Initial catalog, contributions and MVP acceptance

Add three projects after measuring cost, retaining explicit partial coverage where needed. Finish catalog search, monthly charts/tables, methodology, contribution instructions, and a PR template.

Contribution CI validates local schema, duplicates, and size. Network checks verify public visibility and canonical resolution, support retry, and distinguish inconclusive rate-limit results from success. Do not execute monitored repositories' code.

**Complete when:** E2E checks cover a small project, a moderately active project, and one exceeding budget. Three entries are navigable with honest states, the contribution path works, and one dataset has a manual source audit. Verify Pages origin and accessibility before launch. Full 12-month history and comparisons are outside the MVP.

## CI for pull requests

Ship CI in milestone 1 and extend it as components arrive. Document the same local and Actions commands in the README.

- Trigger on `pull_request` and pushes to `main`. Declare workflow-level `permissions: {}` and grant `contents: read` only to jobs that need it. Use no secrets, data writes, or PR deployments. Do not execute PR code with `pull_request_target`.
- Pin Node and Go versions and external actions to verified commit SHAs. Install npm dependencies from the frozen lockfile and cache dependencies by lockfile. Do not cache credentials or test results.
- Run independent type-aware ESLint/type checks, tests/parity, build, Go quality/test/build, and golangci-lint jobs. Add browser checks when the application exists. Use timeouts and cancel superseded runs for a PR.
- Maintain the final `ci-required` check for language checks, failing if any required job fails, is cancelled, or unexpectedly skips. Run zizmor and OpenSSF Scorecard as independent workflows outside this aggregation. Configuring required checks in GitHub branch protection is an operational step.
- Keep mandatory tests deterministic, using fixtures and simulated API responses without mutable GitHub data or the `data` branch. Use validated synthetic datasets for PR builds, covering pending, partial, stale, and empty success.
- Upload test reports with limited retention. Add browser traces/screenshots and static site artifacts when those components exist; inspecting a PR artifact does not deploy it.

| Milestone | Required CI additions                                                                                                                                                   |
| --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1         | Lint, formatting, typecheck, metric/schema tests, Go/TypeScript parity, package builds, Go vet/race tests/build, golangci-lint, and independent zizmor/Scorecard audits |
| 2         | Simulated REST pagination, request accounting, retries, timeout, and cancellation                                                                                       |
| 3         | Astro Pages-base build and browser checks for queries, cache, errors, keyboard controls, and tables                                                                     |
| 4         | Simulated Go ingestion, checkpoint resumption, idempotency, late events, and aggregate validation                                                                       |
| 5         | Failure preservation, reproducibility, and artifact checks excluding internal records and forbidden fields                                                              |
| 6         | Local catalog validation, contribution flow, and small/moderate/over-budget E2E scenarios                                                                               |

Keep real API integration in a separate manual/scheduled workflow with a bounded budget. Test authenticated reads in Go Actions jobs and CORS/headers in a browser from Pages. Network-dependent catalog checks must allow retry and record inconclusive outcomes for review and launch validation without making every code change depend on API availability.

## Dependencies and gates

Main sequence: milestone 1 → 2 → 3 → 4 → 5 → 6. The visual shell can use fixtures after milestone 1; historical ingestion depends on milestone 2 cost measurements. Routine implementation does not require approval between milestones.

| Gate        | Required evidence                                                        | If it fails                                     |
| ----------- | ------------------------------------------------------------------------ | ----------------------------------------------- |
| Methodology | Contracts, fixtures, and parity without population/denominator ambiguity | Correct before collecting real data             |
| Feasibility | Endpoint access, origin headers, and measured cost                       | Adjust strategy and document credential changes |
| Integrity   | Idempotent resumption and verifiable coverage                            | Keep datasets partial/pending                   |
| Publication | Last success preserved, correct paths, reproducible build                | Correct before launch                           |
| MVP         | Unauthenticated live queries, three projects, contributions, and audit   | Close gaps before expanding                     |

After the MVP, use requests per project, backlog, runtime, storage, and reconciliation lag to decide whether to add 12-month history, projects, or comparisons.

## Milestone 3 implementation notes

The local Astro interface now supports bounded public-repository queries, shareable query URLs, progress and cancellation, metric denominators, reviewer bars with an equivalent table, outcomes, and methodology. Complete normalized samples use a one-hour versioned repository-ID cache; refresh bypasses it, and failed or partial queries preserve earlier complete entries. The historical catalog is visibly in development until milestone 4 produces validated datasets.

A native TypeScript controller is sufficient for the current interaction; React islands and a chart dependency remain deferred to historical trends. Playwright was selected by the owner and joins `ci-required` with simulated API responses and retained failure traces. PR builds upload a static preview artifact without deployment. Deployment-origin CORS verification remains an operational launch gate.

### Saved-sample extension requested during interface review

The owner requested pre-generated UI data, extra OpenSSF/CNCF examples, contribution guidance for uncataloged repositories, and a dedicated data branch. The Go sample generator now revalidates sanitized cached pages with ETags and publishes aggregate-only JSON. The UI uses those saved samples before any live GitHub query and exposes explicit refresh. PR CI validates the catalog and public contracts offline; independent real-API collection produces review artifacts. Default-branch generation writes `data` and explicitly builds/deploys Pages from public snapshots, with source/data provenance and separate write/deploy jobs.

This advances saved samples and publication infrastructure without claiming milestone 4's complete historical ingestion. Monthly backfill, resumable pending streams, fair historical queues, and reconciliation remain required. Pages configuration and deployed-origin evidence remain operational launch work. Catalog inclusion requests future historical tracking, not immediate complete history.

### Catalog sampling refinement

Saved collection now enumerates every eligible human PR created within a frozen 90-day window, without an eighteen/three-hundred-PR population cap. Hourly executions resume sanitized enumeration and per-PR event checkpoints under request budgets; completed windows stay fresh for 24 hours. Only complete cohorts replace public results. The UI labels legacy samples, full creation cohorts, and pending progress distinctly. Browser queries remain newest-eight/20-attempt and offer a catalog contribution guide. Tests cover more than 1,000 PRs and multi-execution completion without live API access; monthly historical ingestion remains pending.

### Prow participation extension

Methodology 1.1.0 adds observed human Prow review commands across repositories, including the Kubernetes and CNCF ecosystems. Acquisition strips source bodies after extracting bounded signals; both metric engines deduplicate GitHub and command sources per reviewer–PR pair and honor actor-local cancellation. Combined participation requires complete review/issue/inline streams. Shared fixtures cover extraction, cancellation, sources, coverage, and language parity. Checkpoint/cache invalidation regenerates source-aware samples while legacy GitHub-only public data remains readable after collection failures. Full historical ingestion and larger repository-wide populations remain follow-up work.

### Publication revision guards

Snapshot runs record the exact data commit restored before collection. Publication requires unchanged source and data revisions and an exact push lease; newer manually merged datasets and concurrent writers are preserved. Pages validates artifact source/data revisions again immediately before deployment. Ref checks use Git transport, and offline tests cover concurrent data updates, source advances, cold starts, and unchanged reruns. Incremental queues and monthly historical ingestion remain the next ordered implementation stages.

### Incremental creation-cohort collection

Version-3 private checkpoints track PR update metadata and event-check time while retaining version-2 read compatibility. Scheduled Go collection reuses unchanged closed event streams, refreshes new/changed/open PRs, and targets seven-day closed-PR reconciliation. The current creation cohort is enumerated on every refresh; no PR population cap is introduced. A persisted rotating project queue prevents fixed-order starvation under a shared rate limit. Public collection metadata and the UI disclose event age and cache reuse independently of population coverage. Monthly historical review windows, including reviews on older PRs, remain the next implementation stage.

### Monthly historical review implementation

Go now supports a separate exhaustive historical backfill across PRs of every creation age, with per-listing/per-PR sanitized checkpoints, validated cursor advancement, frozen 90-day windows, and independent fair rotation. Monthly aggregation uses the existing historical engine and methodology 1.1.0; shared Go/TypeScript fixtures distinguish old-PR reviews from creation-cohort counts, deduplicate repeated pairs across months, and cover Prow commands. Reconciliation recalculates affected months after source changes.

The workflow prioritizes current cohorts and gives historical work a separate small budget. Complete monthly series and pending progress publish through the existing source/data revision guards; recursive restoration retains checkpoint shards. Native chart bars and equivalent accessible tables use the existing TypeScript controller without adding a chart dependency. UTC dates, partial months, populations, event age, and unfinished backfill remain explicit. Offline tests cover pagination interruptions, duplicate listing records, exclusion changes, forced refresh, preserved results, schema/privacy boundaries, missing history, mobile rendering, and late browser responses. Exhaustive backfill is operationally pending until each project's actual enumeration and required event streams complete; implementation does not imply complete data for all catalog projects.
