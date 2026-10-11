import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { analyze, normalizeActor } from "../packages/analysis/src/index.js";
import type { AnalysisInput, Review } from "../packages/analysis/src/index.js";

const fixture = (): AnalysisInput =>
  JSON.parse(
    readFileSync(
      new URL("./fixtures/monthly-input.json", import.meta.url),
      "utf8",
    ),
  );
const review = (overrides: Partial<Review> = {}): Review => ({
  id: "extra",
  actor: { id: 10, login: "renamed", type: "User" },
  occurred_at: "2026-01-02T00:00:00Z",
  state: "COMMENTED",
  ...overrides,
});

test("matches the shared monthly golden result", () => {
  const expected = JSON.parse(
    readFileSync(
      new URL("./fixtures/monthly-expected.json", import.meta.url),
      "utf8",
    ),
  );
  assert.deepEqual(analyze(fixture()), expected);
});

test("counts one reviewer–PR pair across review states and account renames", () => {
  const input = fixture();
  input.pull_requests[0]!.reviews.push(
    review(),
    review({ id: "changes", state: "CHANGES_REQUESTED" }),
  );
  const result = analyze(input);
  assert.equal(result.review_activity.review_units, 5);
  assert.equal(result.review_activity.participants, 2);
  assert.equal(result.first_response.median_ms.value, 3 * 3600000);
});

test("excludes self-responses, bots, missing actors and nonqualifying reviews", () => {
  const input = fixture();
  const pr = input.pull_requests[0]!;
  input.bot_ids = [30];
  pr.reviews = [
    review({ actor: pr.author }),
    review({
      id: "bot-type",
      actor: { id: 40, login: "automation", type: "Bot" },
    }),
    review({
      id: "bot-list",
      actor: { id: 30, login: "automation-2", type: "User" },
    }),
    review({ id: "deleted", actor: null }),
    review({ id: "pending", state: "PENDING" }),
    review({ id: "dismissed", state: "DISMISSED" }),
  ];
  const result = analyze(input);
  assert.equal(result.review_activity.review_units, 4);
  assert.equal(result.first_response.responded_prs, 4);
  assert.equal(result.first_response.unanswered_prs, 1);
  assert.equal(result.first_response.median_ms.value, null);
  assert.equal(result.exclusions.self_events, 1);
  assert.equal(result.exclusions.bot_events, 2);
  assert.equal(result.exclusions.missing_event_actors, 1);
});

test("excludes draft, missing and bot authors from every population", () => {
  const input = fixture();
  input.pull_requests[0]!.draft = true;
  input.pull_requests[1]!.author = null;
  input.pull_requests[2]!.author!.type = "Bot";
  input.bot_ids = [input.pull_requests[3]!.author!.id];
  const result = analyze(input);
  assert.equal(result.creation_cohort.eligible_prs, 1);
  assert.equal(result.review_activity.concentration_80.value, null);
  assert.deepEqual(result.exclusions, {
    draft_prs: 1,
    missing_authors: 1,
    bot_authors: 2,
    bot_events: 0,
    missing_event_actors: 0,
    self_events: 0,
  });
});

test("requires complete event streams for response and complete reviews for participation", () => {
  const input = fixture();
  input.pull_requests[0]!.coverage.issue_comments = false;
  input.pull_requests[1]!.coverage.reviews = false;
  const result = analyze(input);
  assert.equal(result.review_activity.covered_prs, 4);
  assert.equal(result.review_activity.review_units, 4);
  assert.equal(result.review_activity.coverage, "partial");
  assert.equal(result.first_response.covered_prs, 3);
  assert.equal(result.first_response.responded_prs, 3);
  assert.equal(result.first_response.unanswered_prs, 0);
  assert.equal(result.first_response.coverage, "partial");
});

test("distinguishes successful empty results from incomplete enumeration", () => {
  const input = fixture();
  input.pull_requests = [];
  assert.equal(analyze(input).review_activity.coverage, "complete_for_window");
  assert.equal(analyze(input).review_activity.concentration_80.value, null);
  input.enumeration.review_activity = false;
  assert.equal(analyze(input).review_activity.coverage, "partial");
});

