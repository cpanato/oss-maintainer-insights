import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join, resolve } from "node:path";
import test from "node:test";
import type { TestContext } from "node:test";

function fixture(
  t: TestContext,
  replies: string[],
  maximum = "3900",
  failure = false,
) {
  const root = mkdtempSync(join(tmpdir(), "oss-pages-quota-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeFileSync(join(root, "clock"), "100");
  writeFileSync(join(root, "calls"), "0");
  writeFileSync(
    join(root, "gh"),
    `#!/bin/sh
set -eu
count=$(($(cat "$QUOTA_FIXTURE/calls") + 1))
printf '%s' "$count" > "$QUOTA_FIXTURE/calls"
${
  failure
    ? "exit 3"
    : `case "$count" in
${replies.map((reply, i) => `${i + 1}) printf '%s\\n' '${reply}';;`).join("\n")}
*) exit 4;;
esac`
}
`,
    { mode: 0o755 },
  );
  writeFileSync(join(root, "date"), '#!/bin/sh\ncat "$QUOTA_FIXTURE/clock"\n', {
    mode: 0o755,
  });
  writeFileSync(
    join(root, "sleep"),
    '#!/bin/sh\nprintf "%s" "$(($(cat "$QUOTA_FIXTURE/clock") + $1))" > "$QUOTA_FIXTURE/clock"\n',
    { mode: 0o755 },
  );
  const result = spawnSync("bash", [resolve("scripts/wait-pages-quota.sh")], {
    encoding: "utf8",
    timeout: 5000,
    env: {
      ...process.env,
      PATH: `${root}${delimiter}${process.env.PATH}`,
      QUOTA_FIXTURE: root,
      PAGES_QUOTA_MAX_WAIT_SECONDS: maximum,
    },
  });
  return {
    ...result,
    calls: Number(readFileSync(join(root, "calls"), "utf8")),
    clock: Number(readFileSync(join(root, "clock"), "utf8")),
  };
}

void test("Pages quota preflight proceeds at the threshold without waiting", (t) => {
  const result = fixture(t, ["100\t999"]);
  assert.equal(result.status, 0);
  assert.equal(result.calls, 1);
  assert.equal(result.clock, 100);
});
void test("Pages quota preflight waits until reset plus buffer and rechecks", (t) => {
  const result = fixture(t, ["0\t105", "250\t999"]);
  assert.equal(result.status, 0);
  assert.equal(result.calls, 2);
  assert.equal(result.clock, 110);
  assert.match(result.stdout, /waiting 10 seconds/);
});
void test("Pages quota preflight refuses a reset outside its bounded wait", (t) => {
  const result = fixture(t, ["99\t115"], "10");
  assert.equal(result.status, 1);
  assert.equal(result.clock, 100);
  assert.match(result.stderr, /preserve the current site/);
});
void test("Pages quota preflight rejects malformed/API responses and invalid bounds", (t) => {
  for (const reply of ["invalid", "100\tunknown", "-1\t105"])
    assert.equal(fixture(t, [reply]).status, 1);
  assert.equal(fixture(t, [], "3900", true).status, 3);
  for (const bound of ["0", "3901", "invalid"]) {
    const result = fixture(t, [], bound);
    assert.equal(result.status, 1);
    assert.equal(result.calls, 0);
  }
});
