import assert from "node:assert/strict";
import test from "node:test";
import {
  GitHubClient,
  GitHubError,
  normalizeRepository,
  repositoryPath,
} from "../packages/github/src/index.js";
import type {
  ClientOptions,
  FailureReason,
} from "../packages/github/src/index.js";

const path = "/repos/example/project/pulls?per_page=100";
const next =
  "https://api.github.com/repos/example/project/pulls?per_page=100&page=2";
const decode = (value: unknown): number => {
  if (typeof value !== "number") throw new Error("Invalid record");
  return value;
};
const json = (data: unknown, status = 200, headers: HeadersInit = {}) =>
  new Response(JSON.stringify(data), { status, headers });
const reason =
  (expected: FailureReason) =>
  (error: unknown): boolean =>
    error instanceof GitHubError && error.reason === expected;
function scripted(responses: Response[], options: ClientOptions = {}) {
  const calls: { url: string; options: RequestInit | undefined }[] = [];
  let now = 100000;
  const waits: number[] = [];
  const client = new GitHubClient({
    now: () => now,
    sleep: (ms) => {
      waits.push(ms);
      now += ms;
      return Promise.resolve();
    },
    fetch: (input, init) => {
      calls.push({
        url:
          typeof input === "string"
            ? input
            : input instanceof URL
              ? input.href
              : input.url,
        options: init,
      });
      const response = responses.shift();
      if (!response) throw new Error("Unexpected request");
      return Promise.resolve(response);
    },
    ...options,
  });
  return { client, calls, waits };
}

void test("normalizes repository URLs and rejects unsafe inputs before requesting", async () => {
  assert.equal(
    normalizeRepository(" https://github.com/ossf/scorecard/ "),
    "ossf/scorecard",
  );
  assert.equal(repositoryPath("owner/repo.name"), "/repos/owner/repo.name");
  for (const invalid of [
    "https://github.com.evil.test/a/b",
    "http://github.com/a/b",
    "https://user:pass@github.com/a/b",
    "a/..",
    "a/b/issues",
    "https://[broken",
  ])
    assert.throws(() => normalizeRepository(invalid), reason("invalid_input"));
  const { client, calls } = scripted([]);
  await assert.rejects(
    client.get("https://evil.test/repos/a/b"),
    reason("invalid_input"),
  );
  await assert.rejects(client.get("/user"), reason("invalid_input"));
  assert.equal(calls.length, 0);
});

void test("follows pagination links and keeps authenticated headers out of browser requests", async () => {
  const { client, calls } = scripted([
    json([1], 200, { Link: `<${next}>; rel="next", <${next}>; rel="last"` }),
    json([2]),
  ]);
  assert.deepEqual(await client.paginate(path, decode), {
    items: [1, 2],
    pages: 2,
    complete: true,
    error: null,
  });
  assert.equal(calls[1]!.url, next);
  assert.equal(calls[0]!.options!.credentials, "omit");
  assert.equal(calls[0]!.options!.redirect, "manual");
  assert.equal(
    new Headers(calls[0]!.options!.headers).has("Authorization"),
    false,
  );
  assert.equal(client.requestsUsed, 2);
});

void test("preserves completed pages when the next page exceeds the budget", async () => {
  const { client, calls } = scripted(
    [json([1], 200, { Link: `<${next}>; rel="next"` })],
    { maxRequests: 1 },
  );
  const result = await client.paginate(path, decode);
  assert.deepEqual(result.items, [1]);
  assert.equal(result.complete, false);
  assert.equal(result.error?.reason, "budget");
  assert.equal(calls.length, 1);
  assert.throws(
    () => new GitHubClient({ maxRequests: 21 }),
    reason("invalid_input"),
  );
});

void test("counts retry attempts against the same request budget", async () => {
  const { client, calls } = scripted([json({}, 503)], { maxRequests: 1 });
  await assert.rejects(client.get(path), reason("budget"));
  assert.equal(calls.length, 1);
  const retry = scripted([json({}, 503), json([1])]);
  await retry.client.get(path);
  assert.deepEqual(retry.waits, [1000]);
  assert.equal(retry.client.requestsUsed, 2);
});

