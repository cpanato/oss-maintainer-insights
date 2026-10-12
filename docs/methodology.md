# Methodology 1.1.0

The Go and TypeScript engines implement the same deterministic rules. Shared JSON fixtures, expected results, and cross-language tests are the authority for behavior. Accounts are identified by GitHub user ID; login is a display label. Results describe observed activity, not an authoritative maintainer roster or a project health score.

## Input and populations

Inputs conform to `schemas/analysis-input.schema.json`. Dates are UTC timestamps ending in `Z`, with optional millisecond precision. Windows are half-open: start inclusive, end exclusive. Window end cannot exceed the explicitly provided observation time. PR records and source event IDs within each stream must be unique; acquisition must upsert repeated API records before analysis.

Exclude currently draft PRs, missing authors, accounts typed `Bot`, and versioned explicit bot IDs. Draft time cannot be reconstructed. Bot and missing actors, self events, and excluded PRs have separate counters. Event exclusion counters count source-event occurrences on eligible PRs, once even when the event could serve multiple metrics. Unknown automation is a limitation.

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

Catalog snapshots select at most 100 human-authored, non-draft PRs from each of three disjoint 30-day creation bands within the preceding 90 days. Each band inspects its newest 100 search candidates; current PR details confirm eligibility. Empty bands stay empty. PRs without reviews or responses remain selected. Selection favors newer PRs within each band and is not random or exhaustive. Aggregate oldest/newest creation timestamps describe selected PR ages at collection. Browser queries retain the newest-eight policy. Both policies use the same metric engine and sufficiency thresholds; only selected populations differ.

## Migration from 1.0.0

Methodology 1.0.0 counted only GitHub submitted reviews and required only the review stream for participation. Version 1.1.0 includes Prow signals and requires all three event streams. The browser cache key changes with methodology; collector checkpoint version 2 forces comment-body extraction instead of reusing old body-free pages. Fresh legacy datasets trigger recollection. Within the same selection policy and methodology, partial updates preserve complete samples. Failed migration preserves prior public JSON, which remains readable and is visibly labeled as legacy GitHub-only data. New output always uses 1.1.0 with source breakdowns.
