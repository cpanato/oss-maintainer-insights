# Methodology 1.2.0

The Go and TypeScript engines implement the same deterministic rules. Shared JSON fixtures, expected results, and cross-language tests are the authority for behavior. Accounts are identified by GitHub user ID; login is a display label. Results describe observed activity, not an authoritative maintainer roster or a project health score.

## Input and populations

Inputs conform to `schemas/analysis-input.schema.json`. Dates are UTC timestamps ending in `Z`, with optional millisecond precision. Windows are half-open: start inclusive, end exclusive. Window end cannot exceed the explicitly provided observation time. PR records and source event IDs within each stream must be unique; acquisition must upsert repeated API records before analysis.

Include PRs created by humans, accounts typed `Bot`, and versioned explicit automation IDs. Exclude currently draft PRs and missing authors. Bot review/comment actors remain excluded, including configured automation IDs; bot-authored PRs can still receive qualifying human reviews and responses. Distinct author counts include only humans. Draft time cannot be reconstructed. Bot and missing actors, self events, and excluded PRs have separate counters. Event exclusion counters count source-event occurrences on eligible PRs, once even when the event could serve multiple metrics. Unknown automation is a limitation.

Creation cohorts contain eligible PRs created within the window. They determine distinct authors, observed outcomes, and first response. Historical review windows also include older eligible PRs. Live inputs contain the bounded selected PR sample; the analysis engine does not select or fetch PRs itself.

## Reviews

A review unit is one human reviewer–PR pair with either a qualifying submitted GitHub review (`APPROVED`, `CHANGES_REQUESTED`, or `COMMENTED`) or an active observed Prow command signal. Exclude pending/dismissed reviews, bots, deleted actors, self events, and events before creation or after observation. Require complete pagination of reviews, issue comments, and inline comments before the PR contributes to combined participation. Repeated reviews, both command kinds, and overlapping GitHub/Prow sources count once per pair.

Recognize standalone `/lgtm` and `/approve` lines, case-insensitively, including the optional `no-issue` argument. Recognize `/lgtm cancel`, `/approve cancel`, `/remove-lgtm`, and `/remove-approve`. Ignore blockquote command lines, indented code, inline-code examples, fenced code, and HTML comments. Acquisition retains only bounded normalized `review_commands` signals; comment/review bodies never enter checkpoints or public JSON. This applies to any repository using this command convention, without a Kubernetes-specific rule.

