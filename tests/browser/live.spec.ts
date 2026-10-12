import { test, expect } from "@playwright/test";
import type { Page, Route } from "@playwright/test";

const headers = {
  "access-control-allow-origin": "*",
  "access-control-allow-headers": "*",
  "access-control-expose-headers":
    "Link,X-RateLimit-Remaining,X-RateLimit-Limit,X-RateLimit-Reset",
  "x-ratelimit-remaining": "55",
  "x-ratelimit-limit": "60",
};
const created = new Date(Date.now() - 2 * 86400000).toISOString();
const responded = new Date(Date.parse(created) + 3600000).toISOString();
const account = (id: number) => ({ id, login: `person-${id}`, type: "User" });
async function fixture(page: Page, count = 2) {
  const calls: string[] = [];
  await page.route("**/data/*.json", (route) =>
    route.fulfill({ status: 404, body: "No saved fixture" }),
  );
  await page.route("https://api.github.com/**", async (route) => {
    if (route.request().method() === "OPTIONS") {
      await route.fulfill({ status: 204, headers });
      return;
    }
    const path = new URL(route.request().url()).pathname;
    calls.push(path);
    const payload: unknown = path.startsWith("/repos/")
      ? { id: 42, full_name: "example/repo", private: false }
      : path === "/repositories/42/pulls"
        ? Array.from({ length: count }, (_, i) => ({
            id: i + 100,
            number: i + 1,
            user: account(i + 1),
            created_at: created,
            draft: false,
            state: "open",
            merged_at: null,
            body: "PRIVATE_BODY_MARKER",
          }))
        : path.endsWith("/reviews")
          ? [
              {
                id: 1,
                user: account(99),
                state: "APPROVED",
                submitted_at: responded,
                body: "PRIVATE_BODY_MARKER",
              },
            ]
          : [];
    await route.fulfill({ json: payload, headers });
  });
  return calls;
}
async function explore(page: Page) {
  await page
    .getByLabel("GitHub repository", { exact: true })
    .fill("example/repo");
  await page.getByRole("button", { name: "Explore repository" }).click();
}

