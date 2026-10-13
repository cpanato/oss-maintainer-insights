import { Ajv } from "ajv";
import addFormats from "ajv-formats";
import type { FormatsPlugin } from "ajv-formats";
import resultSchema from "../../../../schemas/analysis-result.schema.json" with { type: "json" };
import historySchema from "../../../../schemas/repository-history.schema.json" with { type: "json" };
import type { AnalysisResult } from "../../../../packages/analysis/src/index.js";
import type { PublicSnapshot } from "./live.js";

const validator = new Ajv({ strict: true });
// NodeNext and Astro resolve this CommonJS export to different types.
// eslint-disable-next-line @typescript-eslint/no-unnecessary-type-assertion
const applyFormats: FormatsPlugin = addFormats as unknown as FormatsPlugin;
applyFormats(validator);
validator.addSchema(resultSchema);
const valid = validator.compile(historySchema);

export interface RepositoryHistory {
  schema_version: 1;
  policy: "all-pr-review-history-90-days-v1";
  repository: AnalysisResult["repository"];
  collected_at: string;
  window: AnalysisResult["window"];
  progress: {
    window: AnalysisResult["window"];
    enumerated_prs: number;
    eligible_prs: number;
    covered_prs: number;
    enumeration_complete: true;
  };
  collection: NonNullable<PublicSnapshot["collection"]>;
  months: AnalysisResult[];
}

/** History has a different review population from a PR creation-cohort sample. */
export function parseRepositoryHistory(
  value: unknown,
  repository: string,
): RepositoryHistory | null {
  if (!valid(value)) return null;
  const history = value as unknown as RepositoryHistory;
  const start = Date.parse(history.window.start);
  const end = Date.parse(history.window.end);
  const progress = history.progress;
  const collection = history.collection;
  if (
    history.repository.full_name.toLowerCase() !== repository.toLowerCase() ||
    end - start !== 90 * 86400000 ||
    end !== Date.parse(history.collected_at) ||
    progress.window.start !== history.window.start ||
    progress.window.end !== history.window.end ||
    progress.covered_prs !== progress.eligible_prs ||
    progress.eligible_prs > progress.enumerated_prs ||
    collection.refreshed_prs + collection.reused_prs !== progress.covered_prs ||
    (progress.covered_prs > 0 && !collection.events_checked_since) ||
    (collection.events_checked_since &&
      Date.parse(collection.events_checked_since) > end)
  )
    return null;
  let cursor = start;
  for (const month of history.months) {
    const date = new Date(cursor);
    const next = Math.min(
      end,
      Date.UTC(date.getUTCFullYear(), date.getUTCMonth() + 1, 1),
    );
    const reviews = month.review_activity;
    const response = month.first_response;
    if (
      month.repository.id !== history.repository.id ||
      month.repository.full_name !== history.repository.full_name ||
      month.collected_at !== history.collected_at ||
      Date.parse(month.window.start) !== cursor ||
      Date.parse(month.window.end) !== next ||
      next <= cursor ||
      month.creation_cohort.coverage !== "complete_for_window" ||
      reviews.coverage !== "complete_for_window" ||
      reviews.eligible_prs !== progress.eligible_prs ||
      reviews.covered_prs !== progress.covered_prs ||
      reviews.participants !== reviews.distribution.length ||
      new Set(reviews.distribution.map((row) => row.actor.id)).size !==
        reviews.participants ||
      reviews.distribution.reduce((sum, row) => sum + row.review_units, 0) !==
        reviews.review_units ||
      response.coverage !== "complete_for_window" ||
      response.eligible_prs !== month.creation_cohort.eligible_prs ||
      response.covered_prs !== response.eligible_prs ||
      response.responded_prs + response.unanswered_prs !==
        response.covered_prs ||
      month.creation_cohort.eligible_prs > progress.eligible_prs
    )
      return null;
    cursor = next;
  }
  return cursor === end ? history : null;
}
