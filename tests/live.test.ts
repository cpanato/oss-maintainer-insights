import assert from "node:assert/strict";
import { test } from "node:test";
import { GitHubClient } from "../packages/github/src/index.js";
import { collectLive, LiveCache } from "../apps/web/src/lib/live.js";
import type { StorageLike } from "../apps/web/src/lib/live.js";

const observed = Date.parse("2026-10-12T12:00:00Z");
const user = (id: number, type = "User") => ({ id, login: `user-${id}`, type });
const pr = (number: number) => ({
  id: number + 100,
  number,
  user: user(number),
  created_at: "2026-10-10T12:00:00Z",
  draft: false,
  state: "open",
  merged_at: null,
  body: "DO NOT RETAIN",
  title: "DO NOT RETAIN",
});
const event = (id: number) => ({
  id,
  user: user(900),
  state: "APPROVED",
  submitted_at: "2026-10-10T13:00:00Z",
  created_at: "2026-10-10T13:00:00Z",
  body: "DO NOT RETAIN",
});
function client(
  pulls: unknown[],
  stream: (path: string) => unknown[] = () => [event(1)],
  maxRequests = 20,
) {
  const paths: string[] = [];
  const transport: typeof fetch = (input) => {
    const url = new URL(input instanceof Request ? input.url : input);
    paths.push(url.pathname);
    const data =
      url.pathname === "/repos/example/repo"
        ? { id: 42, full_name: "example/repo", private: false }
        : url.pathname === "/repositories/42/pulls"
          ? pulls
          : stream(url.pathname);
    return Promise.resolve(
      new Response(JSON.stringify(data), {
        headers: { "content-type": "application/json" },
      }),
    );
  };
  return { client: new GitHubClient({ fetch: transport, maxRequests }), paths };
}
function memory(): StorageLike & { values: Map<string, string> } {
  const values = new Map<string, string>();
  return {
    values,
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => {
      values.set(key, value);
    },
    removeItem: (key) => {
      values.delete(key);
    },
  };
}
void test("live selection excludes drafts/bots/deleted authors, selects newest eight, and prioritizes reviews", async () => {
  const pulls = Array.from({ length: 12 }, (_, index) => pr(index + 1));
  const api = client([
    ...pulls,
    { ...pr(40), draft: true },
    { ...pr(41), user: user(41, "Bot") },
    { ...pr(42), user: null },
    { ...pr(43), created_at: "2025-01-01T00:00:00Z" },
  ]);
  const snapshot = await collectLive("example/repo", {
    client: api.client,
    now: () => observed,
  });
  assert.equal(snapshot.selection.selected, 8);
  assert.equal(snapshot.selection.eligible_on_page, 12);
  assert.equal(snapshot.selection.truncated, true);
  assert.deepEqual(
    snapshot.input.pull_requests.map((item) => item.id),
    [112, 111, 110, 109, 108, 107, 106, 105],
  );
  assert.equal(snapshot.requests, 20);
  assert.equal(snapshot.complete, false);
  assert.equal(snapshot.result.review_activity.covered_prs, 8);
  assert.equal(snapshot.result.first_response.covered_prs, 5);
  assert.ok(api.paths.slice(2, 10).every((path) => path.endsWith("/reviews")));
  assert.ok(!JSON.stringify(snapshot).includes("DO NOT RETAIN"));
});
void test("pending, duplicate, self, bot and deleted-actor reviews do not inflate participation", async () => {
  const api = client([pr(1)], (path) =>
    path.endsWith("/reviews")
      ? [
          event(1),
          event(1),
          { id: 2, state: "PENDING", submitted_at: null },
          { ...event(3), user: user(1) },
          { ...event(4), user: user(4, "Bot") },
          { ...event(5), user: { id: 10137, login: "ghost", type: "User" } },
        ]
      : [],
  );
  const snapshot = await collectLive("example/repo", {
    client: api.client,
    now: () => observed,
  });
  assert.equal(snapshot.complete, true);
  assert.equal(snapshot.result.review_activity.review_units, 1);
  assert.equal(snapshot.result.first_response.responded_prs, 1);
  assert.equal(snapshot.result.first_response.median_ms.value, null);
});
void test("a missing comment stream prevents an unanswered designation", async () => {
  const api = client([pr(1)], () => [], 3);
  const snapshot = await collectLive("example/repo", {
    client: api.client,
    now: () => observed,
  });
  assert.equal(snapshot.result.review_activity.covered_prs, 1);
  assert.equal(snapshot.result.first_response.covered_prs, 0);
  assert.equal(snapshot.result.first_response.unanswered_prs, 0);
  assert.equal(snapshot.result.first_response.coverage, "unavailable");
});
void test("cancellation retains normalized partial data", async () => {
  const api = client([pr(1)]);
  const controller = new AbortController();
  const snapshot = await collectLive("example/repo", {
    client: api.client,
    now: () => observed,
    signal: controller.signal,
    onProgress: () => controller.abort(),
  });
  assert.equal(snapshot.complete, false);
  assert.equal(snapshot.requests, 2);
  assert.equal(snapshot.result.review_activity.coverage, "unavailable");
});
void test("cache uses aliases and versions, expires, recomputes aggregates, and preserves complete entries", async () => {
  const api = client([pr(1)]);
  const snapshot = await collectLive("example/repo", {
    client: api.client,
    now: () => observed,
  });
  const store = memory();
  let clock = observed;
  const cache = new LiveCache(store, () => clock);
  assert.equal(cache.write("old/repo", snapshot), true);
  assert.equal(cache.read("OLD/repo")?.input.repository.id, 42);
  assert.equal(
    cache.write("example/repo", { ...snapshot, complete: false }),
    false,
  );
  assert.equal(cache.read("example/repo")?.complete, true);
  const key = [...store.values.keys()].find((key) => key.includes("repo:42"))!;
  const tampered = structuredClone(snapshot);
  tampered.result.review_activity.participants = 999;
  store.setItem(
    key,
    JSON.stringify({ version: 1, saved_at: observed, snapshot: tampered }),
  );
  assert.equal(
    cache.read("example/repo")?.result.review_activity.participants,
    1,
  );
  clock += 3600000;
  assert.equal(cache.read("example/repo"), null);
  clock = observed;
  tampered.input.pull_requests[0]!.reviews[0] = {
    ...tampered.input.pull_requests[0]!.reviews[0]!,
    body: "raw body",
  } as (typeof tampered.input.pull_requests)[0]["reviews"][number];
  store.setItem(
    key,
    JSON.stringify({ version: 1, saved_at: observed, snapshot: tampered }),
  );
  assert.equal(cache.read("example/repo"), null);
  cache.clear("example/repo");
  assert.equal(cache.read("old/repo"), null);
});
void test("disabled and quota-limited storage never prevents collection", async () => {
  const api = client([]);
  const snapshot = await collectLive("example/repo", {
    client: api.client,
    now: () => observed,
  });
  const unavailable = new LiveCache(null);
  assert.equal(unavailable.read("example/repo"), null);
  assert.equal(unavailable.write("example/repo", snapshot), false);
  const blocked = new LiveCache({
    getItem: () => {
      throw Error("disabled");
    },
    setItem: () => {
      throw Error("quota");
    },
    removeItem: () => {
      throw Error("disabled");
    },
  });
  assert.equal(blocked.read("example/repo"), null);
  assert.equal(blocked.write("example/repo", snapshot), false);
  assert.equal(blocked.clear("example/repo"), false);
});
void test("paginated reviews and retries share the same query budget", async () => {
  let attempts = 0;
  const transport: typeof fetch = (input) => {
    const url = new URL(input instanceof Request ? input.url : input);
    attempts++;
    if (attempts === 3)
      return Promise.resolve(new Response("{}", { status: 503 }));
    const metadata = url.pathname.startsWith("/repos/");
    const list = url.pathname === "/repositories/42/pulls";
    const data = metadata
      ? { id: 42, full_name: "example/repo", private: false }
      : list
        ? [pr(1)]
        : [event(attempts)];
    return Promise.resolve(
      new Response(JSON.stringify(data), {
        headers:
          !metadata && !list
            ? {
                link: `<https://api.github.com/repositories/42/pulls/1/reviews?per_page=100&page=${attempts}>; rel="next"`,
              }
            : {},
      }),
    );
  };
  const api = new GitHubClient({
    fetch: transport,
    sleep: () => Promise.resolve(),
    maxRequests: 6,
  });
  const snapshot = await collectLive("example/repo", {
    client: api,
    now: () => observed,
  });
  assert.equal(attempts, 6);
  assert.equal(snapshot.requests, 6);
  assert.equal(snapshot.input.pull_requests[0]?.reviews.length, 3);
  assert.equal(snapshot.result.review_activity.covered_prs, 0);
  assert.equal(snapshot.result.review_activity.review_units, 0);
  assert.equal(snapshot.failures[0]?.reason, "budget");
});
void test("browser acquisition matches the shared Go snapshot fixture", async () => {
  const { readFileSync } = await import("node:fs");
  const source = JSON.parse(
    readFileSync(new URL("./fixtures/live-api.json", import.meta.url), "utf8"),
  ) as {
    observed_at: string;
    repository: string;
    metadata: unknown;
    pull_requests: unknown[];
    reviews: unknown[];
    issue_comments: unknown[];
    review_comments: unknown[];
  };
  const transport: typeof fetch = (input) => {
    const path = new URL(input instanceof Request ? input.url : input).pathname;
    const data = path.startsWith("/repos/")
      ? source.metadata
      : path === "/repositories/42/pulls"
        ? source.pull_requests
        : path.endsWith("/reviews")
          ? source.reviews
          : path.includes("/issues/")
            ? source.issue_comments
            : source.review_comments;
    return Promise.resolve(new Response(JSON.stringify(data)));
  };
  const { input, ...publicSnapshot } = await collectLive(source.repository, {
    client: new GitHubClient({ fetch: transport }),
    now: () => Date.parse(source.observed_at),
  });
  assert.equal(input.mode, "live_sample");
  assert.deepEqual(
    publicSnapshot,
    JSON.parse(
      readFileSync(
        new URL("./fixtures/live-snapshot.json", import.meta.url),
        "utf8",
      ),
    ) as unknown,
  );
});
void test("cache rejects an entry when the explicit bot policy changes", async () => {
  const api = client([pr(1)]);
  const snapshot = await collectLive("example/repo", {
    client: api.client,
    now: () => observed,
  });
  const cache = new LiveCache(memory(), () => observed);
  cache.write("example/repo", snapshot);
  assert.ok(cache.read("example/repo", []));
  assert.equal(cache.read("example/repo", [900]), null);
});
