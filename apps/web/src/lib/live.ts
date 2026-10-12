import { Ajv } from "ajv";
import addFormats from "ajv-formats";
import type { FormatsPlugin } from "ajv-formats";
import inputSchema from "../../../../schemas/analysis-input.schema.json" with { type: "json" };
import resultSchema from "../../../../schemas/analysis-result.schema.json" with { type: "json" };
import snapshotSchema from "../../../../schemas/repository-snapshot.schema.json" with { type: "json" };
const validator = new Ajv({ strict: true });
// NodeNext and Astro's bundler resolve this CommonJS export to different types.
// eslint-disable-next-line @typescript-eslint/no-unnecessary-type-assertion
const applyFormats: FormatsPlugin = addFormats as unknown as FormatsPlugin;
applyFormats(validator);
const validInput = validator.compile(inputSchema);
validator.addSchema(resultSchema);
const validPublic = validator.compile(snapshotSchema);
import {
  analyze,
  METHODOLOGY_VERSION,
  SCHEMA_VERSION,
} from "../../../../packages/analysis/src/index.js";
import type {
  Actor,
  AnalysisInput,
  AnalysisResult,
  Event,
  PullRequest,
  Review,
} from "../../../../packages/analysis/src/index.js";
import {
  GitHubClient,
  GitHubError,
  normalizeRepository,
  repositoryPath,
} from "../../../../packages/github/src/index.js";
import type {
  FailureReason,
  RateLimit,
} from "../../../../packages/github/src/index.js";

export const QUERY_POLICY = "newest-eight-human-prs-90-days-v1";
export const CATALOG_POLICY = "three-age-bands-eighteen-human-prs-90-days-v1";
export interface StreamFailure {
  pr: number;
  stream: string;
  reason: FailureReason;
}
export interface PublicSnapshot {
  policy: typeof QUERY_POLICY | typeof CATALOG_POLICY;
  sample_age?: { oldest_created_at: string; newest_created_at: string };
  result: AnalysisResult;
  selection: {
    listed: number;
    selected: number;
    eligible_on_page: number;
    truncated: boolean;
    list_has_more: boolean;
  };
  requests: number;
  rate: RateLimit;
  failures: StreamFailure[];
  complete: boolean;
}
export interface LiveSnapshot extends PublicSnapshot {
  input: AnalysisInput;
}

/** Static snapshots publish validated aggregates, never source records. */
export function parsePublicSnapshot(
  value: unknown,
  repository: string,
): PublicSnapshot | null {
  if (!validPublic(value)) return null;
  const snapshot = value as unknown as PublicSnapshot;
  if (
    normalizeRepository(snapshot.result.repository.full_name).toLowerCase() !==
      normalizeRepository(repository).toLowerCase() ||
    snapshot.result.mode !== "live_sample" ||
    snapshot.result.methodology_version !== METHODOLOGY_VERSION ||
    snapshot.selection.selected !==
      snapshot.result.creation_cohort.eligible_prs ||
    snapshot.selection.selected > snapshot.selection.eligible_on_page ||
    snapshot.selection.eligible_on_page > snapshot.selection.listed
  )
    return null;
  const reviews = snapshot.result.review_activity;
  const response = snapshot.result.first_response;
  if (
    reviews.eligible_prs !== snapshot.selection.selected ||
    response.eligible_prs !== snapshot.selection.selected ||
    reviews.covered_prs > reviews.eligible_prs ||
    response.covered_prs > response.eligible_prs ||
    response.responded_prs + response.unanswered_prs !== response.covered_prs ||
    (snapshot.complete &&
      (reviews.covered_prs !== reviews.eligible_prs ||
        response.covered_prs !== response.eligible_prs))
  )
    return null;
  if (snapshot.sample_age) {
    const oldest = Date.parse(snapshot.sample_age.oldest_created_at);
    const newest = Date.parse(snapshot.sample_age.newest_created_at);
    if (
      snapshot.selection.selected === 0 ||
      oldest > newest ||
      oldest < Date.parse(snapshot.result.window.start) ||
      newest >= Date.parse(snapshot.result.collected_at)
    )
      return null;
  }
  return snapshot;
}
export interface LiveOptions {
  client?: GitHubClient;
  now?: () => number;
  botIds?: number[];
  signal?: AbortSignal;
  onProgress?: (snapshot: LiveSnapshot) => void;
}
const object = (value: unknown): Record<string, unknown> => {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new GitHubError("invalid_response", "Invalid GitHub record");
  return value as Record<string, unknown>;
};
const id = (value: unknown): number => {
  if (!Number.isSafeInteger(value) || Number(value) <= 0)
    throw new GitHubError("invalid_response", "Invalid GitHub identifier");
  return Number(value);
};
const date = (value: unknown): string => {
  if (
    typeof value !== "string" ||
    !Number.isFinite(Date.parse(value)) ||
    new Date(value).toISOString().replace(".000Z", "Z") !==
      value.replace(".000Z", "Z")
  )
    throw new GitHubError("invalid_response", "Invalid GitHub timestamp");
  return value;
};
function actor(value: unknown): Actor | null {
  if (value === null) return null;
  const user = object(value);
  if (user.login === "ghost") return null;
  if (
    typeof user.login !== "string" ||
    !user.login ||
    (user.type !== "User" && user.type !== "Bot")
  )
    throw new GitHubError("invalid_response", "Invalid GitHub account");
  return { id: id(user.id), login: user.login, type: user.type };
}
function decodePR(value: unknown): { number: number; pr: PullRequest } {
  const raw = object(value);
  if (
    typeof raw.draft !== "boolean" ||
    (raw.state !== "open" && raw.state !== "closed") ||
    (typeof raw.merged_at !== "string" && raw.merged_at !== null)
  )
    throw new GitHubError("invalid_response", "Invalid PR state");
  return {
    number: id(raw.number),
    pr: {
      id: id(raw.id),
      author: actor(raw.user),
      created_at: date(raw.created_at),
      draft: raw.draft,
      outcome:
        raw.merged_at !== null
          ? "merged"
          : raw.state === "open"
            ? "open"
            : "closed_unmerged",
      coverage: {
        reviews: false,
        issue_comments: false,
        review_comments: false,
      },
      reviews: [],
      issue_comments: [],
      review_comments: [],
    },
  };
}
function review(value: unknown): Review | null {
  const raw = object(value);
  if (raw.state === "PENDING") return null;
  if (
    typeof raw.state !== "string" ||
    !["APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED"].includes(
      raw.state,
    )
  )
    throw new GitHubError("invalid_response", "Invalid review state");
  return {
    id: String(id(raw.id)),
    actor: actor(raw.user),
    occurred_at: date(raw.submitted_at),
    state: raw.state as Review["state"],
  };
}
function comment(value: unknown): Event {
  const raw = object(value);
  return {
    id: String(id(raw.id)),
    actor: actor(raw.user),
    occurred_at: date(raw.created_at),
  };
}
function unique<T extends Event>(events: T[]): T[] {
  return [...new Map(events.map((event) => [event.id, event])).values()];
}

