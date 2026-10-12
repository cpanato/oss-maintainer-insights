# CI practices

Keep language checks in one workflow and repository/workflow security audits independent. Keep real API measurements outside deterministic PR checks. Do not couple unrelated checks through reusable workflows or cross-workflow polling.

| Workflow        | Purpose                                                                                              | Trigger                  | Required-check aggregation                                       |
| --------------- | ---------------------------------------------------------------------------------------------------- | ------------------------ | ---------------------------------------------------------------- |
| `ci.yml`        | Node/Astro checks, metrics/schema/parity tests, static build, Playwright, Go tests and golangci-lint | PR, push to `main`       | `ci-required` requires every language and browser job to succeed |
| `zizmor.yml`    | GitHub Actions security analysis with annotations                                                    | PR, push to `main`       | Independent; not included in `ci-required`                       |
| `api-probe.yml` | Bounded Go measurements with the workflow credential                                                 | Explicit manual dispatch | Independent operational evidence                                 |
| `scorecard.yml` | OpenSSF audit of repository security practices                                                       | Push to `main`, weekly   | Independent report; not a PR score threshold                     |

## Language checks

Use the official golangci-lint action and pin the binary to **v2.14.0**. `.golangci.yml` enables errcheck, errorlint, gosec, govet, ineffassign, staticcheck, and unused, with gofmt formatting. Analyze `./cmd/... ./internal/... ./schemas/...` explicitly to avoid traversing npm dependencies. Check the full codebase rather than only new issues.

ESLint uses the existing typescript-eslint dependency with `recommendedTypeChecked` and TypeScript project service. It checks unsafe assignments/calls and unhandled promises alongside the existing formatting and compiler checks. Test fixture parsing uses explicit types or `unknown`; test registration explicitly marks its returned promise as owned by the Node test runner. No additional Node lint library is required.

The browser job builds Astro with the `/oss-maintainer-insights/` prefix and runs Playwright with Chromium. GitHub responses are intercepted and simulated, covering selection, budget, cancellation, cache/refresh failures, empty results, restricted access, rate limits, keyboard controls, mobile layout, and prefixed navigation. Unit tests additionally cover acquisition pagination/retry accounting and cache validation. The job needs only `contents: read` and participates in `ci-required`. The static build artifact, HTML browser report, and failure traces/screenshots have seven-day retention. Preview artifacts are never deployed by PR CI.

Local commands:

```sh
npm run check
golangci-lint run ./cmd/... ./internal/... ./schemas/...
go test -race ./cmd/... ./internal/... ./schemas/...
actionlint .github/workflows/*.yml
zizmor --offline --persona pedantic --no-progress .github
```

The zizmor workflow uses `persona: pedantic` and the tool version selected by its pinned action. Run local checks with `zizmor --offline --persona pedantic --no-progress .github`. CI also performs online action audits with a read-only GitHub token; offline checks do not substitute for those checks.

## Workflow hardening

- Declare `permissions: {}` at workflow level. Language jobs receive only `contents: read`; `ci-required` receives no repository permissions. Zizmor additionally receives `actions: read` for online analysis. Scorecard receives read-only actions, issues, pull-request, and checks scopes for private-repository inspection, with inline explanations. The API probe receives only `contents: read`.
- Pin external actions to verified full commit SHAs, tools to exact versions, and npm dependencies to the lockfile. Dependabot checks actions, npm, and Go modules weekly with a seven-day cooldown for version updates; security updates are handled separately by Dependabot.
- Disable checkout credential persistence. Do not use `pull_request_target`, checkout arbitrary PR content in privileged jobs, or interpolate untrusted input into shell scripts. No CI job requires a PAT or deploy credential.
- Limit job time and cancel superseded runs. Keep fixture tests independent of mutable API results. Retain reports for seven days, without credentials or raw API payloads.
- Review findings and fix their causes rather than introducing broad suppressions or disabling failure exit codes.

## Real API experiment

`api-probe.yml` is intentionally manual: it spends shared GitHub API allowance to measure three public repositories with at most ten requests each. A manual trigger is necessary here to keep live measurements intentional and outside PR checks. Its Go step receives only the workflow token through its environment, logs no token, and uploads sanitized count/coverage reports for seven days. Run it after the workflow exists on `main`; inspect each report rather than treating a successful job as complete coverage.

## OpenSSF reports and repository settings

Scorecard audits the default branch and repository settings, including permissions, dependency updates, pinned dependencies, and protection practices. The workflow checks out `main` and only triggers on a push to that branch or its weekly schedule. It does not execute repository code, does not run on PRs, and skips fork repositories.

Download `scorecard-report` from the workflow's artifacts to inspect its SARIF findings. `publish_results: false` keeps the workflow free of OIDC/write permissions and avoids publishing results to the Scorecard API. An executed audit is not a claim that every practice passed; review each finding and its scope.

After merge, configure `ci-required` in branch protection or a ruleset. Zizmor may be configured as a separate required check if desired. Review approvals, branch protection, and any account-level security controls are repository settings, not capabilities that a YAML workflow can guarantee. Scorecard's first scheduled/default-branch execution remains pending until this workflow is merged.

## References

- [Helm golangci-lint job](https://github.com/helm/chart-releaser/blob/main/.github/workflows/ci.yaml#L84-L106)
- [Helm zizmor workflow](https://github.com/helm/chart-releaser/blob/main/.github/workflows/zizmor.yaml)
- [OpenSSF Scorecard action](https://github.com/ossf/scorecard-action)
- [zizmor action](https://github.com/zizmorcore/zizmor-action)

## Saved data and publication

`snapshots.yml` is independent from mandatory tests. PRs generate read-only real-API review artifacts, without data writes or deployment. Default-branch and daily runs restore `data`, collect bounded samples in Go, commit only generated JSON/checkpoints in a separate job, build the public site, and deploy a Pages artifact. Manual dispatch is intentional for owner-requested regeneration. A live failure or rate limit does not make mandatory fixture tests depend on API availability.

Permissions remain job-scoped: collection/build receive `contents: read`, data publication receives `contents: write`, and Pages deployment receives only `pages: write` plus `id-token: write`. Default-branch runs serialize collection through deployment and reject an obsolete source revision before writing data. Website artifacts carry source/data revision provenance and validate every public JSON file; private collector checkpoints never enter the website artifact. See [operations](operations.md) for the first Pages configuration and limits. Dependabot also checks Go modules weekly with the same seven-day cooldown.
