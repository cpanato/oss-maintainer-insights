# CI practices

Keep language checks in one workflow and repository/workflow security audits independent. Do not couple unrelated checks through reusable workflows or cross-workflow polling.

| Workflow        | Purpose                                                                                | Trigger                | Required-check aggregation                           |
| --------------- | -------------------------------------------------------------------------------------- | ---------------------- | ---------------------------------------------------- |
| `ci.yml`        | Node lint/type checks, metrics/schema/parity tests, builds, Go tests and golangci-lint | PR, push to `main`     | `ci-required` requires every language job to succeed |
| `zizmor.yml`    | GitHub Actions security analysis with annotations                                      | PR, push to `main`     | Independent; not included in `ci-required`           |
| `scorecard.yml` | OpenSSF audit of repository security practices                                         | Push to `main`, weekly | Independent report; not a PR score threshold         |

## Language checks

Use the official golangci-lint action and pin the binary to **v2.14.0**. `.golangci.yml` enables errcheck, errorlint, gosec, govet, ineffassign, staticcheck, and unused, with gofmt formatting. Analyze `./cmd/... ./internal/...` explicitly to avoid traversing npm dependencies. Check the full codebase rather than only new issues.

ESLint uses the existing typescript-eslint dependency with `recommendedTypeChecked` and TypeScript project service. It checks unsafe assignments/calls and unhandled promises alongside the existing formatting and compiler checks. Test fixture parsing uses explicit types or `unknown`; test registration explicitly marks its returned promise as owned by the Node test runner. No additional Node lint library is required.

Local commands:

```sh
npm run check
golangci-lint run ./cmd/... ./internal/...
go test -race ./cmd/... ./internal/...
actionlint .github/workflows/*.yml
zizmor --offline --persona pedantic --no-progress .github
```

The zizmor workflow uses `persona: pedantic` and the tool version selected by its pinned action. Run local checks with `zizmor --offline --persona pedantic --no-progress .github`. CI also performs online action audits with a read-only GitHub token; offline checks do not substitute for those checks.

## Workflow hardening

- Declare `permissions: {}` at workflow level. Language jobs receive only `contents: read`; `ci-required` receives no repository permissions. Zizmor additionally receives `actions: read` for online analysis. Scorecard receives only `contents: read`.
- Pin external actions to verified full commit SHAs, tools to exact versions, and npm dependencies to the lockfile. Dependabot checks actions and npm weekly with a seven-day cooldown for version updates; security updates are handled separately by Dependabot.
- Disable checkout credential persistence. Do not use `pull_request_target`, checkout arbitrary PR content in privileged jobs, or interpolate untrusted input into shell scripts. No CI job requires a PAT or deploy credential.
- Limit job time and cancel superseded runs. Keep fixture tests independent of mutable API results. Retain reports for seven days, without credentials or raw API payloads.
- Review findings and fix their causes rather than introducing broad suppressions or disabling failure exit codes.

## OpenSSF reports and repository settings

Scorecard audits the default branch and repository settings, including permissions, dependency updates, pinned dependencies, and protection practices. The workflow checks out `main` and only triggers on a push to that branch or its weekly schedule. It does not execute repository code, does not run on PRs, and skips fork repositories.

Download `scorecard-report` from the workflow's artifacts to inspect its SARIF findings. `publish_results: false` keeps the workflow free of OIDC/write permissions and avoids publishing results to the Scorecard API. An executed audit is not a claim that every practice passed; review each finding and its scope.

After merge, configure `ci-required` in branch protection or a ruleset. Zizmor may be configured as a separate required check if desired. Review approvals, branch protection, and any account-level security controls are repository settings, not capabilities that a YAML workflow can guarantee. Scorecard's first scheduled/default-branch execution remains pending until this workflow is merged.

## References

- [Helm golangci-lint job](https://github.com/helm/chart-releaser/blob/main/.github/workflows/ci.yaml#L84-L106)
- [Helm zizmor workflow](https://github.com/helm/chart-releaser/blob/main/.github/workflows/zizmor.yaml)
- [OpenSSF Scorecard action](https://github.com/ossf/scorecard-action)
- [zizmor action](https://github.com/zizmorcore/zizmor-action)
