import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { Ajv } from "ajv";
import catalogSchema from "../schemas/catalog.schema.json" with { type: "json" };
import catalog from "../catalog/repositories.json" with { type: "json" };
import { normalizeRepository } from "../packages/github/src/index.js";
import { parsePublicSnapshot } from "../apps/web/src/lib/live.js";

void test("reviewed catalog is valid, canonical, unique and within the collector limit", () => {
  assert.equal(new Ajv({ strict: true }).compile(catalogSchema)(catalog), true);
  assert.ok(catalog.length <= 20);
  const repositories = catalog.map((entry) =>
    normalizeRepository(entry.repository).toLowerCase(),
  );
  assert.equal(new Set(repositories).size, catalog.length);
});
void test("public snapshots validate aggregates and reject internal records or inconsistent counts", () => {
  const raw: unknown = JSON.parse(
    readFileSync(
      new URL("./fixtures/live-snapshot.json", import.meta.url),
      "utf8",
    ),
  );
  const snapshot = parsePublicSnapshot(raw, "example/repo")!;
  assert.ok(snapshot);
  assert.equal(parsePublicSnapshot(raw, "other/repo"), null);
  assert.equal(
    parsePublicSnapshot(
      { ...snapshot, input: { body: "secret" } },
      "example/repo",
    ),
    null,
  );
  assert.equal(
    parsePublicSnapshot(
      { ...snapshot, selection: { ...snapshot.selection, selected: 8 } },
      "example/repo",
    ),
    null,
  );
  assert.equal(
    parsePublicSnapshot(
      {
        ...snapshot,
        result: {
          ...snapshot.result,
          first_response: { ...snapshot.result.first_response, covered_prs: 0 },
        },
      },
      "example/repo",
    ),
    null,
  );
});

void test("incremental snapshots distinguish event freshness and reject contradictory collection metadata", () => {
  const raw: unknown = JSON.parse(
    readFileSync(
      new URL("./fixtures/incremental-snapshot.json", import.meta.url),
      "utf8",
    ),
  );
  const saved = parsePublicSnapshot(raw, "example/repo")!;
  assert.ok(saved);
  assert.equal(saved.collection?.reused_prs, 18);
  for (const collection of [
    { ...saved.collection, refreshed_prs: 99 },
    { ...saved.collection, events_checked_since: "2026-10-13T12:00:00Z" },
    { ...saved.collection, refreshed_prs: 0 },
    { ...saved.collection, events_checked_since: undefined },
  ])
    assert.equal(
      parsePublicSnapshot({ ...saved, collection }, "example/repo"),
      null,
    );
});
