import assert from "node:assert/strict";
import test from "node:test";
import {
  collectionRow,
  parseCollectionIndex,
} from "../apps/web/src/lib/collections.js";
import history from "./fixtures/history-expected.json" with { type: "json" };
import snapshot from "./fixtures/catalog-snapshot.json" with { type: "json" };
import sample from "./fixtures/live-snapshot.json" with { type: "json" };

const pending = () => ({
  schema_version: 1,
  attempted_at: "2026-10-13T00:00:00Z",
  entries: [
    {
      repository: "example/repo",
      status: "pending",
      reason: "budget",
      requests: 100,
      progress: {
        ...history.progress,
        covered_prs: 1,
        enumeration_complete: false,
      },
    },
  ],
});

void test("overview distinguishes unfinished enumeration from a previous complete history", () => {
  const row = collectionRow(
    "example/repo",
    "history",
    parseCollectionIndex(pending()),
    history,
  );
  assert.equal(row.status, "Pending");
  assert.equal(row.previous, true);
  assert.match(row.coverage, /1\/3 with all event pages/);
  assert.match(row.enumeration, /final total is unknown/);
  assert.match(row.reason, /Request budget/);
  assert.match(row.window, /90 days/);
  assert.match(row.observed, /UTC$/);
});

void test("overview preserves saved results after errors without calling them current successes", () => {
  const index = pending();
  index.entries[0]!.status = "preserved";
  index.entries[0]!.reason = "rate_limit";
  const row = collectionRow(
    "example/repo",
    "history",
    parseCollectionIndex(index),
    history,
  );
  assert.equal(row.status, "Error");
  assert.equal(row.previous, true);
  assert.match(row.reason, /rate limit/);
  assert.equal(row.requests, "100");
  assert.equal(
    collectionRow("example/repo", "history", null, history).status,
    "Complete",
  );
  assert.equal(
    collectionRow("example/repo", "history", null, history).indexUnavailable,
    true,
  );
});

void test("overview never turns missing data, mismatched repositories or live samples into full cohorts", () => {
  assert.equal(
    collectionRow("example/repo", "cohort", null, null).status,
    "Not collected",
  );
  assert.equal(
    collectionRow("example/repo", "cohort", null, snapshot).status,
    "Complete",
  );
  assert.equal(
    collectionRow("different/repo", "cohort", null, snapshot).status,
    "Not collected",
  );
  assert.equal(
    collectionRow("example/repo", "cohort", null, sample).status,
    "Saved sample",
  );
  assert.equal(
    collectionRow("example/repo", "history", null, history).methodology,
    "1.2.0",
  );
});

void test("overview rejects misleading progress totals, duplicate entries and invalid dates", () => {
  const index = pending();
  assert.ok(parseCollectionIndex(index));
  index.entries.push(structuredClone(index.entries[0]!));
  assert.equal(parseCollectionIndex(index), null);
  index.entries.pop();
  index.entries[0]!.progress.covered_prs = 4;
  assert.equal(parseCollectionIndex(index), null);
  index.entries[0]!.progress.covered_prs = 1;
  index.entries[0]!.progress.window.end = "2026-10-12T00:00:00Z";
  assert.equal(parseCollectionIndex(index), null);
  assert.equal(parseCollectionIndex({ entries: [] }), null);
});