test("initial page makes no API calls and supports root links and validation", async ({
  page,
}) => {
  const calls = await fixture(page);
  await page.goto("./");
  await expect(
    page.getByRole("heading", { name: "The work behind the pull request." }),
  ).toBeVisible();
  expect(calls).toHaveLength(0);
  await page.getByLabel("GitHub repository", { exact: true }).fill("invalid");
  await page.getByRole("button", { name: "Explore repository" }).click();
  await expect(page.getByRole("alert")).toContainText("Enter owner/repository");
  expect(calls).toHaveLength(0);
  await page.getByRole("link", { name: "Methodology", exact: true }).click();
  await expect(page).toHaveURL(/\/methodology\/$/);
  await expect(
    page.getByRole("heading", { name: "A bounded live sample" }),
  ).toBeVisible();
});
test("complete small sample displays denominators, caches, refreshes and clears", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const calls = await fixture(page);
  await page.goto("./");
  await explore(page);
  await expect(page.getByRole("status")).toContainText("Sample complete");
  expect(calls).toHaveLength(8);
  await expect(page.locator("#reviewers")).toHaveText("1");
  await expect(page.locator("#response")).toHaveText("—");
  await expect(page.locator("#response-note")).toContainText(
    "Sample has 2 human responses",
  );
  await expect(page.getByRole("table")).toContainText("100.0%");
  const stored = await page.evaluate(() => JSON.stringify(localStorage));
  expect(stored).not.toContain("PRIVATE_BODY_MARKER");
  await page.reload();
  await expect(page.getByRole("status")).toContainText("cached sample");
  expect(calls).toHaveLength(8);
  await page.getByRole("button", { name: "Refresh live" }).click();
  await expect(page.getByRole("status")).toContainText("Sample complete");
  expect(calls).toHaveLength(16);
  await page.getByRole("button", { name: "Clear cache" }).click();
  await expect(page.getByRole("status")).toContainText("cache cleared");
  expect(errors).toEqual([]);
});
test("request cap produces a partial sample with limited combined review and response coverage", async ({
  page,
}) => {
  const calls = await fixture(page, 12);
  await page.goto("./?repo=example/repo");
  await expect(page.getByRole("status")).toContainText("Partial sample");
  expect(calls).toHaveLength(20);
  await expect(page.locator("#sample-description")).toContainText(
    "8 selected PRs",
  );
  await expect(page.locator("#coverage")).not.toContainText("8/8");
  await expect(page.locator("#coverage")).toContainText("5/8");
  await expect(page.locator("#response")).toHaveText("1.0 h");
  await expect(page.locator("#concentration")).toHaveText("1");
  expect(await page.evaluate(() => localStorage.length)).toBe(0);
});
test("failed refresh preserves a complete cache and labels the visible old sample", async ({
  page,
}) => {
  await fixture(page);
  await page.goto("./?repo=example/repo");
  await expect(page.getByRole("status")).toContainText("Sample complete");
  await page.route("https://api.github.com/**", (route) =>
    route.fulfill({ status: 404, json: { message: "Not Found" }, headers }),
  );
  await page.getByRole("button", { name: "Refresh live" }).click();
  await expect(page.getByRole("alert")).toContainText("not found");
  await expect(page.getByRole("status")).toContainText(
    "Previous visible sample: example/repo",
  );
  await page.reload();
  await expect(page.getByRole("status")).toContainText("cached sample");
});
test("cancel stops a pending query and preserves partial coverage", async ({
  page,
}) => {
  await fixture(page);
  let held: Route | undefined;
  await page.route("https://api.github.com/**/reviews?*", (route) => {
    held = route;
  });
  await page.goto("./?repo=example/repo");
  await expect(page.locator("#sample-description")).toContainText(
    "Partial collection",
  );
  await expect.poll(() => !!held).toBe(true);
  await page.getByRole("button", { name: "Cancel query" }).click();
  await expect(page.getByRole("status")).toContainText("Query cancelled");
  await expect(page.locator("#reviewers")).toHaveText("—");
  await held?.abort();
  expect(await page.evaluate(() => localStorage.length)).toBe(0);
});
for (const failure of ["disabled", "quota"] as const) {
  test(`queries work when local storage is ${failure}`, async ({ page }) => {
    await page.addInitScript((mode) => {
      if (mode === "disabled")
        Object.defineProperty(window, "localStorage", {
          get: () => {
            throw new Error("disabled");
          },
        });
      else
        Storage.prototype.setItem = () => {
          throw new DOMException("Quota exceeded", "QuotaExceededError");
        };
    }, failure);
    await fixture(page);
    await page.goto("./?repo=example/repo");
    await expect(page.getByRole("status")).toContainText(
      "Local cache unavailable",
    );
    await expect(page.locator("#reviewers")).toHaveText("1");
  });
}
test("mobile layout remains within viewport and supports keyboard submission", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await fixture(page);
  await page.goto("./");
  await page
    .getByLabel("GitHub repository", { exact: true })
    .fill("https://github.com/example/repo");
  await page.getByLabel("GitHub repository", { exact: true }).press("Enter");
  await expect(page.getByRole("status")).toContainText("Sample complete");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
});

test("empty successful enumeration has complete sample coverage and insufficient metrics", async ({
  page,
}) => {
  const calls = await fixture(page, 0);
  await page.goto("./?repo=example/repo");
  await expect(page.getByRole("status")).toContainText("Sample complete");
  await expect(page.locator("#sample-description")).toContainText(
    "0 selected PRs",
  );
  await expect(page.locator("#response")).toHaveText("—");
  await expect(page.locator("#coverage")).toContainText("0/0");
  expect(calls).toHaveLength(2);
});
for (const status of [403, 429]) {
  test(`access failure ${status} is explicit and retains no cache`, async ({
    page,
  }) => {
    await page.route("https://api.github.com/**", (route) =>
      route.fulfill({
        status,
        headers: { ...headers, "retry-after": "3600" },
        json: {
          message: status === 403 ? "Forbidden" : "API rate limit exceeded",
        },
      }),
    );
    await page.goto("./?repo=example/repo");
    await expect(page.getByRole("alert")).toContainText(
      status === 403 ? "denied access" : "rate limit",
    );
    await expect(page.locator("#results")).toBeHidden();
    expect(await page.evaluate(() => localStorage.length)).toBe(0);
  });
}
test("switching repositories prevents an older in-flight query replacing newer results", async ({
  page,
}) => {
  await fixture(page);
  let held: Route | undefined;
  await page.route("https://api.github.com/repos/old/repo", (route) => {
    held = route;
  });
  await page.goto("./?repo=old/repo");
  await expect.poll(() => !!held).toBe(true);
  await page.getByRole("button", { name: "ossf/scorecard" }).click();
  await expect(page.getByRole("status")).toContainText("Sample complete");
  await held?.fulfill({
    json: { id: 123, full_name: "old/repo", private: false },
    headers,
  });
  await expect(page.locator("#repo-name")).toHaveText("example/repo");
  await expect(page).toHaveURL(/repo=ossf%2Fscorecard/);
});