test("distinguishes unavailable streams from zero review units", () => {
  const input = fixture();
  for (const pr of input.pull_requests)
    pr.coverage = {
      reviews: false,
      issue_comments: false,
      review_comments: false,
    };
  const result = analyze(input);
  assert.equal(result.review_activity.coverage, "unavailable");
  assert.equal(result.first_response.coverage, "unavailable");
  assert.equal(result.first_response.unanswered_prs, 0);
});

test("historical review windows include old PRs and use the earliest pair timestamp", () => {
  const input = fixture();
  input.pull_requests[0]!.created_at = "2025-12-01T00:00:00Z";
  const old = input.pull_requests[1]!;
  old.created_at = "2025-12-01T00:00:00Z";
  old.reviews.push(
    review({ id: "earlier", occurred_at: "2025-12-31T23:59:59Z" }),
  );
  const result = analyze(input);
  assert.equal(result.creation_cohort.eligible_prs, 3);
  assert.equal(result.review_activity.eligible_prs, 5);
  assert.equal(result.review_activity.review_units, 4);
});

test("UTC windows include the start and exclude the end", () => {
  const input = fixture();
  input.pull_requests[0]!.reviews[0]!.occurred_at = input.window.start;
  input.pull_requests[1]!.reviews[0]!.occurred_at = input.window.end;
  input.pull_requests[2]!.created_at = input.window.end;
  input.pull_requests[2]!.reviews = [];
  assert.equal(analyze(input).creation_cohort.eligible_prs, 4);
  assert.equal(analyze(input).review_activity.review_units, 3);
});

test("uses the earliest comment or qualifying review; ignores pre-creation and future events", () => {
  const input = fixture();
  const pr = input.pull_requests[4]!;
  pr.issue_comments.push(
    review({ occurred_at: "2026-01-01T00:30:00Z" }),
    review({ id: "before", occurred_at: "2025-12-31T23:59:59Z" }),
    review({ id: "future", occurred_at: "2026-02-02T00:00:00Z" }),
  );
  assert.equal(analyze(input).first_response.median_ms.value, 2 * 3600000);
});

test("live review activity follows the creation sample and includes reviews through observation", () => {
  const input = fixture();
  input.mode = "live_sample";
  input.window.end = "2026-01-02T00:00:00Z";
  input.pull_requests[0]!.reviews[0]!.occurred_at = "2026-01-03T00:00:00Z";
  const result = analyze(input);
  assert.equal(result.review_activity.review_units, 5);
  assert.equal(result.review_activity.coverage, "complete_for_sample");
});

test("five responses unlock median; even sample medians average the middle durations", () => {
  const input = fixture();
  const extra = structuredClone(input.pull_requests[4]!);
  extra.id = 6;
  extra.reviews[0]!.occurred_at = "2026-01-01T06:00:00Z";
  input.pull_requests.push(extra);
  assert.equal(analyze(input).first_response.median_ms.value, 3.5 * 3600000);
});

test("rejects invalid UTC windows and duplicate normalized records", () => {
  const input = fixture();
  input.window.start = "2026-02-30T00:00:00Z";
  assert.throws(() => analyze(input), /Invalid UTC/);
  input.window.start = input.window.end;
  assert.throws(() => analyze(input), /Window/);
  const duplicate = fixture();
  duplicate.pull_requests.push(duplicate.pull_requests[0]!);
  assert.throws(() => analyze(duplicate), /Duplicate PR/);
  const events = fixture();
  events.pull_requests[0]!.reviews.push(events.pull_requests[0]!.reviews[0]!);
  assert.throws(() => analyze(events), /Duplicate event/);
  assert.throws(
    () => normalizeActor({ id: 0, login: "", type: "User" }),
    /Invalid actor/,
  );
});

test("does not mutate input and produces deterministic results", () => {
  const input = fixture();
  const before = structuredClone(input);
  assert.deepEqual(analyze(input), analyze(input));
  assert.deepEqual(input, before);
});
