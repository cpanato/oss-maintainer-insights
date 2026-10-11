# Methodology 1.0.0

The Go and TypeScript engines implement the same deterministic rules. Shared JSON fixtures, expected results, and cross-language tests are the authority for behavior. Accounts are identified by GitHub user ID; login is a display label. Results describe observed activity, not an authoritative maintainer roster or a project health score.

## Input and populations

Inputs conform to `schemas/analysis-input.schema.json`. Dates are UTC timestamps ending in `Z`, with optional millisecond precision. Windows are half-open: start inclusive, end exclusive. Window end cannot exceed the explicitly provided observation time. PR records and source event IDs within each stream must be unique; acquisition must upsert repeated API records before analysis.

Exclude currently draft PRs, missing authors, accounts typed `Bot`, and versioned explicit bot IDs. Draft time cannot be reconstructed. Bot and missing actors, self events, and excluded PRs have separate counters. Event exclusion counters count source-event occurrences on eligible PRs, once even when the event could serve multiple metrics. Unknown automation is a limitation.

Creation cohorts contain eligible PRs created within the window. They determine distinct authors, observed outcomes, and first response. Historical review windows also include older eligible PRs. Live inputs contain the bounded selected PR sample; the analysis engine does not select or fetch PRs itself.

## Reviews

A review unit is one reviewer–PR pair with at least one submitted `APPROVED`, `CHANGES_REQUESTED`, or `COMMENTED` review. Exclude `PENDING`, `DISMISSED`, self reviews, and reviews before PR creation or after observation. Require complete review pagination for a PR to contribute units. Multiple reviews by the same account on one PR count once, including across login changes.

Historical units belong to the month of the earliest currently qualifying review of the pair. Fetching only reviews inside the target month cannot establish this rule. Reconciliation can revise history when review states change; the MVP does not reconstruct former dismissed states. Live units include qualifying observed reviews of the selected creation sample through observation.

Concentration 80 is the smallest number of reviewers whose units reach at least 80% of all units. Largest share is the largest reviewer's units divided by all units. Hide both headline values when fewer than five eligible PRs have complete review coverage, or when there are no units. Preserve raw distribution, units, participants, and covered-PR counts. Sort distribution by descending units and ascending actor ID for ties.

## First human response

For each creation-cohort PR, use the earliest qualifying submitted review, issue comment, or inline review comment by a human other than the author. Require complete pagination of all three streams. Ignore events before creation and after observation. An event exactly at creation yields an observed zero duration; an unanswered PR has no duration.

Report covered PRs, responded PRs, and unanswered covered PRs separately. Uncovered PRs are not labeled unanswered. Report the median response duration in milliseconds only for at least five responded PRs. For an even count, average the two middle durations.

## Coverage and sufficiency

Coverage and sample sufficiency are separate fields. Finished enumeration and all required streams yield `complete_for_sample` in live mode or `complete_for_window` in historical mode. Incomplete enumeration or some missing streams yields `partial`; a nonempty eligible population with no fully covered PRs yields `unavailable`. Empty successful enumeration can be complete with insufficient observations.

Each metric has its own eligible and covered counts. Enumeration flags describe creation and review populations separately. `insufficient_data` suppresses a headline value; it does not imply collection failure or a confidence interval. The acquisition layer must separately record freshness, latest reconciliation, pending work, and failure reasons before publishing.

## Schema and version changes

Validate normalized inputs and public outputs against the versioned JSON Schemas at acquisition/publication boundaries. Structural JSON Schema validation does not establish semantic relationships such as cohort counts or chronological windows; engines and fixtures check those rules. The current offline Go CLI accepts prevalidated normalized fixtures and performs semantic checks and strict field decoding. Full Go schema validation will be added with authenticated ingestion before publishing data.

Methodology changes require updates to both engines, fixtures, tests, and either recomputation or an explicit break in published history. The initial bot-list policy is empty plus per-input explicit IDs; production collection must version that policy with its dataset.
