import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join, resolve } from "node:path";
import test from "node:test";
import type { TestContext } from "node:test";

function fixture(t: TestContext, cold = false) {
  const root = mkdtempSync(join(tmpdir(), "oss-publication-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const repository = join(root, "source");
  const remote = join(root, "remote.git");
  mkdirSync(repository);
  const git = (...args: string[]) =>
    execFileSync(
      "git",
      ["-c", "commit.gpgsign=false", "-C", repository, ...args],
      { encoding: "utf8", stdio: "pipe" },
    ).trim();
  git("init", "--bare", "--initial-branch=main", remote);
  git("init", "--initial-branch=main");
  git("config", "user.name", "Publication fixture");
  git("config", "user.email", "fixture@example.invalid");
  git("commit", "--allow-empty", "-m", "source");
  const source = git("rev-parse", "HEAD");
  git("push", remote, "main");
  let data = "";
  if (!cold) {
    git("switch", "--orphan", "data");
    mkdirSync(join(repository, "snapshots"));
    mkdirSync(join(repository, "checkpoints"));
    writeFileSync(join(repository, "snapshots/example.json"), "old\n");
    writeFileSync(
      join(repository, "checkpoints/example.json"),
      "old checkpoint\n",
    );
    writeFileSync(join(repository, "source-revision.txt"), `${source}\n`);
    git("add", ".");
    git("commit", "-m", "old data");
    data = git("rev-parse", "HEAD");
    git("push", remote, "data");
    git("switch", "main");
  }
  const publicData = join(root, "public");
  const checkpoints = join(root, "private");
  mkdirSync(publicData);
  mkdirSync(checkpoints);
  writeFileSync(join(publicData, "example.json"), "new\n");
  writeFileSync(join(checkpoints, "example.json"), "new checkpoint\n");
  const remoteGit = (...args: string[]) => git("--git-dir", remote, ...args);
  const manualData = (push = true) => {
    git("switch", "data");
    writeFileSync(
      join(repository, "snapshots/example.json"),
      "manual newer data\n",
    );
    git("add", ".");
    git("commit", "-m", "manual update");
    const revision = git("rev-parse", "HEAD");
    if (push) git("push", remote, "data");
    git("switch", "main");
    return revision;
  };
  let execution = 0;
  const run = (
    script = "publish-snapshots.sh",
    extra: NodeJS.ProcessEnv = {},
  ) => {
    const output = join(root, `output-${++execution}`);
    writeFileSync(output, "");
    const result = spawnSync("bash", [resolve("scripts", script)], {
      cwd: repository,
      encoding: "utf8",
      env: {
        ...process.env,
        GH_REPO: "example/repo",
        SNAPSHOT_REMOTE_URL: remote,
        SOURCE_REVISION: source,
        DATA_BASE_REVISION: data,
        DATA_REVISION: data,
        PUBLIC_DATA_DIRECTORY: publicData,
        CHECKPOINT_DIRECTORY: checkpoints,
        RUNNER_TEMP: root,
        GITHUB_OUTPUT: output,
        ...extra,
      },
    });
    const outputs: Record<string, string> = {};
    for (const line of readFileSync(output, "utf8").trim().split("\n")) {
      const [key, value] = line.split("=");
      if (key && value) outputs[key] = value;
    }
    return { ...result, outputs };
  };
  return {
    root,
    repository,
    remote,
    source,
    data,
    git,
    remoteGit,
    manualData,
    run,
  };
}

void test("publishes data based on the restored revision and records exact provenance", (t) => {
  const f = fixture(t);
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.outputs.current, "true");
  assert.equal(result.outputs.revision, f.remoteGit("rev-parse", "data"));
  assert.equal(f.remoteGit("show", "data:snapshots/example.json"), "new");
  assert.equal(
    f.remoteGit("show", "data:checkpoints/example.json"),
    "new checkpoint",
  );
  assert.equal(f.remoteGit("show", "data:source-revision.txt"), f.source);
  assert.equal(f.remoteGit("rev-parse", "data^"), f.data);
});

void test("a source revision superseded during collection cannot publish", (t) => {
  const f = fixture(t);
  f.git("commit", "--allow-empty", "-m", "new source");
  f.git("push", f.remote, "main");
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.outputs.current, "false");
  assert.equal(f.remoteGit("rev-parse", "data"), f.data);
});