void test("distinguishes permission-denied 403 from primary and secondary rate limits", async () => {
  const forbidden = scripted([
    json({ message: "Resource not accessible by integration" }, 403),
  ]);
  await assert.rejects(forbidden.client.get(path), reason("forbidden"));
  assert.equal(forbidden.calls.length, 1);
  const primary = scripted([
    json({}, 403, { "X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "160" }),
  ]);
  await assert.rejects(primary.client.get(path), reason("rate_limit"));
  await assert.rejects(primary.client.get(path), reason("rate_limit"));
  assert.equal(primary.calls.length, 1);
  const secondary = scripted([
    json({ message: "You have exceeded a secondary rate limit" }, 403),
  ]);
  await assert.rejects(
    secondary.client.get(path),
    (error: unknown) =>
      reason("rate_limit")(error) && (error as GitHubError).retryAt === 160000,
  );
  assert.equal(secondary.waits.length, 0);
});

void test("obeys Retry-After seconds, HTTP dates and primary reset times", async () => {
  const seconds = scripted([json({}, 429, { "Retry-After": "1" }), json([])]);
  await seconds.client.get(path);
  assert.deepEqual(seconds.waits, [1000]);
  const date = scripted([
    json({}, 429, { "Retry-After": new Date(102000).toUTCString() }),
    json([]),
  ]);
  await date.client.get(path);
  assert.deepEqual(date.waits, [2000]);
  const reset = scripted([
    json({}, 403, { "X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "101" }),
    json([]),
  ]);
  await reset.client.get(path);
  assert.deepEqual(reset.waits, [1000]);
});

void test("bounds repeated rate-limit retries and honors low remaining allowance", async () => {
  const retry = scripted([
    json({}, 429, { "Retry-After": "1" }),
    json({}, 429, { "Retry-After": "1" }),
    json({}, 429, { "Retry-After": "1" }),
  ]);
  await assert.rejects(retry.client.get(path), reason("rate_limit"));
  assert.equal(retry.calls.length, 3);
  const low = scripted([
    json([], 200, { "X-RateLimit-Remaining": "2", "X-RateLimit-Reset": "160" }),
  ]);
  await low.client.get(path);
  await assert.rejects(low.client.get(path), reason("rate_limit"));
  assert.equal(low.calls.length, 1);
  assert.equal(low.client.rateLimit.remaining, 2);
});

void test("retains partial results for malformed pages, links, cycles and foreign origins", async () => {
  for (const second of [
    json({ error: "not an array" }),
    json([2, "invalid"]),
  ]) {
    const { client } = scripted([
      json([1], 200, { Link: `<${next}>; rel="next"` }),
      second,
    ]);
    const result = await client.paginate(path, decode);
    assert.deepEqual(result.items, [1]);
    assert.equal(result.error?.reason, "invalid_response");
  }
  for (const link of [
    "broken",
    `<${next}>; rel="next", <${next}>; rel="next"`,
    `<https://evil.test/repos/a/b>; rel="next"`,
    `<https://api.github.com${path}>; rel="next"`,
  ]) {
    const { client } = scripted([json([1], 200, { Link: link })]);
    const result = await client.paginate(path, decode);
    assert.equal(result.complete, false);
    assert.deepEqual(result.items, [1]);
    assert.equal(client.requestsUsed, 1);
  }
});

void test("serializes calls, including retries, and continues after a failed request", async () => {
  let inFlight = 0;
  let maximum = 0;
  let count = 0;
  const client = new GitHubClient({
    fetch: async () => {
      inFlight++;
      maximum = Math.max(maximum, inFlight);
      await new Promise((resolve) => setTimeout(resolve, 2));
      inFlight--;
      return json([], count++ === 0 ? 404 : 200);
    },
  });
  const results = await Promise.allSettled([
    client.get(path),
    client.get(path),
    client.get(path),
  ]);
  assert.equal(results[0].status, "rejected");
  assert.equal(results[2].status, "fulfilled");
  assert.equal(maximum, 1);
});

void test("follows and counts safe redirects but refuses foreign or opaque redirects", async () => {
  const safe = scripted([
    new Response(null, { status: 301, headers: { Location: next } }),
    json([]),
  ]);
  await safe.client.get(path);
  assert.equal(safe.client.requestsUsed, 2);
  const foreign = scripted([
    new Response(null, {
      status: 302,
      headers: { Location: "https://evil.test/repos/a/b" },
    }),
  ]);
  await assert.rejects(foreign.client.get(path), reason("invalid_input"));
  assert.equal(foreign.calls.length, 1);
  const opaque = new Response(null);
  Object.defineProperty(opaque, "type", { value: "opaqueredirect" });
  await assert.rejects(scripted([opaque]).client.get(path), reason("redirect"));
});

void test("distinguishes timeout, active cancellation, queued cancellation and network failure", async () => {
  const transport: typeof fetch = (_input, init) =>
    new Promise((_resolve, reject) => {
      if (!init?.signal) throw new Error("Missing abort signal");
      init.signal.addEventListener(
        "abort",
        () => reject(new DOMException("Aborted", "AbortError")),
        { once: true },
      );
    });
  await assert.rejects(
    new GitHubClient({ fetch: transport, timeoutMs: 5 }).get(path),
    reason("timeout"),
  );
  const controller = new AbortController();
  const client = new GitHubClient({ fetch: transport });
  const active = client.get(path, controller.signal);
  const queued = client.get(path, controller.signal);
  setTimeout(() => controller.abort(), 2);
  await assert.rejects(active, reason("cancelled"));
  await assert.rejects(queued, reason("cancelled"));
  assert.equal(client.requestsUsed, 1);
  const stopped = new AbortController();
  stopped.abort();
  await assert.rejects(client.get(path, stopped.signal), reason("cancelled"));
  await assert.rejects(
    new GitHubClient({
      fetch: () => Promise.reject(new TypeError("Network unavailable")),
    }).get(path),
    reason("network"),
  );
});

void test("treats invalid JSON and successful empty pages differently", async () => {
  const malformed = scripted([new Response("not json", { status: 200 })]);
  await assert.rejects(malformed.client.get(path), reason("invalid_response"));
  assert.equal(
    (await scripted([json([])]).client.paginate(path, decode)).complete,
    true,
  );
});

void test("keeps transient server cooldowns distinct from quota exhaustion", async () => {
  const { client, calls } = scripted([json({}, 503)], { maxWaitMs: 0 });
  await assert.rejects(client.get(path), reason("http"));
  await assert.rejects(client.get(path), reason("http"));
  assert.equal(calls.length, 1);
});