test("saved complete window explains its scope and response denominator without API calls", async ({
  page,
}) => {
  const { readFileSync } = await import("node:fs");
  const { parsePublicSnapshot } =
    await import("../../apps/web/src/lib/live.js");
  const raw: unknown = JSON.parse(
    readFileSync(
      new URL("../fixtures/catalog-snapshot.json", import.meta.url),
      "utf8",
    ),
  );
  const saved = parsePublicSnapshot(raw, "example/repo")!;
  saved.result.repository.full_name = "kubernetes/kubernetes";
  const calls = await fixture(page);
  await page.route("**/data/kubernetes--kubernetes.json", (route) =>
    route.fulfill({ json: saved }),
  );
  await page.goto("./?repo=kubernetes/kubernetes");
  await expect(page.getByRole("status")).toContainText("No GitHub API calls");
  await expect(page.locator("#sample-description")).toContainText(
    "21 eligible PRs",
  );
  await expect(page.locator("#sample-description")).toContainText(
    "PR ages at collection: 1.0 days–67.0 days",
  );
  await expect(page.locator("#sample-description")).toContainText(
    "90-day window",
  );
  await expect(page.locator("#sample-description")).toContainText(
    "All eligible PRs created within this window",
  );
  await expect(page.locator("#sample-scope")).toContainText(
    "Metrics cover all eligible human-authored, non-draft PRs",
  );
  await expect(page.locator("#sample-scope")).toContainText(
    "Reviews on PRs created before the window are outside this collection",
  );
  await expect(page.locator("#sample-scope")).toContainText(
    "Refresh live replaces this view with a smaller sample",
  );
  await expect(page.locator("#coverage-notes")).toContainText(
    "GitHub pagination is not an atomic historical snapshot",
  );
  await expect(page.locator("#coverage-notes")).toContainText(
    "enumerated through GitHub PR pages",
  );
  await expect(page.locator("#response-note")).toContainText(
    "14 responded PRs",
  );
  expect(calls).toHaveLength(0);
  saved.result.first_response.median_ms = {
    value: null,
    status: "insufficient_data",
  };
  saved.result.first_response.responded_prs = 3;
  saved.result.first_response.unanswered_prs = 18;
  await page.reload();
  await expect(page.locator("#response-note")).toContainText(
    "Creation window has 3 human responses",
  );
  await expect(page.locator("#response-note")).toContainText(
    "At least 5 are needed",
  );
  expect(calls).toHaveLength(0);
});