/** One client and one budget cover the entire live query. Only normalized records survive. */
export async function collectLive(
  repository: string,
  options: LiveOptions = {},
): Promise<LiveSnapshot> {
  const client = options.client ?? new GitHubClient();
  const now = options.now ?? Date.now;
  const signal = options.signal;
  const metadata = object(
    (await client.get(repositoryPath(repository), signal)).data,
  );
  if (metadata.private !== false || typeof metadata.full_name !== "string")
    throw new GitHubError(
      "invalid_response",
      "A public repository is required",
    );
  const repositoryId = id(metadata.id);
  const canonical = normalizeRepository(metadata.full_name);
  const path = `/repositories/${repositoryId}`;
  const page = await client.get(
    `${path}/pulls?state=all&sort=created&direction=desc&per_page=30`,
    signal,
  );
  if (!Array.isArray(page.data))
    throw new GitHubError("invalid_response", "Invalid PR list");
  const observed = now();
  const start = observed - 90 * 86400000;
  const bots = new Set(options.botIds ?? []);
  const listed = (page.data as unknown[]).map(decodePR);
  const eligible = listed.filter(
    ({ pr }) =>
      !pr.draft &&
      pr.author !== null &&
      pr.author.type === "User" &&
      !bots.has(pr.author.id) &&
      Date.parse(pr.created_at) >= start &&
      Date.parse(pr.created_at) < observed,
  );
  eligible.sort(
    (a, b) =>
      Date.parse(b.pr.created_at) - Date.parse(a.pr.created_at) ||
      b.number - a.number,
  );
  const selected = eligible.slice(0, 8);
  const input: AnalysisInput = {
    schema_version: SCHEMA_VERSION,
    repository: { id: repositoryId, full_name: canonical },
    mode: "live_sample",
    observed_at: new Date(observed).toISOString(),
    window: {
      start: new Date(start).toISOString(),
      end: new Date(observed).toISOString(),
    },
    enumeration: { creation_cohort: true, review_activity: true },
    bot_ids: [...bots],
    pull_requests: selected.map((item) => item.pr),
  };
  const failures: StreamFailure[] = [];
  const snapshot = (): LiveSnapshot => ({
    policy: QUERY_POLICY,
    input: structuredClone(input),
    result: analyze(input),
    selection: {
      listed: listed.length,
      selected: selected.length,
      eligible_on_page: eligible.length,
      truncated: eligible.length > 8,
      list_has_more: page.headers.has("link"),
    },
    requests: client.requestsUsed,
    rate: client.rateLimit,
    failures: [...failures],
    complete:
      input.pull_requests.every((pr) =>
        Object.values(pr.coverage).every(Boolean),
      ) && !signal?.aborted,
  });
  const publish = () => options.onProgress?.(snapshot());
  publish();
  // Finish review participation before spending the remaining budget on comments.
  for (const item of selected) {
    const stream = await client.paginate(
      `${path}/pulls/${item.number}/reviews?per_page=100`,
      review,
      signal,
    );
    item.pr.reviews = unique(
      stream.items.filter((event): event is Review => event !== null),
    );
    item.pr.coverage.reviews = stream.complete;
    if (stream.error)
      failures.push({
        pr: item.number,
        stream: "reviews",
        reason: stream.error.reason,
      });
    publish();
    if (signal?.aborted) return snapshot();
  }
  for (const item of selected) {
    if (!item.pr.coverage.reviews) continue;
    for (const name of ["issue_comments", "review_comments"] as const) {
      const endpoint =
        name === "issue_comments"
          ? `${path}/issues/${item.number}/comments`
          : `${path}/pulls/${item.number}/comments`;
      const stream = await client.paginate(
        `${endpoint}?per_page=100`,
        comment,
        signal,
      );
      item.pr[name] = unique(stream.items);
      item.pr.coverage[name] = stream.complete;
      if (stream.error)
        failures.push({
          pr: item.number,
          stream: name,
          reason: stream.error.reason,
        });
      publish();
      if (signal?.aborted) return snapshot();
    }
  }
  return snapshot();
}

