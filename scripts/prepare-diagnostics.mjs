import { copyFileSync, mkdirSync } from "node:fs";
import { resolve } from "node:path";
const root = resolve(import.meta.dirname, "..");
const site = resolve(root, "apps/web/dist");
mkdirSync(resolve(site, "diagnostics"), { recursive: true });
mkdirSync(resolve(site, "packages/github/dist"), { recursive: true });
for (const name of ["github.html", "github.js"])
  copyFileSync(
    resolve(root, "diagnostics", name),
    resolve(site, "diagnostics", name),
  );
copyFileSync(
  resolve(root, "packages/github/dist/index.js"),
  resolve(site, "packages/github/dist/index.js"),
);
