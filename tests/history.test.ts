import assert from "node:assert/strict";
import test from "node:test";
import input from "./fixtures/history-input.json" with { type: "json" };
import expected from "./fixtures/history-expected.json" with { type: "json" };
import { analyze } from "../packages/analysis/src/index.js";
import type { AnalysisInput } from "../packages/analysis/src/index.js";
import { parseRepositoryHistory } from "../apps/web/src/lib/history.js";

void test("monthly history matches the shared Go acquisition golden and includes reviews on old PRs", () => {
  const history = parseRepositoryHistory(expected, "example/repo");
  assert.ok(history);
  assert.deepEqual(
    history.months.map((month) =>
      analyze({ ...input, window: month.window } as AnalysisInput),
    ),
    history.months,
  );
  assert.deepEqual(
    history.months.map((month) => month.review_activity.review_units),
    [0, 1, 1, 1],
  );
  assert.deepEqual(
    history.months.map((month) => month.creation_cohort.eligible_prs),
    [0, 0, 1, 0],
  );
});

void test("historical publication rejects mixed repositories, gaps, partial populations and source records", () => {
  const mutate = (change: (value: typeof expected) => void) => {
    const value = structuredClone(expected);
    change(value);
    assert.equal(parseRepositoryHistory(value, "example/repo"), null);
  };
  assert.equal(parseRepositoryHistory(expected, "other/repo"), null);
  assert.equal(
    parseRepositoryHistory({ ...expected, input }, "example/repo"),
    null,
  );
  mutate((value) => {
    value.progress.enumeration_complete = false;
  });
  mutate((value) => {
    value.progress.covered_prs--;
  });
  mutate((value) => {
    value.months[1]!.window.start = "2026-08-02T00:00:00Z";
  });
  mutate((value) => {
    value.months[1]!.repository.id++;
  });
  mutate((value) => {
    value.collection.events_checked_since = "2027-01-01T00:00:00Z";
  });
  mutate((value) => {
    value.months[1]!.review_activity.coverage = "partial";
  });
});
