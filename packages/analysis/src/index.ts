import type {
  Actor,
  AnalysisInput,
  AnalysisResult,
  Coverage,
  Event,
  PullRequest,
  Review,
} from "./types.js";
export type * from "./types.js";

export const SCHEMA_VERSION = 1;
export const METHODOLOGY_VERSION = "1.0.0";
export const MIN_OBSERVATIONS = 5;

/** Normalize account labels at the API boundary; stable IDs remain identity. */
export function normalizeActor(actor: Actor | null): Actor | null {
  if (actor === null) return null;
  if (
    !Number.isSafeInteger(actor.id) ||
    actor.id <= 0 ||
    !actor.login.trim() ||
    !["User", "Bot"].includes(actor.type)
  ) {
    throw new Error("Invalid actor");
  }
  return { id: actor.id, login: actor.login.trim(), type: actor.type };
}

function timestamp(value: string): number {
  const parsed = Date.parse(value);
  if (
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{3})?Z$/.test(value) ||
    !Number.isFinite(parsed) ||
    new Date(parsed).toISOString().replace(".000Z", "Z") !==
      value.replace(".000Z", "Z")
  ) {
    throw new Error(`Invalid UTC timestamp: ${value}`);
  }
  return parsed;
}

const qualifies = (review: Review): boolean =>
  ["APPROVED", "CHANGES_REQUESTED", "COMMENTED"].includes(review.state);

function coverage(
  mode: AnalysisInput["mode"],
  enumeration: boolean,
  covered: number,
  eligible: number,
): Coverage {
  if (enumeration && covered === eligible)
    return mode === "live_sample"
      ? "complete_for_sample"
      : "complete_for_window";
  return eligible > 0 && covered === 0 ? "unavailable" : "partial";
}

