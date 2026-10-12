export const API_ORIGIN = "https://api.github.com";
export const API_VERSION = "2022-11-28";
export type FailureReason =
  | "invalid_input"
  | "budget"
  | "rate_limit"
  | "forbidden"
  | "not_found"
  | "timeout"
  | "cancelled"
  | "network"
  | "http"
  | "invalid_response"
  | "redirect";

export class GitHubError extends Error {
  constructor(
    public readonly reason: FailureReason,
    message: string,
    public readonly status: number | null = null,
    public readonly retryAt: number | null = null,
  ) {
    super(message);
    this.name = "GitHubError";
  }
}

export interface RateLimit {
  remaining: number | null;
  limit: number | null;
  resetAt: number | null;
}
export interface Page {
  data: unknown;
  headers: Headers;
  url: string;
}
export interface Pagination<T> {
  items: T[];
  complete: boolean;
  pages: number;
  error: GitHubError | null;
}
export interface ClientOptions {
  fetch?: typeof fetch;
  now?: () => number;
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>;
  maxRequests?: number;
  timeoutMs?: number;
  maxRetries?: number;
  maxWaitMs?: number;
  minimumRemaining?: number;
}

export function normalizeRepository(input: string): string {
  let value = input.trim();
  if (/^https?:\/\//i.test(value)) {
    let url: URL;
    try {
      url = new URL(value);
    } catch {
      throw new GitHubError("invalid_input", "Invalid repository URL");
    }
    if (
      url.protocol !== "https:" ||
      url.hostname !== "github.com" ||
      url.port ||
      url.username ||
      url.password
    )
      throw new GitHubError(
        "invalid_input",
        "Use a public github.com repository URL",
      );
    value = url.pathname.replace(/^\//, "").replace(/\/$/, "");
  }
  const parts = value.split("/");
  const owner = parts[0] ?? "";
  const repo = parts[1] ?? "";
  if (
    parts.length !== 2 ||
    !/^[a-z\d](?:[a-z\d-]{0,37}[a-z\d])?$/i.test(owner) ||
    !/^[a-z\d_.-]{1,100}$/i.test(repo) ||
    repo === "." ||
    repo === ".."
  )
    throw new GitHubError(
      "invalid_input",
      "Enter owner/repo or a GitHub repository URL",
    );
  return `${owner}/${repo}`;
}

export function repositoryPath(input: string): string {
  return `/repos/${normalizeRepository(input).split("/").map(encodeURIComponent).join("/")}`;
}

function apiURL(input: string): URL {
  let url: URL;
  try {
    url = new URL(input, API_ORIGIN);
  } catch {
    throw new GitHubError("invalid_input", "Invalid GitHub API URL");
  }
  if (
    url.origin !== API_ORIGIN ||
    url.username ||
    url.password ||
    url.hash ||
    !/^\/(repos|repositories)\//.test(url.pathname)
  )
    throw new GitHubError(
      "invalid_input",
      "Requests must use GitHub repository REST endpoints",
    );
  return url;
}

function nextURL(header: string | null): string | null {
  if (!header) return null;
  let found = false;
  let next: string | null = null;
  for (const match of header.matchAll(/<([^>]+)>([^<]*)/g)) {
    const relation = /;\s*rel\s*=\s*(?:"([^"]+)"|([^;\s,]+))/.exec(match[2]!);
    if (!relation)
      throw new GitHubError("invalid_response", "Malformed pagination header");
    found = true;
    if ((relation[1] ?? relation[2] ?? "").split(/\s+/).includes("next")) {
      if (next !== null)
        throw new GitHubError(
          "invalid_response",
          "Ambiguous pagination header",
        );
      next = match[1]!;
    }
  }
  if (!found)
    throw new GitHubError("invalid_response", "Malformed pagination header");
  return next;
}

function headerNumber(headers: Headers, key: string): number | null {
  const raw = headers.get(key);
  if (raw === null || !/^\d+$/.test(raw)) return null;
  const value = Number(raw);
  return Number.isSafeInteger(value) ? value : null;
}

