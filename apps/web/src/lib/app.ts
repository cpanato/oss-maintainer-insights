import {
  GitHubError,
  normalizeRepository,
} from "../../../../packages/github/src/index.js";
import {
  collectLive,
  LiveCache,
  parsePublicSnapshot,
  CATALOG_POLICY,
  LEGACY_CATALOG_POLICY,
  BAND_CATALOG_POLICY,
  parseCollectionProgress,
} from "./live.js";
import type { PublicSnapshot } from "./live.js";
import catalog from "../../../../catalog/repositories.json" with { type: "json" };
const catalogEntries: { repository: string; bot_ids?: number[] }[] = catalog;
const savedRepositories = new Map(
  catalogEntries.map((entry) => [entry.repository.toLowerCase(), entry]),
);

function element<T extends HTMLElement>(id: string): T {
  const found = document.getElementById(id);
  if (!found) throw new Error(`Missing interface element: ${id}`);
  return found as T;
}
const text = (id: string, value: string) => {
  element(id).textContent = value;
};
const form = element<HTMLFormElement>("query");
const input = element<HTMLInputElement>("repository");
const cancel = element<HTMLButtonElement>("cancel");
let storage: Storage | null = null;
try {
  storage = window.localStorage;
} catch {
  /* Queries still work when storage is disabled. */
}
const cache = new LiveCache(storage);
let controller: AbortController | null = null;
let generation = 0;
let displayed: PublicSnapshot | null = null;
let reviewPage = 1;
let reviewPageSize = 10;
const reviewPageSizes = [10, 20, 50, 100, 200];
const labels: Record<string, string> = {
  invalid_input:
    "Enter owner/repository or a public github.com repository URL.",
  budget: "The 20-request query budget was reached.",
  rate_limit:
    "GitHub's API rate limit was reached. Wait for the reset before refreshing.",
  forbidden: "GitHub denied access to this repository.",
  not_found: "Repository not found or unavailable without authentication.",
  timeout: "GitHub did not respond within the request timeout.",
  cancelled: "Query cancelled.",
  network: "Could not reach GitHub. Check your connection and try again.",
  http: "GitHub returned an error. Please try again later.",
  invalid_response:
    "GitHub returned unexpected data; the affected stream is incomplete.",
  redirect:
    "The browser could not follow GitHub's repository redirect. Try the current repository name.",
};
function failureMessage(error: unknown): string {
  if (!(error instanceof GitHubError))
    return "The query could not be completed. Please try again.";
  return (
    (labels[error.reason] ?? labels.http!) +
    (error.retryAt
      ? ` Retry after ${new Date(error.retryAt).toLocaleTimeString()}.`
      : "")
  );
}
function duration(ms: number): string {
  if (ms < 60000) return `${Math.round(ms / 1000)}s`;
  if (ms < 3600000) return `${(ms / 60000).toFixed(1)} min`;
  if (ms < 86400000) return `${(ms / 3600000).toFixed(1)} h`;
  return `${(ms / 86400000).toFixed(1)} days`;
}
function render(snapshot: PublicSnapshot, source: "live" | "cache" | "saved") {
  if (displayed?.result.repository.id !== snapshot.result.repository.id)
    reviewPage = 1;
  displayed = snapshot;
  const { result, selection } = snapshot;
  const catalogSample = [
    CATALOG_POLICY,
    LEGACY_CATALOG_POLICY,
    BAND_CATALOG_POLICY,
  ].includes(snapshot.policy);
  const fullWindow = snapshot.policy === CATALOG_POLICY;
  const bandLimit = snapshot.policy === LEGACY_CATALOG_POLICY ? 6 : 100;
  text(
    "population-label",
    fullWindow ? "90-DAY CREATION COHORT" : "RECENT SAMPLE",
  );
  const windowDays = Math.round(
    (Date.parse(result.window.end) - Date.parse(result.window.start)) /
      86400000,
  );
  const date = (value: string) =>
    new Date(value).toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      timeZone: "UTC",
    });
  const age = snapshot.sample_age;
  const observed = Date.parse(result.collected_at);
  const ageNote = age
    ? ` · PR ages at collection: ${duration(observed - Date.parse(age.newest_created_at))}–${duration(observed - Date.parse(age.oldest_created_at))}`
    : "";
  const reviews = result.review_activity;
  const response = result.first_response;
  element("results").hidden = false;
  element("empty-state").hidden = true;
  element("clear-cache").hidden = source === "saved";
  text("repo-name", result.repository.full_name);
  text(
    "sample-description",
    `${selection.selected} ${fullWindow ? "eligible PRs" : "selected PRs"} · ${windowDays}-day window (${date(result.window.start)}–${date(result.window.end)}, UTC) · ${fullWindow ? "All eligible PRs created within this window" : catalogSample ? `Up to ${bandLimit} PRs from each of three 30-day creation bands` : "Up to 8 newest eligible PRs"}${ageNote} · ${snapshot.complete ? (fullWindow ? "Collection complete for this creation window" : "Collection complete for this sample") : "Partial collection"}`,
  );
  text(
    "sample-scope",
    `${fullWindow ? "Metrics cover all eligible human-authored, non-draft PRs created in this window. Reviews on PRs created before the window are outside this collection." : `All metrics below describe these ${selection.selected} selected PRs. They do not count all PRs, reviews, or contributors in this repository during the ${windowDays}-day window.`}${snapshot.selection.truncated || snapshot.selection.list_has_more ? " More PRs exist beyond the sample limit." : ""}${catalogSample ? " Refresh live replaces this view with a smaller sample of up to 8 recent PRs." : ""}`,
  );
  text(
    "freshness",
    `${source === "cache" ? "Cached result" : source === "saved" ? "Saved snapshot" : "Live query"} · Observed ${new Date(result.collected_at).toLocaleString()} · ${snapshot.requests}${source === "saved" ? "" : "/20"} requests ${fullWindow ? "in the final collection run" : "at collection"} · Methodology ${result.methodology_version}${source === "saved" || snapshot.rate.remaining === null ? "" : ` · API allowance at collection: ${snapshot.rate.remaining}`}`,
  );
  text("authors", String(result.creation_cohort.authors));
  text(
    "author-note",
    `${result.creation_cohort.eligible_prs} eligible PRs in this ${fullWindow ? "creation window" : "sample"}`,
  );
  element("event-freshness").hidden = !snapshot.collection;
  if (snapshot.collection) {
    const collection = snapshot.collection;
    text(
      "event-freshness",
      `Incremental collection: ${collection.refreshed_prs} PRs revalidated for this window; ${collection.reused_prs} unchanged closed PRs reused.${collection.events_checked_since ? ` Event streams last checked on or after ${new Date(collection.events_checked_since).toLocaleString()}.` : ""} Open or changed PRs are revalidated; closed PRs have a ${collection.reconciliation_interval_days}-day reconciliation target. API limits can delay updates. Population coverage and event freshness are separate.`,
    );
  }
  text(
    "reviewers",
    reviews.coverage === "unavailable" ? "—" : String(reviews.participants),
  );
  text(
    "reviewer-note",
    `${reviews.covered_prs}/${reviews.eligible_prs} PRs with complete ${result.methodology_version === "1.0.0" ? "GitHub reviews" : "review and comment streams"}`,
  );
  text(
    "concentration",
    reviews.concentration_80.value === null
      ? "—"
      : String(reviews.concentration_80.value),
  );
  text(
    "concentration-note",
    reviews.concentration_80.value === null
      ? reviews.review_units === 0
        ? "No qualifying reviews in this sample · Repository activity outside the sample is not measured"
        : `Sample too small · ${reviews.covered_prs}/5 required PRs with complete reviews`
      : "Reviewers accounting for at least 80% of review units",
  );
  text(
    "response",
    response.median_ms.value === null
      ? "—"
      : duration(response.median_ms.value),
  );
  text(
    "response-note",
    response.median_ms.value === null
      ? `${fullWindow ? "Creation window" : "Sample"} has ${response.responded_prs} human responses · At least 5 are needed for a median`
      : `Median across ${response.responded_prs} responded PRs`,
  );
  text("review-units", `${reviews.review_units} review units`);
  const sources = reviews.sources ?? {
    github_review_units: reviews.review_units,
    prow_command_units: 0,
  };
  text(
    "review-sources",
    `${sources.github_review_units} GitHub review units · ${sources.prow_command_units} Prow command units. Pairs appearing in both sources count once in the total.${result.methodology_version === "1.0.0" ? " Legacy snapshot: Prow commands were not included." : ""}`,
  );
  renderReviewPage();
  const coverage = element("coverage");
  coverage.replaceChildren();
  for (const [name, value] of [
    [
      fullWindow
        ? "PRs inspected during enumeration"
        : catalogSample
          ? "PR candidates inspected across age bands"
          : "PRs inspected on the first page",
      String(selection.listed),
    ],
    [
      fullWindow
        ? "Eligible human PRs in the window"
        : catalogSample
          ? "Eligible candidates across age bands"
          : "Eligible PRs on that page",
      String(selection.eligible_on_page),
    ],
    [
      fullWindow ? "Eligible creation cohort" : "Selected sample",
      String(selection.selected),
    ],
    [
      "Complete review participation streams",
      `${reviews.covered_prs}/${reviews.eligible_prs}`,
    ],
    [
      "Complete response streams",
      `${response.covered_prs}/${response.eligible_prs}`,
    ],
    [
      "Responded / unanswered covered PRs",
      `${response.responded_prs} / ${response.unanswered_prs}`,
    ],
  ]) {
    const term = document.createElement("dt");
    term.textContent = name!;
    const definition = document.createElement("dd");
    definition.textContent = value!;
    const pair = document.createElement("div");
    pair.append(term, definition);
    coverage.append(pair);
  }
  text(
    "coverage-notes",
    `${fullWindow ? "All eligible human-authored, non-draft PRs created within the 90-day window are enumerated through GitHub PR pages. Reviews on PRs created before the window are outside this collection. GitHub pagination is not an atomic historical snapshot." : catalogSample ? `Up to ${bandLimit} eligible human-authored, non-draft PRs from each of three 30-day creation bands are selected, newest first within each band. Each band inspects at most ${snapshot.policy === BAND_CATALOG_POLICY ? 100 : 30} search candidates. Empty bands stay empty; PRs are not replaced because they lack responses. This is a bounded sample, not a random sample or full 90-day census.` : "The newest eligible human-authored, non-draft PRs from the first 30-record page are selected, up to 8."}${selection.truncated || selection.list_has_more ? " The repository contains PRs outside this bounded sample." : ""} Uncovered PRs are not counted as unanswered. Bots, deleted actors, and self responses are excluded.`,
  );
  const failures = element("failure-list");
  failures.replaceChildren();
  for (const failure of snapshot.failures) {
    const li = document.createElement("li");
    li.textContent = `PR #${failure.pr}, ${failure.stream.replaceAll("_", " ")}: ${labels[failure.reason] ?? failure.reason}`;
    failures.append(li);
  }
  if (!snapshot.failures.length) {
    const li = document.createElement("li");
    li.textContent = snapshot.complete
      ? "All selected event streams completed. The sample is not a repository-wide window."
      : "Event streams are still incomplete.";
    failures.append(li);
  }
  const outcomes = element("outcomes");
  outcomes.replaceChildren();
  for (const [name, count] of Object.entries(result.creation_cohort.outcomes)) {
    const item = document.createElement("p");
    const value = document.createElement("strong");
    value.textContent = String(count);
    item.append(value, ` ${name.replaceAll("_", " ")}`);
    outcomes.append(item);
  }
}
function renderReviewPage() {
  if (!displayed) return;
  const reviews = displayed.result.review_activity;
  const count = reviews.distribution.length;
  const pages = Math.max(1, Math.ceil(count / reviewPageSize));
  reviewPage = Math.min(reviewPage, pages);
  const start = (reviewPage - 1) * reviewPageSize;
  element("review-controls").hidden = count === 0;
  element("review-pagination").hidden = count === 0;
  text(
    "review-page-summary",
    `Showing ${count ? start + 1 : 0}–${Math.min(start + reviewPageSize, count)} of ${count} participants · Page ${reviewPage} of ${pages}`,
  );
  element<HTMLButtonElement>("review-previous").disabled = reviewPage === 1;
  element<HTMLButtonElement>("review-next").disabled = reviewPage === pages;
  const bars = element("distribution");
  const table = element("review-table");
  bars.replaceChildren();
  table.replaceChildren();
  for (const item of reviews.distribution.slice(
    start,
    start + reviewPageSize,
  )) {
    const share = item.review_units / reviews.review_units;
    const bar = document.createElement("div");
    bar.className = "bar-row";
    const name = document.createElement("span");
    name.textContent = item.actor.login;
    const track = document.createElement("div");
    track.className = "bar-track";
    const fill = document.createElement("div");
    fill.className = "bar-fill";
    fill.style.width = `${share * 100}%`;
    const percent = document.createElement("span");
    percent.textContent = `${Math.round(share * 100)}%`;
    track.append(fill);
    bar.append(name, track, percent);
    bars.append(bar);
    const row = document.createElement("tr");
    const account = document.createElement("td");
    const link = document.createElement("a");
    link.href = `https://github.com/${encodeURIComponent(item.actor.login)}`;
    link.textContent = item.actor.login;
    account.append(link);
    row.append(account);
    for (const value of [
      String(item.review_units),
      `GitHub: ${item.sources?.github_review_units ?? item.review_units} · Prow: ${item.sources?.prow_command_units ?? 0}`,
      `${(share * 100).toFixed(1)}%`,
    ]) {
      const cell = document.createElement("td");
      cell.textContent = value;
      row.append(cell);
    }
    table.append(row);
  }
  element("distribution-empty").hidden = reviews.distribution.length > 0;
  text(
    "distribution-empty",
    reviews.coverage === "unavailable"
      ? "Review data is unavailable for this sample."
      : "No qualifying reviews in the covered sample.",
  );
}
element<HTMLSelectElement>("review-page-size").addEventListener(
  "change",
  (event) => {
    const size = Number((event.currentTarget as HTMLSelectElement).value);
    reviewPageSize = reviewPageSizes.includes(size) ? size : 10;
    reviewPage = 1;
    renderReviewPage();
  },
);
element("review-previous").addEventListener("click", () => {
  reviewPage = Math.max(1, reviewPage - 1);
  renderReviewPage();
});
element("review-next").addEventListener("click", () => {
  if (
    displayed &&
    reviewPage * reviewPageSize <
      displayed.result.review_activity.distribution.length
  )
    reviewPage++;
  renderReviewPage();
});
function busy(value: boolean) {
  element<HTMLButtonElement>("inspect").disabled = value;
  element<HTMLButtonElement>("refresh").disabled = value;
  cancel.hidden = !value;
  form.setAttribute("aria-busy", String(value));
}
async function query(repository: string, refresh = false, allowLive = true) {
  let normalized: string;
  try {
    normalized = normalizeRepository(repository);
  } catch (error) {
    text("error", failureMessage(error));
    element("error").hidden = false;
    return;
  }
  controller?.abort();
  const current = ++generation;
  controller = new AbortController();
  const signal = controller.signal;
  input.value = normalized;
  for (const project of document.querySelectorAll<HTMLAnchorElement>(
    "[data-repo]",
  )) {
    if (project.dataset.repo?.toLowerCase() === normalized.toLowerCase())
      project.setAttribute("aria-current", "true");
    else project.removeAttribute("aria-current");
  }
  const url = new URL(location.href);
  url.searchParams.set("repo", normalized);
  history.replaceState(null, "", url);
  element("error").hidden = true;
  element("contribution").hidden = savedRepositories.has(
    normalized.toLowerCase(),
  );
  element("collection-progress").hidden = true;
  const botIds = savedRepositories.get(normalized.toLowerCase())?.bot_ids ?? [];
  const cached =
    !refresh &&
    !savedRepositories.has(normalized.toLowerCase()) &&
    cache.read(normalized, botIds);
  if (cached) {
    render(cached, "cache");
    busy(false);
    text(
      "status",
      "Showing a complete cached sample. Cache expires after one hour; refresh to query GitHub again.",
    );
    return;
  }
  busy(true);
  if (!refresh && savedRepositories.has(normalized.toLowerCase())) {
    text("status", `Loading saved snapshot for ${normalized}.`);
    try {
      const progressRequest = fetch(
        `${import.meta.env.BASE_URL}data/index.json`,
        { signal, credentials: "omit" },
      )
        .then(async (response) =>
          response.ok
            ? parseCollectionProgress(
                (await response.json()) as unknown,
                normalized,
              )
            : null,
        )
        .catch(() => null);
      const response = await fetch(
        `${import.meta.env.BASE_URL}data/${normalized.toLowerCase().replace("/", "--")}.json`,
        { signal, credentials: "omit" },
      );
      const saved = response.ok
        ? parsePublicSnapshot((await response.json()) as unknown, normalized)
        : null;
      const progress = await progressRequest;
      if (current !== generation) return;
      if (progress) {
        text(
          "collection-progress",
          `Full 90-day collection in progress (${progress.window.start.slice(0, 10)}–${progress.window.end.slice(0, 10)}, UTC): ${progress.enumerated_prs} PRs inspected, ${progress.eligible_prs} eligible, ${progress.covered_prs} with complete review and comment streams. ${progress.enumeration_complete ? "PR enumeration finished; event collection continues." : "More PR pages remain to be enumerated."}${saved ? " The previous saved result remains visible until this collection completes." : " Results will be published when this collection completes."}`,
        );
        element("collection-progress").hidden = false;
      }
      if (saved) {
        render(saved, "saved");
        const age = Date.now() - Date.parse(saved.result.collected_at);
        text(
          "status",
          `Showing a saved ${saved.complete ? "complete" : "partial"} ${saved.policy === CATALOG_POLICY ? "90-day creation window" : "sample"}. No GitHub API calls were made.${age > 86400000 ? " This snapshot is over 24 hours old." : ""} Refresh live only when you need a new query.`,
        );
        busy(false);
        controller = null;
        return;
      }
    } catch {
      if (current !== generation) return;
    }
    if (signal.aborted || !allowLive) {
      text(
        "status",
        signal.aborted
          ? "Query cancelled."
          : "A saved snapshot is not available yet. Submit the form to request a live query.",
      );
      busy(false);
      controller = null;
      return;
    }
  }
  text("status", `Collecting ${normalized} · Maximum 20 API requests.`);
  try {
    const snapshot = await collectLive(normalized, {
      signal,
      botIds,
      onProgress: (partial) => {
        if (current !== generation) return;
        render(partial, "live");
        text(
          "status",
          `Collecting ${normalized} · ${partial.requests}/20 requests · Reviews ${partial.result.review_activity.covered_prs}/${partial.selection.selected} complete.`,
        );
      },
    });
    if (current !== generation) return;
    render(snapshot, "live");
    const saved = snapshot.complete && cache.write(normalized, snapshot);
    text(
      "status",
      signal.aborted
        ? "Query cancelled. Collected data remains visible with partial coverage."
        : snapshot.complete
          ? `Sample complete.${saved ? " Saved locally for one hour." : " Local cache unavailable; queries still work."}`
          : "Partial sample. Read the coverage and collection details before comparing results.",
    );
  } catch (error) {
    if (current !== generation) return;
    text("error", failureMessage(error));
    element("error").hidden = signal.aborted;
    text(
      "status",
      `${signal.aborted ? "Query cancelled." : "Query failed."}${displayed ? ` Previous visible sample: ${displayed.result.repository.full_name}, observed ${new Date(displayed.result.collected_at).toLocaleString()}.` : " No sample has been collected."}`,
    );
  } finally {
    if (current === generation) {
      busy(false);
      controller = null;
    }
  }
}
form.addEventListener("submit", (event) => {
  event.preventDefault();
  void query(input.value);
});
cancel.addEventListener("click", () => controller?.abort());
element("refresh").addEventListener("click", () => {
  if (displayed) void query(displayed.result.repository.full_name, true);
});
element("clear-cache").addEventListener("click", () => {
  if (displayed)
    text(
      "status",
      cache.clear(displayed.result.repository.full_name)
        ? "Local cache cleared. The visible sample remains available."
        : "Local storage is unavailable.",
    );
});
for (const example of document.querySelectorAll<HTMLAnchorElement>(
  "[data-repo]",
))
  example.addEventListener("click", (event) => {
    if (
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    )
      return;
    event.preventDefault();
    const repository = example.dataset.repo;
    if (repository)
      void query(repository, false, false).then(() => {
        if (input.value.toLowerCase() !== repository.toLowerCase()) return;
        const target =
          displayed?.result.repository.full_name.toLowerCase() ===
          repository.toLowerCase()
            ? element("results")
            : element("status");
        target.scrollIntoView({
          block: "start",
          behavior: window.matchMedia("(prefers-reduced-motion: reduce)")
            .matches
            ? "instant"
            : "smooth",
        });
      });
  });
const requested = new URL(location.href).searchParams.get("repo");
if (requested) void query(requested, false, false);

for (const button of document.querySelectorAll<HTMLButtonElement>(
  ".info-tip",
)) {
  const tooltip = element(button.getAttribute("popovertarget")!);
  const position = () => {
    const rect = button.getBoundingClientRect();
    tooltip.style.left = `${Math.max(12, Math.min(rect.left, window.innerWidth - tooltip.offsetWidth - 12))}px`;
    tooltip.style.top = `${Math.max(12, Math.min(rect.bottom + 8, window.innerHeight - tooltip.offsetHeight - 12))}px`;
  };
  const show = () => {
    tooltip.showPopover();
    position();
  };
  button.addEventListener("pointerenter", (event) => {
    if (event.pointerType === "mouse") show();
  });
  button.addEventListener("pointerleave", () => {
    if (document.activeElement !== button) tooltip.hidePopover();
  });
  button.addEventListener("click", (event) => {
    event.preventDefault();
    show();
  });
  button.addEventListener("focus", show);
  button.addEventListener("blur", () => tooltip.hidePopover());
  tooltip.addEventListener("toggle", position);
}
