# OSS Maintainer Insights

Explore review participation, response times, and maintenance trends in public GitHub repositories, with live queries and community-curated historical data.

Status: early development. The UI supports saved catalog cohorts, bounded live queries, and monthly historical trends when backfill completes. Go generates validated 90-day creation cohorts and historical review windows with resumable checkpoints. Review participation includes GitHub reviews and human Prow `/lgtm`/`/approve` signals, deduplicated per reviewer and PR. Publication uses a dedicated `data` branch and GitHub Pages artifacts. See the [implementation plan](docs/design/oss-maintainer-insights-implementation-plan.md).

## Development

Requires Node (see `.node-version`), Go (see `.go-version`), Git, and rsync. Offline publication tests use temporary local Git repositories; they never contact GitHub.

```sh
npm ci
npm run check   # lint, format, typecheck, tests, builds (requires Go for parity tests)
npm run dev     # http://127.0.0.1:4321/
```

Choose a project from the prominent saved catalog to load its saved results, or enter `owner/repository` or a GitHub repository URL in the live query form. Direct links use `?repo=owner/repository`. Project cards show saved PR counts when available and label entries awaiting collection. Catalog entries load saved JSON without GitHub API requests; **Refresh live** explicitly requests a new sample. Other repositories use bounded unauthenticated live queries and offer a [contribution guide](CONTRIBUTING.md). Saved creation cohorts enumerate every eligible human- or bot-authored, non-draft PR created within a frozen 90-day window, across as many executions as needed. Historical review collection separately enumerates PRs of every age, including old PRs reviewed within the window. Monthly charts have equivalent tables, label partial calendar months, and keep creation-cohort and review-activity populations distinct. Earlier complete data remains visible until replacement collection completes; the UI shows backlog and event age. See [operations](docs/operations.md) for limits, caching, and behavior.

The **Collections** page summarizes published creation-cohort and historical backfill states for every catalog project, including inspected/eligible/covered counts, unfinished enumeration, reasons and request usage. It distinguishes preserved datasets from failed or pending attempts and labels shared index timestamps separately from dataset observation times. It is a static published overview, not a live job monitor.

Browser tests use simulated GitHub responses and need no secrets. See [accessibility and mobile checks](docs/accessibility.md) for keyboard, focus and narrow-screen review:

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
go run ./cmd/collector history
```

`analyze` validates normalized input against the embedded schema and computes metrics (expected output: `tests/fixtures/monthly-expected.json`). `probe` runs a bounded public-API feasibility check. `snapshots` reads `catalog/repositories.json`, writes public aggregates to `apps/web/public/data/` and sanitized checkpoints to `.cache/snapshots/`, and skips saved samples newer than 24 hours. Scheduled refresh reuses unchanged closed PR event streams, refreshes open/new/changed PRs, and targets seven-day reconciliation for closed PRs. Public results show event-check age separately from population coverage. A persisted rotating queue shares the API allowance across projects. Use `--force` to revalidate completed windows with ETags; pending windows keep resuming. Collection uses `GITHUB_TOKEN` only if set in the environment; no command publishes by itself. For local authenticated generation, `snapshots --github-cli --force --rate-limit-wait=65m` uses the installed GitHub CLI credential store without exporting its token. Pending collections resume from sanitized checkpoints; the wait limit bounds each API reset/retry pause. Rebuild the site after generating or downloading data.

`history` scans every PR, collects all required review and comment pages, and computes UTC calendar-month slices of a frozen 90-day window. It saves separate listing/per-PR checkpoints under `.cache/snapshots/history/`, publishes complete aggregates under `apps/web/public/data/history/`, and records pending coverage in `history/index.json`. Its independent rotating queue defaults to 100 attempts per project and a five-minute execution deadline; large repositories require multiple runs. Use `history --github-cli --requests=1300 --timeout=90m --rate-limit-wait=65m` for bounded local backfill. `--force` revalidates completed history; pending work keeps its frozen observation and resumes. Rebuild the site after generation.

The independent snapshot workflow runs on `main`, scheduled refreshes, and manual refreshes; it collects current creation cohorts first, then resumes historical work with a separate small budget. It saves data, builds only public JSON into the website, and deploys a Pages artifact. PRs validate collection with offline fixtures and make no live collection requests. Configure **Settings → Pages → GitHub Actions** before the first deployment. PR workflows never publish data or deploy Pages.

## Documentation

- [Design](docs/design/oss-maintainer-insights-design.md)
- [Implementation plan](docs/design/oss-maintainer-insights-implementation-plan.md)
- [Methodology](docs/methodology.md)
- [CI practices](docs/ci.md)
- [Operations and feasibility findings](docs/operations.md)

Repository documentation, code, comments, and user-facing strings are written in English. The project is licensed under Apache 2.0.
