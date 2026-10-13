import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  copyFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
  readFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

void test("website boundary validates public JSON and rejects source records and checkpoints", () => {
  const directory = mkdtempSync(join(tmpdir(), "oss-artifacts-"));
  const run = () =>
    execFileSync(
      process.execPath,
      ["scripts/check-site.mjs", "--directory", directory],
      { stdio: "pipe" },
    );
  try {
    const snapshot = join(directory, "example--repo.json");
    copyFileSync(
      new URL("./fixtures/live-snapshot.json", import.meta.url),
      snapshot,
    );
    assert.match(run().toString(), /validated/);
    const historyDirectory = join(directory, "history");
    mkdirSync(historyDirectory);
    const history = join(historyDirectory, "example--repo.json");
    copyFileSync(
      new URL("./fixtures/history-expected.json", import.meta.url),
      history,
    );
    assert.match(run().toString(), /validated/);
    const historical = JSON.parse(readFileSync(history, "utf8")) as Record<
      string,
      unknown
    >;
    writeFileSync(history, JSON.stringify({ ...historical, pages: {} }));
    assert.throws(run, /Invalid repository-history/);
    rmSync(historyDirectory, { recursive: true });
    const raw = JSON.parse(readFileSync(snapshot, "utf8")) as Record<
      string,
      unknown
    >;
    writeFileSync(
      snapshot,
      JSON.stringify({ ...raw, input: { body: "forbidden" } }),
    );
    assert.throws(run, /Invalid repository-snapshot/);
    copyFileSync(
      new URL("./fixtures/live-snapshot.json", import.meta.url),
      snapshot,
    );
    mkdirSync(join(directory, "checkpoints"));
    assert.throws(run, /Internal collector state/);
    rmSync(join(directory, "checkpoints"), { recursive: true });
    symlinkSync(snapshot, join(directory, "linked.json"));
    assert.throws(run, /symlinks/);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
