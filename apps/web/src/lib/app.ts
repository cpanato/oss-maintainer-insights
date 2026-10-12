import {
  GitHubError,
  normalizeRepository,
} from "../../../../packages/github/src/index.js";
import { collectLive, LiveCache, parsePublicSnapshot } from "./live.js";
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
  displayed = snapshot;
  const { result, selection } = snapshot;
  const catalogSample =
    snapshot.policy === "three-age-bands-eighteen-human-prs-90-days-v1";
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
    `${selection.selected} selected PRs · ${catalogSample ? "Three 30-day creation bands" : "Newest PRs in the preceding 90 days"}${ageNote} · ${snapshot.complete ? "Collection complete for this sample" : "Partial collection"}`,
  );
  text(
    "freshness",
    `${source === "cache" ? "Cached result" : source === "saved" ? "Saved snapshot" : "Live query"} · Observed ${new Date(result.collected_at).toLocaleString()} · ${snapshot.requests}${source === "saved" ? "" : "/20"} requests at collection${source === "saved" || snapshot.rate.remaining === null ? "" : ` · API allowance at collection: ${snapshot.rate.remaining}`}`,
  );
  text("authors", String(result.creation_cohort.authors));
  text(
    "author-note",
    `${result.creation_cohort.eligible_prs} eligible PRs in this sample`,
  );
  text(
    "reviewers",
    reviews.coverage === "unavailable" ? "—" : String(reviews.participants),
  );
  text(
    "reviewer-note",
    `${reviews.covered_prs}/${reviews.eligible_prs} PRs with complete reviews`,
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
      ? `Sample has ${response.responded_prs} human responses · At least 5 are needed for a median`
      : `Median across ${response.responded_prs} responded PRs`,
  );
  text("review-units", `${reviews.review_units} review units`);
  const bars = element("distribution");
  const table = element("review-table");
  bars.replaceChildren();
  table.replaceChildren();
  for (const item of reviews.distribution) {
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
  const coverage = element("coverage");
  coverage.replaceChildren();
  for (const [name, value] of [
    [
      catalogSample
        ? "PR candidates inspected across age bands"
        : "PRs inspected on the first page",
      String(selection.listed),
    ],
    [
      catalogSample
        ? "Eligible candidates across age bands"
        : "Eligible PRs on that page",
      String(selection.eligible_on_page),
    ],
    ["Selected sample", String(selection.selected)],
    [
      "Complete review streams",
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
    `${catalogSample ? "Up to six eligible human-authored, non-draft PRs from each of three 30-day creation bands are selected, newest first within each band. Each band inspects at most 30 search candidates. Empty bands stay empty; PRs are not replaced because they lack responses. This is a bounded sample, not a random sample or full 90-day census." : "The newest eligible human-authored, non-draft PRs from the first 30-record page are selected, up to 8."}${selection.truncated || selection.list_has_more ? " The repository contains PRs outside this bounded sample." : ""} Uncovered PRs are not counted as unanswered. Bots, deleted actors, and self responses are excluded.`,
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
  const url = new URL(location.href);
  url.searchParams.set("repo", normalized);
  history.replaceState(null, "", url);
  element("error").hidden = true;
  element("contribution").hidden = savedRepositories.has(
    normalized.toLowerCase(),
  );
  const botIds = savedRepositories.get(normalized.toLowerCase())?.bot_ids ?? [];
  const cached = !refresh && cache.read(normalized, botIds);
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
      const response = await fetch(
        `${import.meta.env.BASE_URL}data/${normalized.toLowerCase().replace("/", "--")}.json`,
        { signal, credentials: "omit" },
      );
      const saved = response.ok
        ? parsePublicSnapshot((await response.json()) as unknown, normalized)
        : null;
      if (current !== generation) return;
      if (saved) {
        render(saved, "saved");
        const age = Date.now() - Date.parse(saved.result.collected_at);
        text(
          "status",
          `Showing a saved ${saved.complete ? "complete" : "partial"} sample. No GitHub API calls were made.${age > 86400000 ? " This snapshot is over 24 hours old." : ""} Refresh live only when you need a new query.`,
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
for (const example of document.querySelectorAll<HTMLButtonElement>(
  "[data-repo]",
))
  example.addEventListener("click", () => {
    if (example.dataset.repo) void query(example.dataset.repo);
  });
const requested = new URL(location.href).searchParams.get("repo");
if (requested) void query(requested, false, false);