Replay each human actor's signals chronologically, using numeric GitHub event IDs to break timestamp ties when possible. Cancellation withdraws that actor's corresponding command kind; another active kind or a qualifying GitHub review still keeps the pair. Reapplication after cancellation starts a new active episode. Repeated positives in the same episode retain its earliest time. Signals from qualifying review bodies are included alongside issue and inline comments. These are observed human review intentions, not proof of Prow authorization or merge approval: OWNERS, plugin configuration, bot-applied labels, and commit-driven label resets are not reconstructed. See the [Prow review flow](https://docs.prow.k8s.io/docs/components/plugins/approve/approvers/) and [LGTM implementation](https://github.com/kubernetes-sigs/prow/blob/main/pkg/plugins/lgtm/lgtm.go).

Publish `github_review_units` and `prow_command_units` globally and per reviewer. These source counts overlap when both contribute to the same pair; their sum can exceed the deduplicated total. Historical units belong to the earliest currently qualifying event of the pair. Reconciliation and explicit cancellation can revise history; current edited comment bodies do not reconstruct former text. Live units include qualifying events of the selected creation sample through observation.

Concentration 80 is the smallest number of reviewers whose units reach at least 80% of all units. Largest share is the largest reviewer's units divided by all units. Hide both headline values when fewer than five eligible PRs have complete review-and-comment coverage, or when there are no units. Preserve raw distribution, units, participants, and covered-PR counts. Sort distribution by descending units and ascending actor ID for ties.

## First human response

For each creation-cohort PR, use the earliest qualifying submitted review, issue comment, or inline review comment by a human other than the author. Require complete pagination of all three streams. Ignore events before creation and after observation. An event exactly at creation yields an observed zero duration; an unanswered PR has no duration.

Report covered PRs, responded PRs, and unanswered covered PRs separately. Uncovered PRs are not labeled unanswered. Report the median response duration in milliseconds only for at least five responded PRs. For an even count, average the two middle durations.

## Coverage and sufficiency

Coverage and sample sufficiency are separate fields. Finished enumeration and all required streams yield `complete_for_sample` in live mode or `complete_for_window` in historical mode. Incomplete enumeration or some missing streams yields `partial`; a nonempty eligible population with no fully covered PRs yields `unavailable`. Empty successful enumeration can be complete with insufficient observations.

Each metric has its own eligible and covered counts. Enumeration flags describe creation and review populations separately. `insufficient_data` suppresses a headline value; it does not imply collection failure or a confidence interval. The acquisition layer must separately record freshness, latest reconciliation, pending work, and failure reasons before publishing.

## Schema and version changes

Validate normalized inputs and public outputs against the versioned JSON Schemas at acquisition/publication boundaries. Structural JSON Schema validation does not establish semantic relationships such as cohort counts or chronological windows; engines and fixtures check those rules. The Go CLI validates normalized inputs and public samples against embedded JSON Schemas using jsonschema/v6, followed by semantic analysis checks and strict field decoding. Source API payloads are projected into typed records before retention; public website samples contain aggregates only.

Methodology changes require updates to both engines, fixtures, tests, and either recomputation or an explicit break in published history. The initial bot-list policy is empty plus per-input explicit IDs; production collection must version that policy with its dataset.

## Saved catalog selection

Catalog snapshots enumerate every eligible human- or bot-authored, non-draft PR created within a frozen 90-day window. The collector paginates the REST PR listing from newest creation time until the window boundary or the end of the listing. It does not use search, impose a PR population limit, or select by review/response activity. PR creation timestamps and outcomes come from typed PR records. Duplicate IDs across shifting pages count once. GitHub pagination is not atomic; deletions and changes during traversal cannot be reconstructed as a historical snapshot.

Execution request limits bound work per run. Sanitized checkpoints persist completed pages and progress; subsequent executions resume the same window until enumeration and all three event streams finish. Only a fully complete window replaces public results. Old eighteen/three-hundred-PR samples remain readable and explicitly labeled while collection is pending. Reviews on PRs created before the window are outside this creation cohort. A separate historical collector enumerates PRs of every creation age and publishes monthly review windows only after exhaustive event coverage. Browser queries retain the newest-eight policy and 20-attempt budget. Go and TypeScript use the same metric engine, source definitions, and sufficiency thresholds.

Monthly history partitions a frozen 90-day window at UTC calendar-month boundaries; edge months can be partial and show their exact half-open dates. Monthly creation cohorts measure new PR authors, outcomes, and response. Monthly review windows count each pair in its earliest currently qualifying event month, including reviews on older PRs. Pairs whose earliest qualifying event precedes the 90-day window do not count again. Source state is observed rather than reconstructed at each past month boundary, and reconciliation can revise the series. Event-check age and enumeration completeness are disclosed separately.

## Migration from 1.0.0

Methodology 1.0.0 counted only GitHub submitted reviews and required only the review stream for participation. Version 1.1.0 includes Prow signals and requires all three event streams. The browser cache key changes with methodology; collector checkpoint version 2 forces comment-body extraction instead of reusing old body-free pages. Fresh legacy datasets trigger recollection. Partial updates preserve complete public results across policy migrations. Failed migration preserves prior public JSON, which remains readable and is visibly labeled as legacy GitHub-only data. New output uses 1.2.0 with source and population breakdowns.

## Bot-authored PR inclusion and population reporting

Version 1.2.0 includes non-draft bot-authored PRs even when they have no human reviews. This avoids selecting PRs based on observed participation. Human reviews, Prow signals, and first responses on these PRs count under the same deduplication, state, self-event, coverage, and sufficiency rules. Bot review/comment events never count as human participation. The legacy `exclusions.bot_authors` field is zero under 1.2; acquisition population counts identify bot-authored PRs separately.

Population metadata assigns each unique inspected PR to exactly one category: created at/after observation, outside a creation-cohort window, draft, missing/deleted author, or eligible human/bot author. Date filters take precedence; drafts take precedence over author categories. Historical collection does not apply a creation-date lower bound, so older PRs remain eligible. Category counts describe retrieved metadata; live sampling can select fewer PRs than the eligible total. Complete event coverage means every required page was fetched, not that every PR received a review.

Windows show duration in days and full UTC timestamps, start inclusive and end exclusive. Older datasets without population metadata show that the breakdown was not recorded. Version 1.0/1.1 datasets remain readable and visibly disclose their bot-authored PR exclusion until a complete 1.2 replacement is ready; monthly series must have one methodology throughout. Closed, unchanged 1.1 event streams can be reinterpreted with the 1.2 engine within the reconciliation target, because event normalization did not change. Version 1.0 streams remain incompatible because they lack Prow extraction. Population enumeration, open/changed/expired PRs, and newly included bot PRs still need acquisition.
