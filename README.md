# OSS Maintainer Insights

Explore review participation, response times, and maintenance trends in public GitHub repositories, with live queries and community-curated historical data.

Status: early development. Live queries work today; historical ingestion, the curated catalog, and deployment are planned. See the [implementation plan](docs/design/oss-maintainer-insights-implementation-plan.md).

## Development

Requires Node (see `.node-version`) and Go (see `.go-version`).

```sh
npm ci
npm run check   # lint, format, typecheck, tests, builds (requires Go for parity tests)
npm run dev     # http://127.0.0.1:4321/oss-maintainer-insights/
```

Enter `owner/repository` or a GitHub repository URL, or open `?repo=owner/repository`. Live queries are a bounded, unauthenticated sample, not a repository census. See [operations](docs/operations.md) for limits, caching, and behavior.

Browser tests use simulated GitHub responses and need no secrets:

```sh
npm run build
npx playwright install chromium
npm run test:browser
```

Go checks (see [CI practices](docs/ci.md) for the full list):

```sh
go vet ./cmd/... ./internal/...
go test -race ./cmd/... ./internal/...
```

Run `npm run format` and `gofmt -w cmd internal` to format sources.

## Go collector

```sh
go run ./cmd/collector analyze < tests/fixtures/monthly-input.json
go run ./cmd/collector probe --requests=10 ossf/scorecard
```

`analyze` computes metrics from normalized input (expected output: `tests/fixtures/monthly-expected.json`). `probe` runs a bounded public-API feasibility check and uses `GITHUB_TOKEN` only if set in the environment. Neither command publishes data.

## Documentation

- [Design](docs/design/oss-maintainer-insights-design.md)
- [Implementation plan](docs/design/oss-maintainer-insights-implementation-plan.md)
- [Methodology](docs/methodology.md)
- [CI practices](docs/ci.md)
- [Operations and feasibility findings](docs/operations.md)

Repository documentation, code, comments, and user-facing strings are written in English. The project is licensed under Apache 2.0.
