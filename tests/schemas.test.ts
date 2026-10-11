import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { Ajv } from "ajv";
import type { AnySchema } from "ajv";
import { default as addFormats } from "ajv-formats";
import { analyze } from "../packages/analysis/src/index.js";
import type {
  AnalysisInput,
  AnalysisResult,
} from "../packages/analysis/src/index.js";

const load = <T = unknown>(path: string): T =>
  JSON.parse(readFileSync(new URL(path, import.meta.url), "utf8")) as T;
const ajv = new Ajv({ allErrors: true, strict: true });
addFormats.default(ajv);
const inputValid = ajv.compile(
  load<AnySchema>("../schemas/analysis-input.schema.json"),
);
const resultValid = ajv.compile(
  load<AnySchema>("../schemas/analysis-result.schema.json"),
);
const catalogValid = ajv.compile(
  load<AnySchema>("../schemas/catalog.schema.json"),
);

void test("shared input and golden output conform to strict schemas", () => {
  const input = load<AnalysisInput>("./fixtures/monthly-input.json");
  assert.equal(inputValid(input), true, ajv.errorsText(inputValid.errors));
  assert.equal(
    resultValid(load("./fixtures/monthly-expected.json")),
    true,
    ajv.errorsText(resultValid.errors),
  );
  assert.equal(
    resultValid(analyze(input)),
    true,
    ajv.errorsText(resultValid.errors),
  );
});

void test("rejects raw bodies, missing coverage, bad dates and unsupported versions", () => {
  const input = load<AnalysisInput>("./fixtures/monthly-input.json");
  const pr = input.pull_requests[0]!;
  Object.assign(pr, { body: "Never retain comment or PR bodies" });
  assert.equal(inputValid(input), false);
  Reflect.deleteProperty(pr, "body");
  Reflect.deleteProperty(pr, "coverage");
  assert.equal(inputValid(input), false);
  const wrong = {
    ...load<AnalysisInput>("./fixtures/monthly-input.json"),
    schema_version: 2,
  };
  assert.equal(inputValid(wrong), false);
  wrong.schema_version = 1;
  wrong.observed_at = "2026-02-30T00:00:00Z";
  assert.equal(inputValid(wrong), false);
});

void test("rejects contradictory metric status and numeric values", () => {
  const result = load<AnalysisResult>("./fixtures/monthly-expected.json");
  result.first_response.median_ms.status = "insufficient_data";
  assert.equal(resultValid(result), false);
  result.first_response.median_ms.value = null;
  assert.equal(resultValid(result), true);
  result.review_activity.largest_reviewer_share.value = 1.1;
  assert.equal(resultValid(result), false);
});

void test("validates catalog repository syntax and excludes unknown fields", () => {
  assert.equal(
    catalogValid([{ repository: "example/project", bot_ids: [42] }]),
    true,
  );
  assert.equal(
    catalogValid([
      { repository: "https://github.com/example/project", bot_ids: [] },
    ]),
    false,
  );
  assert.equal(
    catalogValid([
      { repository: "example/project", bot_ids: [], token: "forbidden" },
    ]),
    false,
  );
});
