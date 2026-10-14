import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  analyze,
  parseReviewCommands,
} from "../packages/analysis/src/index.js";
import type {
  AnalysisInput,
  ReviewCommand,
} from "../packages/analysis/src/index.js";
const cases = JSON.parse(
  readFileSync(
    new URL("./fixtures/prow-commands.json", import.meta.url),
    "utf8",
  ),
) as { name: string; body: string; expected: ReviewCommand[] }[];
for (const entry of cases)
  void test(`Prow command extraction: ${entry.name}`, () =>
    assert.deepEqual(parseReviewCommands(entry.body), entry.expected));
void test("Prow participation deduplicates sources, respects actor-local cancellation, and requires comment coverage", () => {
  const input = JSON.parse(
    readFileSync(
      new URL("./fixtures/prow-input.json", import.meta.url),
      "utf8",
    ),
  ) as AnalysisInput;
  const before = structuredClone(input);
  const result = analyze(input);
  assert.equal(result.methodology_version, "1.2.0");
  assert.equal(result.review_activity.review_units, 8);
  assert.equal(result.review_activity.participants, 5);
  assert.deepEqual(result.review_activity.sources, {
    github_review_units: 5,
    prow_command_units: 4,
  });
  assert.ok(
    !result.review_activity.distribution.some((row) =>
      [50, 60, 70, 90, 104].includes(row.actor.id),
    ),
  );
  assert.deepEqual(input, before);
  input.pull_requests[0]!.coverage.issue_comments = false;
  const partial = analyze(input);
  assert.equal(partial.review_activity.covered_prs, 4);
  assert.equal(partial.review_activity.review_units, 6);
});