export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}
const prefix = `oss-insights:${SCHEMA_VERSION}:${METHODOLOGY_VERSION}:${QUERY_POLICY}:`;
const ttl = 3600000;
/** Cache only complete normalized queries; failures cannot replace a good entry. */
export class LiveCache {
  constructor(
    private readonly storage: StorageLike | null,
    private readonly now: () => number = Date.now,
  ) {}
  read(
    repository: string,
    botIds: readonly number[] = [],
  ): LiveSnapshot | null {
    try {
      const alias = this.storage?.getItem(
        prefix + "alias:" + normalizeRepository(repository).toLowerCase(),
      );
      if (!alias) return null;
      const raw = this.storage?.getItem(prefix + "repo:" + alias);
      if (!raw) return null;
      const cached = object(JSON.parse(raw) as unknown);
      const snapshot = cached.snapshot as LiveSnapshot;
      const input = object(snapshot?.input);
      if (!validInput(input) || input.mode !== "live_sample") return null;
      if (
        [...snapshot.input.bot_ids].sort((a, b) => a - b).join(",") !==
        [...botIds].sort((a, b) => a - b).join(",")
      )
        return null;
      const selection = object(snapshot.selection);
      const rate = object(snapshot.rate);
      if (
        ![
          selection.listed,
          selection.selected,
          selection.eligible_on_page,
          snapshot.requests,
        ].every((value) => Number.isSafeInteger(value) && Number(value) >= 0) ||
        selection.selected !== snapshot.input.pull_requests.length ||
        selection.selected > 8 ||
        snapshot.requests > 20 ||
        typeof selection.truncated !== "boolean" ||
        typeof selection.list_has_more !== "boolean" ||
        ![rate.remaining, rate.limit, rate.resetAt].every(
          (value) =>
            value === null ||
            (typeof value === "number" && Number.isFinite(value)),
        ) ||
        !Array.isArray(snapshot.failures) ||
        snapshot.failures.length !== 0
      )
        return null;
      if (
        cached.version !== 1 ||
        typeof cached.saved_at !== "number" ||
        this.now() < cached.saved_at ||
        this.now() - cached.saved_at >= ttl ||
        snapshot.policy !== QUERY_POLICY ||
        !snapshot.complete ||
        input.schema_version !== SCHEMA_VERSION ||
        snapshot.result.methodology_version !== METHODOLOGY_VERSION ||
        String(snapshot.input.repository.id) !== alias ||
        !snapshot.input.pull_requests.every((pr) =>
          Object.values(pr.coverage).every(Boolean),
        )
      )
        return null;
      // Recalculate metrics instead of trusting cached aggregate values.
      return { ...snapshot, result: analyze(snapshot.input) };
    } catch {
      return null;
    }
  }
  write(requested: string, snapshot: LiveSnapshot): boolean {
    if (!snapshot.complete) return false;
    try {
      if (!this.storage) return false;
      const repositoryId = String(snapshot.input.repository.id);
      this.storage.setItem(
        prefix + "repo:" + repositoryId,
        JSON.stringify({ version: 1, saved_at: this.now(), snapshot }),
      );
      for (const name of [requested, snapshot.input.repository.full_name])
        this.storage.setItem(
          prefix + "alias:" + normalizeRepository(name).toLowerCase(),
          repositoryId,
        );
      return true;
    } catch {
      return false;
    }
  }
  clear(repository: string): boolean {
    try {
      const key =
        prefix + "alias:" + normalizeRepository(repository).toLowerCase();
      const alias = this.storage?.getItem(key);
      if (alias) this.storage?.removeItem(prefix + "repo:" + alias);
      this.storage?.removeItem(key);
      return this.storage !== null;
    } catch {
      return false;
    }
  }
}
