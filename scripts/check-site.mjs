import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import process from "node:process";
import { Ajv } from "ajv";
import addFormats from "ajv-formats";

const root = resolve(import.meta.dirname, "..");
const directoryOption = process.argv.indexOf("--directory");
const site = resolve(
  root,
  directoryOption >= 0 ? process.argv[directoryOption + 1] : "apps/web/dist",
);
const read = (path) => JSON.parse(readFileSync(path, "utf8"));
const ajv = new Ajv({ strict: true });
addFormats(ajv);
const names = [
  "analysis-result",
  "repository-snapshot",
  "snapshot-index",
  "build-manifest",
];
for (const name of names)
  ajv.addSchema(read(resolve(root, `schemas/${name}.schema.json`)));
const validate = (name, value) => {
  if (
    !ajv.validate(
      `https://cpanato.github.io/oss-maintainer-insights/schemas/${name}.schema.json`,
      value,
    )
  )
    throw new Error(`Invalid ${name} artifact`);
};
if (process.argv.includes("--manifest")) {
  const manifest = {
    schema_version: 1,
    source_revision: process.env.SOURCE_REVISION,
    data_revision: process.env.DATA_REVISION,
  };
  validate("build-manifest", manifest);
  writeFileSync(
    resolve(site, "build-manifest.json"),
    JSON.stringify(manifest, null, 2) + "\n",
  );
}
for (const entry of readdirSync(site, {
  recursive: true,
  withFileTypes: true,
})) {
  if (entry.isSymbolicLink())
    throw new Error("Website artifacts must not contain symlinks");
  const path = resolve(entry.parentPath, entry.name);
  if (
    path.includes("/checkpoints/") ||
    entry.name === "checkpoints" ||
    entry.name === ".cache" ||
    entry.name === "source-revision.txt"
  )
    throw new Error("Internal collector state in website artifact");
  if (!entry.isFile() || !entry.name.endsWith(".json")) continue;
  validate(
    entry.name === "build-manifest.json"
      ? "build-manifest"
      : entry.name === "index.json"
        ? "snapshot-index"
        : "repository-snapshot",
    read(path),
  );
}
process.stdout.write(
  "Public website JSON and artifact boundaries validated.\n",
);