test("saved catalog sample loads without GitHub calls and refresh is explicit", async ({
  page,
}) => {
  const { readFileSync } = await import("node:fs");
  const { parsePublicSnapshot } =
    await import("../../apps/web/src/lib/live.js");
  const raw: unknown = JSON.parse(
    readFileSync(
      new URL("../fixtures/live-snapshot.json", import.meta.url),
      "utf8",
    ),
  );
  const saved = parsePublicSnapshot(raw, "example/repo")!;
  saved.result.repository.full_name = "ossf/allstar";
  const calls = await fixture(page);
  await page.route("**/data/ossf--allstar.json", (route) =>
    route.fulfill({ json: saved }),
  );
  await page.goto("./?repo=ossf/allstar");
  await expect(page.getByRole("status")).toContainText("No GitHub API calls");
  await expect(page.locator("#freshness")).toContainText("Saved snapshot");
  expect(calls).toHaveLength(0);
  await page.getByRole("button", { name: "Refresh live" }).click();
  await expect(page.getByRole("status")).toContainText("Sample complete");
  expect(calls.length).toBeGreaterThan(0);
});
test("missing saved data never causes an automatic GitHub query", async ({
  page,
}) => {
  const calls = await fixture(page);
  await page.route("**/data/ossf--allstar.json", (route) =>
    route.fulfill({ status: 404, body: "missing" }),
  );
  await page.goto("./?repo=ossf/allstar");
  await expect(page.getByRole("status")).toContainText("not available yet");
  expect(calls).toHaveLength(0);
});
test("uncataloged repository offers the historical contribution guide", async ({
  page,
}) => {
  await fixture(page);
  await page.goto("./?repo=example/repo");
  await expect(page.getByRole("status")).toContainText("Sample complete");
  await expect(page.locator("#contribution")).toBeVisible();
  await page.getByRole("link", { name: "Read the contribution guide" }).click();
  await expect(page).toHaveURL(/\/contributing\/$/);
  await expect(
    page.getByRole("heading", { name: "Open a catalog pull request" }),
  ).toBeVisible();
});

test("custom domain root serves stylesheets and page navigation", async ({
  page,
}) => {
  const stylesheet = page.waitForResponse(
    (response) =>
      response.url().includes("/_astro/") && response.url().endsWith(".css"),
  );
  await page.goto("./methodology/");
  const response = await stylesheet;
  expect(response.status()).toBe(200);
  expect(response.headers()["content-type"]).toContain("text/css");
  await expect(page.locator('link[rel="stylesheet"]')).toHaveAttribute(
    "href",
    /^\/_astro\//,
  );
  await page
    .getByRole("link", { name: "Explore repositories", exact: true })
    .click();
  await expect(page).toHaveURL("http://127.0.0.1:4322/");
  await page.goto("./contributing/");
  await expect(page).toHaveURL(/\/contributing\/$/);
  await expect(page.locator('link[rel="stylesheet"]')).toHaveAttribute(
    "href",
    /^\/_astro\//,
  );
});