void test("a manually merged newer data revision cannot be overwritten by an old collection", (t) => {
  const f = fixture(t);
  const newer = f.manualData();
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.outputs.current, "false");
  assert.equal(f.remoteGit("rev-parse", "data"), newer);
  assert.equal(
    f.remoteGit("show", "data:snapshots/example.json"),
    "manual newer data",
  );
});

void test("the exact push lease preserves a data update racing the final ref check", (t) => {
  const f = fixture(t);
  const newer = f.manualData(false);
  f.git("push", f.remote, `${newer}:refs/heads/racing-data`);
  const binary = execFileSync("which", ["git"], { encoding: "utf8" }).trim();
  const directory = join(f.root, "bin");
  mkdirSync(directory);
  const marker = join(f.root, "race-once");
  writeFileSync(
    join(directory, "git"),
    `#!/usr/bin/env node
const {spawnSync} = require('node:child_process');
const {existsSync,writeFileSync} = require('node:fs');
const args=process.argv.slice(2);
if(args.includes('push') && !existsSync(${JSON.stringify(marker)})) {
  writeFileSync(${JSON.stringify(marker)},'once');
  const update=spawnSync(${JSON.stringify(binary)},['--git-dir',${JSON.stringify(f.remote)},'update-ref','refs/heads/data',${JSON.stringify(newer)},${JSON.stringify(f.data)}],{stdio:'inherit'});
  if(update.status!==0)process.exit(1);
}
const result=spawnSync(${JSON.stringify(binary)},args,{stdio:'inherit'});
process.exit(result.status ?? 1);
`,
    { mode: 0o755 },
  );
  const result = f.run("publish-snapshots.sh", {
    PATH: `${directory}${delimiter}${process.env.PATH ?? ""}`,
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.outputs.current, "false");
  assert.equal(f.remoteGit("rev-parse", "data"), newer);
  assert.equal(
    f.remoteGit("show", "data:snapshots/example.json"),
    "manual newer data",
  );
});

void test("a cold publication safely creates the data branch", (t) => {
  const f = fixture(t, true);
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.outputs.current, "true");
  assert.equal(f.remoteGit("show", "data:snapshots/example.json"), "new");
  assert.equal(f.remoteGit("rev-list", "--count", "data"), "1");
});

void test("an unchanged rerun reuses the existing data commit", (t) => {
  const f = fixture(t);
  const first = f.run();
  assert.equal(first.status, 0, first.stderr);
  const result = f.run("publish-snapshots.sh", {
    DATA_BASE_REVISION: first.outputs.revision,
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.outputs.current, "true");
  assert.equal(result.outputs.revision, first.outputs.revision);
});

void test("Pages accepts artifacts whose source and data revisions are both current", (t) => {
  const f = fixture(t);
  const result = f.run("check-snapshot-revisions.sh");
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.outputs.current, "true");
});

void test("Pages discards artifacts when data advances during the build", (t) => {
  const f = fixture(t);
  f.manualData();
  const result = f.run("check-snapshot-revisions.sh");
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.outputs.current, "false");
});

void test("Pages discards artifacts when source advances during the build", (t) => {
  const f = fixture(t);
  f.git("commit", "--allow-empty", "-m", "new source");
  f.git("push", f.remote, "main");
  const result = f.run("check-snapshot-revisions.sh");
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.outputs.current, "false");
});

void test("a push transport failure fails publication when the data ref is unchanged", (t) => {
  const f = fixture(t);
  const binary = execFileSync("which", ["git"], { encoding: "utf8" }).trim();
  const directory = join(f.root, "failing-bin");
  mkdirSync(directory);
  writeFileSync(
    join(directory, "git"),
    `#!/usr/bin/env node
const {spawnSync}=require('node:child_process');
const args=process.argv.slice(2);
if(args.includes('push')) {console.error('Simulated transport failure');process.exit(128)}
const result=spawnSync(${JSON.stringify(binary)},args,{stdio:'inherit'});
process.exit(result.status ?? 1);
`,
    { mode: 0o755 },
  );
  const result = f.run("publish-snapshots.sh", {
    PATH: `${directory}${delimiter}${process.env.PATH ?? ""}`,
  });
  assert.equal(result.status, 1, result.stderr);
  assert.equal(result.outputs.current, undefined);
  assert.match(result.stderr, /Data push failed without a concurrent update/);
  assert.equal(f.remoteGit("rev-parse", "data"), f.data);
});
