# OSS Maintainer Insights

Explore review participation, response times, and maintenance trends in public GitHub repositories, with live queries and community-curated historical data.

The first implementation milestone provides deterministic Go and TypeScript metrics, versioned JSON Schemas, shared fixtures, an offline Go analysis command, and PR CI. The website, authenticated collection, and deployment follow in later milestones.

## Development

Use Node **24.21.0** (see `.node-version`) and Go **1.27.1** (see `go.mod`). Install dependencies from the committed lockfile:

```sh
npm ci
npm run check
```

`npm run check` runs lint, formatting checks, typecheck, unit/schema/parity tests, and the TypeScript package build. Parity tests compile the Go command and compare its output with the TypeScript engine for the same scenarios, so Go must be installed for this command.

Run Go checks separately:

```sh
test -z "$(gofmt -l cmd internal)"
go vet ./cmd/... ./internal/...
go test -race ./cmd/... ./internal/...
go build -o dist/collector ./cmd/collector
```

Go commands target the project's package directories explicitly so installed npm dependencies are not included in Go package discovery. To format source files, run `npm run format` and `gofmt -w cmd internal`.

## Offline analysis

The Go command currently analyzes normalized input; it does not fetch from GitHub or publish data. Use the shared, schema-tested fixture:

```sh
go run ./cmd/collector analyze < tests/fixtures/monthly-input.json
```

The expected result is in `tests/fixtures/monthly-expected.json`. The command rejects unknown fields, duplicate records, and invalid analysis inputs. Inputs must be JSON Schema validated before use; full schema validation at the Go ingestion boundary is scheduled with the collector milestone.

## Pull request checks

The CI workflow runs TypeScript quality, metric/schema/parity tests, TypeScript builds, and Go formatting/vet/race tests/builds. Reports are uploaded for seven days. Tests use synthetic fixtures and do not need secrets or API access. Configure `ci-required` as a required status check in GitHub branch protection after the first workflow run.

## Documentation

- [Design](docs/design/oss-maintainer-insights-design.md)
- [Implementation plan](docs/design/oss-maintainer-insights-implementation-plan.md)
- [Methodology](docs/methodology.md)

Repository documentation, code, comments, and user-facing strings are written in English. The project is licensed under Apache 2.0.