test("Prow command reviews render their sources without duplicate units or stored bodies", async ({
  page,
}) => {
  await fixture(page);
  await page.route(
    "https://api.github.com/repositories/42/issues/*/comments*",
    (route) =>
      route.fulfill({
        headers,
        json: [
          {
            id: 2,
            user: account(99),
            created_at: responded,
            body: "/lgtm\nPRIVATE_BODY_MARKER",
          },
          {
            id: 3,
            user: account(88),
            created_at: responded,
            body: "/approve\nPRIVATE_BODY_MARKER",
          },
          {
            id: 4,
            user: account(77),
            created_at: responded,
            body: "> /lgtm\n```\n/approve\n```",
          },
        ],
      }),
  );
  await page.goto("./?repo=example/repo");
  await expect(page.getByRole("status")).toContainText("Sample complete");
  await expect(page.locator("#reviewers")).toHaveText("2");
  await expect(page.locator("#review-units")).toHaveText("4 review units");
  await expect(page.locator("#review-sources")).toContainText(
    "2 GitHub review units · 4 Prow command units",
  );
  await expect(page.getByRole("table")).toContainText("GitHub: 2 · Prow: 2");
  await expect(page.locator("#freshness")).toContainText("Methodology 1.1.0");
  expect(await page.evaluate(() => JSON.stringify(localStorage))).not.toContain(
    "PRIVATE_BODY_MARKER",
  );
  await page.setViewportSize({ width: 360, height: 780 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
});
test("legacy saved data is visibly labeled while requiring no GitHub requests", async ({
  page,
}) => {
  const { readFileSync } = await import("node:fs");
  const { parsePublicSnapshot } =
    await import("../../apps/web/src/lib/live.js");
  const raw: unknown = JSON.parse(
    readFileSync(
      new URL("../fixtures/live-snapshot.json", import.meta.url),
      "utf8",
    ),
  );
  const saved = parsePublicSnapshot(raw, "example/repo")!;
  saved.result.repository.full_name = "ossf/allstar";
  saved.result.methodology_version = "1.0.0";
  delete saved.result.review_activity.sources;
  for (const row of saved.result.review_activity.distribution)
    delete row.sources;
  const calls = await fixture(page);
  await page.route("**/data/ossf--allstar.json", (route) =>
    route.fulfill({ json: saved }),
  );
  await page.goto("./?repo=ossf/allstar");
  await expect(page.getByRole("status")).toContainText("No GitHub API calls");
  await expect(page.locator("#review-sources")).toContainText(
    "Legacy snapshot: Prow commands were not included",
  );
  await expect(page.locator("#freshness")).toContainText("Methodology 1.0.0");
  expect(calls).toHaveLength(0);
});

test("metric explanations support mouse, keyboard and touch", async ({
  page,
}) => {
  await fixture(page);
  await page.goto("./?repo=example/repo");
  await expect(page.getByRole("status")).toContainText("Sample complete");
  const help = page.getByRole("button", { name: "Explain Share", exact: true });
  const tooltip = page.locator("#metric-help-7");
  await help.hover();
  await expect(tooltip).toBeVisible();
  await expect(tooltip).toContainText("2 of 8 units = 25%");
  await page.mouse.move(0, 0);
  await expect(tooltip).toBeHidden();
  await help.focus();
  await expect(tooltip).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(tooltip).toBeHidden();
  await help.blur();
  await page.setViewportSize({ width: 360, height: 780 });
  await help.dispatchEvent("click");
  await expect(tooltip).toBeVisible();
  const bounds = await tooltip.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(360);
});

test("pending full-window progress preserves a visibly bounded legacy sample", async ({
  page,
}) => {
  const { readFileSync } = await import("node:fs");
  const { parsePublicSnapshot } =
    await import("../../apps/web/src/lib/live.js");
  const raw: unknown = JSON.parse(
    readFileSync(
      new URL("../fixtures/catalog-snapshot.json", import.meta.url),
      "utf8",
    ),
  );
  const saved = parsePublicSnapshot(raw, "example/repo")!;
  saved.policy = "three-age-bands-three-hundred-human-prs-90-days-v1";
  saved.result.repository.full_name = "kubernetes/kubernetes";
  const calls = await fixture(page);
  await page.route("**/data/kubernetes--kubernetes.json", (route) =>
    route.fulfill({ json: saved }),
  );
  await page.route("**/data/index.json", (route) =>
    route.fulfill({
      json: {
        schema_version: 1,
        attempted_at: saved.result.collected_at,
        entries: [
          {
            repository: "kubernetes/kubernetes",
            status: "pending",
            reason: "budget",
            requests: 1300,
            progress: {
              window: saved.result.window,
              enumerated_prs: 1500,
              eligible_prs: 1200,
              covered_prs: 400,
              enumeration_complete: true,
            },
          },
        ],
      },
    }),
  );
  await page.goto("./?repo=kubernetes/kubernetes");
  await expect(page.locator("#collection-progress")).toContainText(
    "1200 eligible, 400 with complete review and comment streams",
  );
  await expect(page.locator("#collection-progress")).toContainText(
    "previous saved result remains visible",
  );
  await expect(page.locator("#sample-description")).toContainText(
    "21 selected PRs",
  );
  await expect(page.locator("#sample-description")).toContainText(
    "Up to 100 PRs from each",
  );
  expect(calls).toHaveLength(0);
});

test("uncatalogued deep links display a prominent catalog contribution guide", async ({
  page,
}) => {
  await fixture(page);
  await page.goto("./?repo=sigstore%2Frekor");
  await expect(page.locator("#contribution")).toBeVisible();
  await expect(page.locator("#contribution")).toContainText(
    "Want more data for this repository?",
  );
  await expect(page.locator("#contribution")).toContainText(
    "pull request to add this repository",
  );
  await page
    .locator("#contribution")
    .getByRole("link", { name: "Read the contribution guide" })
    .click();
  await expect(page).toHaveURL(/\/contributing\/$/);
  await expect(
    page.getByRole("heading", { name: "Open a catalog pull request" }),
  ).toBeVisible();
});
