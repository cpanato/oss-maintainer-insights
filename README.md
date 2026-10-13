# OSS Maintainer Insights

Explore review participation, response times, and maintenance trends in public GitHub repositories, with live queries and community-curated historical data.

Status: early development. The UI supports saved catalog samples and live queries; Go generates validated 90-day creation cohorts with resumable checkpoints. Review participation includes GitHub reviews and human Prow `/lgtm`/`/approve` signals, deduplicated per reviewer and PR. Historical ingestion and monthly trends are still planned. Publication uses a dedicated `data` branch and GitHub Pages artifacts. See the [implementation plan](docs/design/oss-maintainer-insights-implementation-plan.md).

## Development

Requires Node (see `.node-version`) and Go (see `.go-version`).

```sh
npm ci
npm run check   # lint, format, typecheck, tests, builds (requires Go for parity tests)
npm run dev     # http://127.0.0.1:4321/
```

Choose a project from the prominent saved catalog to load its saved results, or enter `owner/repository` or a GitHub repository URL in the live query form. Direct links use `?repo=owner/repository`. Project cards show saved PR counts when available and label entries awaiting collection. Catalog entries load saved JSON without GitHub API requests; **Refresh live** explicitly requests a new sample. Other repositories use bounded unauthenticated live queries and offer a [contribution guide](CONTRIBUTING.md). Saved collection enumerates every eligible human-authored, non-draft PR created within a frozen 90-day window, across as many executions as needed. Earlier samples remain visible until the complete window is ready; the UI shows collection progress. Reviews on PRs created before the window and monthly historical series remain outside this collection. See [operations](docs/operations.md) for limits, caching, and behavior.

Browser tests use simulated GitHub responses and need no secrets:

```sh
npm run build
npx playwright install chromium
npm run test:browser
```

Review activity shows 10 participants per page by default, with 10, 20, 50, 100, and 200 available. Pagination changes only the visible rows and bars; totals and shares use the entire collected dataset and page changes make no API requests.

Go checks (see [CI practices](docs/ci.md) for the full list):

```sh
go vet ./cmd/... ./internal/... ./schemas/...
go test -race ./cmd/... ./internal/... ./schemas/...
```

Run `npm run format` and `gofmt -w cmd internal schemas` to format sources.

## Go collector

```sh
go run ./cmd/collector analyze < tests/fixtures/monthly-input.json
go run ./cmd/collector probe --requests=10 ossf/scorecard
go run ./cmd/collector snapshots
```

`analyze` validates normalized input against the embedded schema and computes metrics (expected output: `tests/fixtures/monthly-expected.json`). `probe` runs a bounded public-API feasibility check. `snapshots` reads `catalog/repositories.json`, writes public aggregates to `apps/web/public/data/` and sanitized checkpoints to `.cache/snapshots/`, and skips saved samples newer than 24 hours. Use `--force` to revalidate with ETags. Collection uses `GITHUB_TOKEN` only if set in the environment; no command publishes by itself. For local authenticated generation, `snapshots --github-cli --force --rate-limit-wait=65m` uses the installed GitHub CLI credential store without exporting its token. Pending collections resume from sanitized checkpoints; the wait limit bounds each API reset/retry pause. Rebuild the site after generating or downloading data.

The independent snapshot workflow runs on `main`, scheduled refreshes, and manual refreshes; it saves data, builds only public JSON into the website, and deploys a Pages artifact. PRs validate collection with offline fixtures and make no live collection requests. Configure **Settings → Pages → GitHub Actions** before the first deployment. PR workflows never publish data or deploy Pages. The collected historical dataset remains a later milestone.

## Documentation

- [Design](docs/design/oss-maintainer-insights-design.md)
- [Implementation plan](docs/design/oss-maintainer-insights-implementation-plan.md)
- [Methodology](docs/methodology.md)
- [CI practices](docs/ci.md)
- [Operations and feasibility findings](docs/operations.md)

Repository documentation, code, comments, and user-facing strings are written in English. The project is licensed under Apache 2.0.
