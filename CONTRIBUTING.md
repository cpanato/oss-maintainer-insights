# Contributing repositories

The saved catalog serves pre-generated public repository creation cohorts. Historical ingestion and monthly trends are still in development. Adding an entry requests full collection of eligible PRs created within a 90-day window. It does not promise monthly historical series or immediate completion.

1. Fork this repository and create a branch.
2. Edit `catalog/repositories.json`, adding an object such as `{ "repository": "owner/repository", "group": "Community" }`. Use a public, canonical GitHub repository name. Choose `OpenSSF` or `CNCF` only for an associated project; otherwise use `Community`.
3. Optionally add `bot_ids` with stable GitHub user IDs for known automation not typed `Bot`. Explain the evidence in your PR. Do not add usernames as identifiers, tokens, private repositories, raw API responses, or generated checkpoint files.
4. Run `npm ci`, `npm run check`, and the Go checks in the README. Catalog schema, canonical name syntax, duplicates, and the current collector size limit are checked offline in PR CI. Browser tests require the separate Playwright setup in the README.
5. Open a PR describing the project, its foundation association if applicable, and why historical tracking would be useful. Use English for repository artifacts and PR text.

PR CI validates the collector with offline fixtures. After merge, the independent snapshot workflow checks actual public API access and records collection progress. Rate limits or network failures are inconclusive operational evidence, not successful visibility checks and not mandatory language-test failures. Reviewers inspect canonical identity, access, collection cost, and coverage before acceptance. Collection never executes code from monitored repositories.

After merge, scheduled Go collection enumerates all eligible human PRs in the 90-day creation window and resumes pending event pages until complete. Full-window data becomes available after all required PR pages and event streams finish. The UI must disclose pending, partial, and stale states. Larger projects may need resumable backfill and several runs. The current request-bounded collector admits at most 20 catalog entries; increasing that limit requires a budget and fairness review.

Generated website samples contain aggregate results only. Normalized events and conditional-page checkpoints are internal collector state, stored separately from public artifacts. A failed or partial update cannot replace an earlier complete sample. Please do not manually fabricate metric values or represent a bounded sample as full historical coverage.