function cancelled(): GitHubError {
  return new GitHubError("cancelled", "Query cancelled");
}
function wait(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(cancelled());
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      reject(cancelled());
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

/** One instance owns a query's complete request budget, including redirects and retries. */
export class GitHubClient {
  private readonly transport: typeof fetch;
  private readonly now: () => number;
  private readonly sleep: (ms: number, signal?: AbortSignal) => Promise<void>;
  private readonly policy: Required<
    Omit<ClientOptions, "fetch" | "now" | "sleep">
  >;
  private queue: Promise<void> = Promise.resolve();
  private used = 0;
  private blockedUntil = 0;
  private blockedReason: FailureReason = "rate_limit";
  private rate: RateLimit = { remaining: null, limit: null, resetAt: null };

  constructor(options: ClientOptions = {}) {
    this.transport = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.now = options.now ?? Date.now;
    this.sleep = options.sleep ?? wait;
    this.policy = {
      maxRequests: options.maxRequests ?? 20,
      timeoutMs: options.timeoutMs ?? 10000,
      maxRetries: options.maxRetries ?? 2,
      maxWaitMs: options.maxWaitMs ?? 2000,
      minimumRemaining: options.minimumRemaining ?? 2,
    };
    for (const [key, value] of Object.entries(this.policy))
      if (
        !Number.isSafeInteger(value) ||
        value < 0 ||
        (key === "timeoutMs" && value === 0)
      )
        throw new GitHubError("invalid_input", `Invalid policy: ${key}`);
    if (this.policy.maxRequests > 20)
      throw new GitHubError("invalid_input", "Live requests cannot exceed 20");
  }

  get requestsUsed(): number {
    return this.used;
  }
  get rateLimit(): RateLimit {
    return { ...this.rate };
  }

  get(input: string, signal?: AbortSignal): Promise<Page> {
    const result = this.queue.then(() => this.request(input, signal));
    this.queue = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  }

  async paginate<T>(
    input: string,
    decode: (value: unknown) => T,
    signal?: AbortSignal,
  ): Promise<Pagination<T>> {
    const items: T[] = [];
    const seen = new Set<string>();
    let pages = 0;
    let next: string | null = input;
    try {
      while (next !== null) {
        const normalized = apiURL(next).href;
        if (seen.has(normalized))
          throw new GitHubError(
            "invalid_response",
            "Pagination cycle detected",
          );
        seen.add(normalized);
        const page = await this.get(normalized, signal);
        if (!Array.isArray(page.data))
          throw new GitHubError(
            "invalid_response",
            "Expected a paginated JSON array",
          );
        // Validate a full page before committing any records from that page.
        const records: T[] = (page.data as unknown[]).map(decode);
        items.push(...records);
        pages++;
        next = nextURL(page.headers.get("link"));
      }
      return { items, pages, complete: true, error: null };
    } catch (error) {
      return {
        items,
        pages,
        complete: false,
        error:
          error instanceof GitHubError
            ? error
            : new GitHubError("invalid_response", "Invalid response record"),
      };
    }
  }

  private async request(input: string, signal?: AbortSignal): Promise<Page> {
    let url = apiURL(input);
    let retries = 0;
    const redirects = new Set<string>();
    while (true) {
      if (signal?.aborted) throw cancelled();
      if (this.used >= this.policy.maxRequests)
        throw new GitHubError("budget", "Query request budget exhausted");
      const delay = this.blockedUntil - this.now();
      if (delay > 0) {
        if (delay > this.policy.maxWaitMs)
          throw new GitHubError(
            this.blockedReason,
            "Retry after the GitHub cooldown",
            null,
            this.blockedUntil,
          );
        await this.sleep(delay, signal);
        if (signal?.aborted) throw cancelled();
      }
      if (
        this.rate.remaining !== null &&
        this.rate.remaining <= this.policy.minimumRemaining
      ) {
        if (this.rate.resetAt !== null && this.now() >= this.rate.resetAt)
          this.rate = { remaining: null, limit: null, resetAt: null };
        else
          throw new GitHubError(
            "rate_limit",
            "Remaining GitHub allowance is low",
            null,
            this.rate.resetAt,
          );
      }
      const controller = new AbortController();
      let timedOut = false;
      const timer = setTimeout(() => {
        timedOut = true;
        controller.abort();
      }, this.policy.timeoutMs);
      const onAbort = () => controller.abort();
      signal?.addEventListener("abort", onAbort, { once: true });
      this.used++;
      let response: Response;
      let body: unknown = null;
      try {
        response = await this.transport(url.href, {
          method: "GET",
          credentials: "omit",
          redirect: "manual",
          headers: {
            Accept: "application/vnd.github+json",
            "X-GitHub-Api-Version": API_VERSION,
          },
          signal: controller.signal,
        });
        if (response.status === 200 && response.type !== "opaqueredirect")
          body = await response.json();
        else if (response.status === 403 || response.status === 429) {
          try {
            body = await response.json();
          } catch {
            body = null;
          }
        }
      } catch (error) {
        if (signal?.aborted) throw cancelled();
        if (timedOut)
          throw new GitHubError("timeout", "GitHub request timed out");
        if (error instanceof SyntaxError)
          throw new GitHubError(
            "invalid_response",
            "Invalid GitHub JSON response",
          );
        throw new GitHubError("network", "GitHub request failed");
      } finally {
        clearTimeout(timer);
        signal?.removeEventListener("abort", onAbort);
      }
      if (signal?.aborted) throw cancelled();
      if (timedOut)
        throw new GitHubError("timeout", "GitHub request timed out");
      this.rate = {
        remaining: headerNumber(response.headers, "x-ratelimit-remaining"),
        limit: headerNumber(response.headers, "x-ratelimit-limit"),
        resetAt: headerNumber(response.headers, "x-ratelimit-reset"),
      };
      if (this.rate.resetAt !== null) this.rate.resetAt *= 1000;
      if (response.type === "opaqueredirect")
        throw new GitHubError(
          "redirect",
          "Browser redirect headers are unavailable; use the canonical repository name",
        );
      if ([301, 302, 303, 307, 308].includes(response.status)) {
        const location = response.headers.get("location");
        if (!location || redirects.has(url.href))
          throw new GitHubError(
            "redirect",
            "Invalid or repeated GitHub redirect",
          );
        redirects.add(url.href);
        url = apiURL(new URL(location, url).href);
        continue;
      }
      if (response.status === 200)
        return { data: body, headers: response.headers, url: url.href };
      const message =
        typeof body === "object" &&
        body !== null &&
        "message" in body &&
        typeof body.message === "string"
          ? body.message
          : "";
      const retryAfter = response.headers.get("retry-after");
      const limited =
        response.status === 429 ||
        (response.status === 403 &&
          (this.rate.remaining === 0 ||
            retryAfter !== null ||
            /rate limit|abuse detection/i.test(message)));
      if (limited) {
        this.blockedReason = "rate_limit";
        const numeric =
          retryAfter !== null && /^\d+(\.\d+)?$/.test(retryAfter)
            ? Number(retryAfter) * 1000
            : null;
        const dated =
          retryAfter !== null && numeric === null
            ? Date.parse(retryAfter)
            : NaN;
        const deadline =
          numeric !== null
            ? this.now() + numeric
            : Number.isFinite(dated)
              ? dated
              : this.rate.remaining === 0 && this.rate.resetAt !== null
                ? this.rate.resetAt
                : this.now() + 60000 * 2 ** retries;
        this.blockedUntil = Math.max(
          this.now() + 1000,
          deadline,
          this.rate.remaining === 0 ? (this.rate.resetAt ?? 0) : 0,
        );
        if (
          retries >= this.policy.maxRetries ||
          this.blockedUntil - this.now() > this.policy.maxWaitMs
        )
          throw new GitHubError(
            "rate_limit",
            "GitHub rate limit reached",
            response.status,
            this.blockedUntil,
          );
      } else if (
        [500, 502, 503, 504].includes(response.status) &&
        retries < this.policy.maxRetries
      ) {
        this.blockedReason = "http";
        this.blockedUntil = this.now() + 1000 * 2 ** retries;
        if (this.blockedUntil - this.now() > this.policy.maxWaitMs)
          throw new GitHubError(
            "http",
            "GitHub service temporarily unavailable",
            response.status,
          );
      } else
        throw new GitHubError(
          response.status === 404
            ? "not_found"
            : response.status === 403
              ? "forbidden"
              : "http",
          "GitHub request was unsuccessful",
          response.status,
        );
      retries++;
    }
  }
}
