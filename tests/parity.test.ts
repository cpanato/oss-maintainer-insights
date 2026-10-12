import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";
import { analyze } from "../packages/analysis/src/index.js";
import type { AnalysisInput } from "../packages/analysis/src/index.js";

let directory: string;
let binary: string;
before(() => {
  directory = mkdtempSync(join(tmpdir(), "oss-insights-parity-"));
  binary = join(directory, "collector");
  execFileSync("go", ["build", "-o", binary, "./cmd/collector"], {
    env: { ...process.env, GOTOOLCHAIN: "local" },
  });
});
after(() => {
  if (directory) rmSync(directory, { recursive: true, force: true });
});

const cases: Record<string, (input: AnalysisInput) => void> = {
  "monthly baseline": () => {},
  "empty success": (input) => {
    input.pull_requests = [];
  },
  "unfinished enumeration": (input) => {
    input.enumeration = { creation_cohort: false, review_activity: false };
  },
  "unanswered and insufficient response sample": (input) => {
    input.pull_requests[0]!.reviews = [];
  },
  "incomplete streams": (input) => {
    input.pull_requests[0]!.coverage.issue_comments = false;
    input.pull_requests[1]!.coverage.reviews = false;
  },
  "unavailable streams": (input) => {
    for (const pr of input.pull_requests)
      pr.coverage = {
        reviews: false,
        issue_comments: false,
        review_comments: false,
      };
  },
  "draft and missing authors": (input) => {
    input.pull_requests[0]!.draft = true;
    input.pull_requests[1]!.author = null;
  },
  "bots and self responses": (input) => {
    input.bot_ids = [10];
    input.pull_requests[0]!.reviews[0]!.actor = input.pull_requests[0]!.author;
  },
  "deleted reviewer": (input) => {
    input.pull_requests[0]!.reviews[0]!.actor = null;
  },
  "old PR with a review in this month": (input) => {
    input.pull_requests[0]!.created_at = "2025-12-01T00:00:00Z";
  },
  "earliest pair outside window": (input) => {
    const pr = input.pull_requests[0]!;
    pr.created_at = "2025-12-01T00:00:00Z";
    pr.reviews.push({
      ...pr.reviews[0]!,
      id: "earlier",
      occurred_at: "2025-12-31T23:59:59Z",
    });
  },
  "deduplicated review and rename": (input) => {
    const pr = input.pull_requests[0]!;
    pr.reviews.push({
      ...pr.reviews[0]!,
      id: "repeat",
      occurred_at: "2026-01-02T00:00:00Z",
      actor: { id: 10, login: "renamed", type: "User" },
    });
  },
  "UTC boundaries": (input) => {
    input.pull_requests[0]!.reviews[0]!.occurred_at = input.window.start;
    input.pull_requests[1]!.reviews[0]!.occurred_at = input.window.end;
  },
  "pending and dismissed reviews": (input) => {
    input.pull_requests[0]!.reviews[0]!.state = "PENDING";
    input.pull_requests[1]!.reviews[0]!.state = "DISMISSED";
  },
  "earliest inline response": (input) => {
    input.pull_requests[4]!.review_comments.push({
      id: "comment",
      actor: { id: 30, login: "commenter", type: "User" },
      occurred_at: "2026-01-01T00:30:00Z",
    });
  },
  "live sample": (input) => {
    input.mode = "live_sample";
  },
  "even median": (input) => {
    const pr = structuredClone(input.pull_requests[4]!);
    pr.id = 6;
    pr.reviews[0]!.occurred_at = "2026-01-01T06:00:00Z";
    input.pull_requests.push(pr);
  },
};
for (const [name, mutate] of Object.entries(cases)) {
  void test(`Go and TypeScript agree: ${name}`, () => {
    const input = JSON.parse(
      readFileSync(
        new URL("./fixtures/monthly-input.json", import.meta.url),
        "utf8",
      ),
    ) as AnalysisInput;
    mutate(input);
    const actual: unknown = JSON.parse(
      execFileSync(binary, ["analyze"], {
        input: JSON.stringify(input),
        encoding: "utf8",
      }),
    );
    assert.deepEqual(actual, analyze(input));
  });
}

void test("catalog age-band fixture preserves Go and TypeScript metric parity", () => {
  const input = JSON.parse(
    readFileSync(
      new URL("./fixtures/catalog-input.json", import.meta.url),
      "utf8",
    ),
  ) as AnalysisInput;
  const snapshot = JSON.parse(
    readFileSync(
      new URL("./fixtures/catalog-snapshot.json", import.meta.url),
      "utf8",
    ),
  ) as { result: unknown };
  const result: unknown = JSON.parse(
    execFileSync(binary, ["analyze"], {
      input: JSON.stringify(input),
      encoding: "utf8",
    }),
  );
  assert.deepEqual(result, analyze(input));
  assert.deepEqual(result, snapshot.result);
});

void test("Prow participation preserves Go and TypeScript metric parity", () => {
  const input = JSON.parse(
    readFileSync(
      new URL("./fixtures/prow-input.json", import.meta.url),
      "utf8",
    ),
  ) as AnalysisInput;
  for (const mode of ["historical", "live_sample"] as const) {
    input.mode = mode;
    const actual: unknown = JSON.parse(
      execFileSync(binary, ["analyze"], {
        input: JSON.stringify(input),
        encoding: "utf8",
      }),
    );
    assert.deepEqual(actual, analyze(input));
  }
});