/** No network, wall clock or storage dependencies. Input is normalized and schema-validated by callers. */
export function analyze(input: AnalysisInput): AnalysisResult {
  const start = timestamp(input.window.start);
  const end = timestamp(input.window.end);
  const observed = timestamp(input.observed_at);
  if (start >= end || end > observed)
    throw new Error("Window must be nonempty and end at or before observation");
  const bots = new Set(input.bot_ids);
  const exclusions = {
    draft_prs: 0,
    bot_authors: 0,
    missing_authors: 0,
    bot_events: 0,
    missing_event_actors: 0,
    self_events: 0,
  };
  const eligible: PullRequest[] = [];
  const prIds = new Set<number>();
  for (const pr of input.pull_requests) {
    if (prIds.has(pr.id)) throw new Error(`Duplicate PR ID: ${pr.id}`);
    prIds.add(pr.id);
    const created = timestamp(pr.created_at);
    if (created > observed) throw new Error("PR created after observation");
    for (const stream of [pr.reviews, pr.issue_comments, pr.review_comments]) {
      const ids = new Set<string>();
      for (const event of stream) {
        if (ids.has(event.id))
          throw new Error(`Duplicate event ID: ${event.id}`);
        ids.add(event.id);
        timestamp(event.occurred_at);
        normalizeActor(event.actor);
      }
    }
    const author = normalizeActor(pr.author);
    if (pr.draft) exclusions.draft_prs++;
    else if (author === null) exclusions.missing_authors++;
    else if (author.type === "Bot" || bots.has(author.id))
      exclusions.bot_authors++;
    else eligible.push(pr);
  }

  const humanEvent = (event: Event, pr: PullRequest): boolean => {
    if (event.actor === null) {
      exclusions.missing_event_actors++;
      return false;
    }
    if (event.actor.type === "Bot" || bots.has(event.actor.id)) {
      exclusions.bot_events++;
      return false;
    }
    if (event.actor.id === pr.author?.id) {
      exclusions.self_events++;
      return false;
    }
    return true;
  };
  // Evaluate exclusions once per source event, even when an event serves two metrics.
  const eventsByPR = new Map<
    number,
    { reviews: Review[]; comments: Event[] }
  >();
  for (const pr of eligible) {
    const reviews = pr.reviews.filter(
      (event) => humanEvent(event, pr) && qualifies(event),
    );
    const comments = [...pr.issue_comments, ...pr.review_comments].filter(
      (event) => humanEvent(event, pr),
    );
    eventsByPR.set(pr.id, { reviews, comments });
  }

  const cohort = eligible.filter(
    (pr) => timestamp(pr.created_at) >= start && timestamp(pr.created_at) < end,
  );
  const authors = new Set(cohort.map((pr) => pr.author!.id));
  const outcomes = { open: 0, closed_unmerged: 0, merged: 0 };
  for (const pr of cohort) outcomes[pr.outcome]++;
  const reviewPopulation = input.mode === "live_sample" ? cohort : eligible;
  const coveredReviews = reviewPopulation.filter((pr) => pr.coverage.reviews);
  const reviewers = new Map<
    number,
    { actor: Actor; review_units: number; labelAt: number; labelEvent: string }
  >();
  for (const pr of coveredReviews) {
    const pairs = new Map<number, Review>();
    for (const review of eventsByPR.get(pr.id)!.reviews) {
      const at = timestamp(review.occurred_at);
      if (at < timestamp(pr.created_at) || at > observed) continue;
      const previous = pairs.get(review.actor!.id);
      if (!previous || at < timestamp(previous.occurred_at))
        pairs.set(review.actor!.id, review);
    }
    for (const review of pairs.values()) {
      const at = timestamp(review.occurred_at);
      // Historical units belong to the earliest qualifying review's month.
      if (input.mode === "historical" && (at < start || at >= end)) continue;
      const actor = normalizeActor(review.actor)!;
      const current = reviewers.get(actor.id);
      if (!current)
        reviewers.set(actor.id, {
          actor,
          review_units: 1,
          labelAt: at,
          labelEvent: review.id,
        });
      else {
        current.review_units++;
        if (
          at > current.labelAt ||
          (at === current.labelAt && review.id > current.labelEvent)
        ) {
          current.actor = actor;
          current.labelAt = at;
          current.labelEvent = review.id;
        }
      }
    }
  }
  const distribution = [...reviewers.values()]
    .sort((a, b) => b.review_units - a.review_units || a.actor.id - b.actor.id)
    .map(({ actor, review_units }) => ({ actor, review_units }));
  const units = distribution.reduce(
    (sum, actor) => sum + actor.review_units,
    0,
  );
  let count = 0;
  let accumulated = 0;
  while (accumulated * 5 < units * 4)
    accumulated += distribution[count++]!.review_units;
  const sufficientReviews =
    coveredReviews.length >= MIN_OBSERVATIONS && units > 0;
  const coveredResponse = cohort.filter(
    (pr) =>
      pr.coverage.reviews &&
      pr.coverage.issue_comments &&
      pr.coverage.review_comments,
  );
  const durations: number[] = [];
  for (const pr of coveredResponse) {
    const events = eventsByPR.get(pr.id)!;
    const responses = [...events.reviews, ...events.comments]
      .map((event) => timestamp(event.occurred_at))
      .filter((at) => at >= timestamp(pr.created_at) && at <= observed);
    if (responses.length)
      durations.push(
        responses.reduce((first, at) => Math.min(first, at), Infinity) -
          timestamp(pr.created_at),
      );
  }
  durations.sort((a, b) => a - b);
  const middle = Math.floor(durations.length / 2);
  const median =
    durations.length >= MIN_OBSERVATIONS
      ? durations.length % 2
        ? durations[middle]!
        : (durations[middle - 1]! + durations[middle]!) / 2
      : null;
  return {
    schema_version: SCHEMA_VERSION,
    methodology_version: METHODOLOGY_VERSION,
    repository: input.repository,
    mode: input.mode,
    collected_at: input.observed_at,
    window: input.window,
    exclusions,
    creation_cohort: {
      eligible_prs: cohort.length,
      coverage: coverage(
        input.mode,
        input.enumeration.creation_cohort,
        cohort.length,
        cohort.length,
      ),
      authors: authors.size,
      outcomes,
    },
    review_activity: {
      eligible_prs: reviewPopulation.length,
      covered_prs: coveredReviews.length,
      coverage: coverage(
        input.mode,
        input.mode === "live_sample"
          ? input.enumeration.creation_cohort
          : input.enumeration.review_activity,
        coveredReviews.length,
        reviewPopulation.length,
      ),
      review_units: units,
      participants: distribution.length,
      distribution,
      concentration_80: {
        value: sufficientReviews ? count : null,
        status: sufficientReviews ? "available" : "insufficient_data",
      },
      largest_reviewer_share: {
        value: sufficientReviews ? distribution[0]!.review_units / units : null,
        status: sufficientReviews ? "available" : "insufficient_data",
      },
    },
    first_response: {
      eligible_prs: cohort.length,
      covered_prs: coveredResponse.length,
      coverage: coverage(
        input.mode,
        input.enumeration.creation_cohort,
        coveredResponse.length,
        cohort.length,
      ),
      responded_prs: durations.length,
      unanswered_prs: coveredResponse.length - durations.length,
      median_ms: {
        value: median,
        status: median === null ? "insufficient_data" : "available",
      },
    },
  };
}
