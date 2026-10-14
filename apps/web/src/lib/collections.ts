import { Ajv } from "ajv";
import addFormats from "ajv-formats";
import type { FormatsPlugin } from "ajv-formats";
import indexSchema from "../../../../schemas/snapshot-index.schema.json" with { type: "json" };
import populationSchema from "../../../../schemas/population.schema.json" with { type: "json" };
import { populationMatches } from "./population.js";
import {
  parsePublicSnapshot,
  CATALOG_POLICY,
  OLD_FULL_CATALOG_POLICY,
} from "./live.js";
import type { CollectionProgress } from "./live.js";
import { parseRepositoryHistory } from "./history.js";

const validator = new Ajv({ strict: true });
// NodeNext and Astro resolve this CommonJS export to different types.
// eslint-disable-next-line @typescript-eslint/no-unnecessary-type-assertion
const applyFormats: FormatsPlugin = addFormats as unknown as FormatsPlugin;
applyFormats(validator);
validator.addSchema(populationSchema);
const validIndex = validator.compile(indexSchema);

interface Entry {
  repository: string;
  status: "fresh" | "updated" | "preserved" | "unavailable" | "pending";
  reason?: string;
  requests: number;
  progress?: CollectionProgress;
}
export interface CollectionIndex {
  attempted_at: string;
  entries: Entry[];
}

export function parseCollectionIndex(value: unknown): CollectionIndex | null {
  if (!validIndex(value)) return null;
  const index = value as unknown as CollectionIndex;
  if (
    new Set(index.entries.map((e) => e.repository.toLowerCase())).size !==
    index.entries.length
  )
    return null;
  for (const { progress } of index.entries) {
    if (
      progress &&
      (progress.covered_prs > progress.eligible_prs ||
        progress.eligible_prs > progress.enumerated_prs ||
        Date.parse(progress.window.end) - Date.parse(progress.window.start) !==
          90 * 86400000 ||
        (progress.population &&
          !populationMatches(
            progress.population,
            progress.enumerated_prs,
            progress.eligible_prs,
          )))
    )
      return null;
  }
  return index;
}

export function utcDate(value: string): string {
  return new Date(value)
    .toISOString()
    .replace("T", " ")
    .replace(/\.\d+Z$/, "Z")
    .replace("Z", " UTC");
}
export function collectionWindow(window: {
  start: string;
  end: string;
}): string {
  return `${(Date.parse(window.end) - Date.parse(window.start)) / 86400000} days · ${utcDate(window.start)} → ${utcDate(window.end)}`;
}
const reasons: Record<string, string> = {
  budget: "Request budget reached; resumes from checkpoints.",
  rate_limit: "GitHub rate limit reached; resumes when allowance is available.",
  timeout: "Execution time limit reached; resumes from checkpoints.",
  cancelled: "Collection interrupted; resumes from checkpoints.",
  forbidden: "GitHub denied access.",
  not_found: "Repository was not found or is inaccessible.",
  network: "Network request failed.",
  http: "GitHub returned an HTTP error.",
  invalid_input: "Collection input was invalid.",
  invalid_response: "GitHub response could not be validated.",
  redirect: "Unexpected API redirect.",
  partial: "Required event pages remain incomplete.",
};

export function collectionRow(
  repository: string,
  kind: "cohort" | "history",
  index: CollectionIndex | null,
  raw: unknown,
) {
  const entry = index?.entries.find(
    (e) => e.repository.toLowerCase() === repository.toLowerCase(),
  );
  const snapshot =
    kind === "cohort" ? parsePublicSnapshot(raw, repository) : null;
  const history =
    kind === "history" ? parseRepositoryHistory(raw, repository) : null;
  const complete =
    !!history ||
    !!(
      snapshot?.complete &&
      [CATALOG_POLICY, OLD_FULL_CATALOG_POLICY].includes(snapshot.policy)
    );
  const dataset = snapshot?.result ?? history?.months[0];
  const savedWindow = snapshot?.result.window ?? history?.window;
  const pending = entry?.status === "pending";
  const error = !!entry && ["preserved", "unavailable"].includes(entry.status);
  const progress = pending ? entry.progress : undefined;
  const inspected =
    progress?.enumerated_prs ??
    history?.progress.enumerated_prs ??
    snapshot?.selection.listed;
  const eligible =
    progress?.eligible_prs ??
    history?.progress.eligible_prs ??
    snapshot?.selection.eligible_on_page;
  const covered =
    progress?.covered_prs ??
    history?.progress.covered_prs ??
    snapshot?.result.review_activity.covered_prs;
  const denominator =
    progress?.eligible_prs ??
    history?.progress.eligible_prs ??
    snapshot?.selection.selected;
  const enumeration = progress
    ? progress.enumeration_complete
      ? "All PR pages inspected."
      : "More PR pages remain; final total is unknown."
    : complete
      ? "All PR pages inspected."
      : "Full enumeration is not available.";
  const window = progress?.window ?? savedWindow;
  return {
    repository,
    kind,
    status: error
      ? "Error"
      : pending
        ? "Pending"
        : complete
          ? "Complete"
          : snapshot
            ? "Saved sample"
            : "Not collected",
    window: window ? collectionWindow(window) : "Not available",
    coverage:
      inspected === undefined
        ? "No published progress"
        : `${inspected} inspected · ${eligible} eligible · ${covered}/${denominator} with all event pages`,
    enumeration,
    reason: entry?.reason
      ? (reasons[entry.reason] ?? "Collection did not finish.")
      : pending
        ? "Backfill is unfinished."
        : error
          ? "Collection did not finish."
          : "—",
    requests: entry ? String(entry.requests) : "Not recorded",
    observed: dataset ? utcDate(dataset.collected_at) : "None",
    methodology: dataset?.methodology_version ?? null,
    previous: (pending || error) && !!dataset,
    indexUnavailable: !index,
  };
}
