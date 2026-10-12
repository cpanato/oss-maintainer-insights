import type { Event, Review, ReviewCommand } from "./types.js";

/** Extract bounded command signals without retaining comment bodies. */
export function parseReviewCommands(body: string): ReviewCommand[] {
  const state = new Map<string, { active: boolean; cancelled: boolean }>();
  let fence: { marker: string; length: number } | null = null;
  for (const line of body
    .replace(/<!--[\s\S]*?(?:-->|$)/g, "")
    .split(/\r?\n/)) {
    const marker = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (fence) {
      if (
        marker &&
        marker[1]![0] === fence.marker &&
        marker[1]!.length >= fence.length &&
        !marker[2]!.trim()
      )
        fence = null;
      continue;
    }
    if (marker) {
      fence = { marker: marker[1]![0]!, length: marker[1]!.length };
      continue;
    }
    const match =
      /^ {0,3}\/(lgtm|approve)(?:[ \t]+(cancel|no-issue))?[ \t]*$/i.exec(line);
    const remove = /^ {0,3}\/remove-(lgtm|approve)[ \t]*$/i.exec(line);
    if (!match && !remove) continue;
    const kind = (match?.[1] ?? remove![1]!).toLowerCase();
    const cancelled = !!remove || match?.[2]?.toLowerCase() === "cancel";
    const previous = state.get(kind);
    state.set(kind, {
      active: !cancelled,
      cancelled: cancelled || !!previous?.cancelled,
    });
  }
  const commands: ReviewCommand[] = [];
  for (const kind of ["lgtm", "approve"] as const) {
    const signal = state.get(kind);
    if (!signal) continue;
    if (signal.cancelled) commands.push(`${kind}_cancel`);
    if (signal.active) commands.push(kind);
  }
  return commands;
}
function compareID(a: string, b: string): number {
  if (/^\d+$/.test(a) && /^\d+$/.test(b)) {
    const first = BigInt(a),
      second = BigInt(b);
    if (first <= 18446744073709551615n && second <= 18446744073709551615n)
      return first < second ? -1 : first > second ? 1 : 0;
  }
  return a < b ? -1 : a > b ? 1 : 0;
}

export interface ReviewPair {
  event: Event;
  github: boolean;
  prow: boolean;
}
export function reviewPairs(
  reviews: Review[],
  comments: Event[],
  created: number,
  observed: number,
): Map<number, ReviewPair> {
  const pairs = new Map<number, ReviewPair>();
  const within = (event: Event) =>
    Date.parse(event.occurred_at) >= created &&
    Date.parse(event.occurred_at) <= observed;
  const include = (event: Event, source: "github" | "prow") => {
    const id = event.actor!.id;
    const pair = pairs.get(id) ?? { event, github: false, prow: false };
    if (
      Date.parse(event.occurred_at) < Date.parse(pair.event.occurred_at) ||
      (Date.parse(event.occurred_at) === Date.parse(pair.event.occurred_at) &&
        compareID(event.id, pair.event.id) < 0)
    )
      pair.event = event;
    pair[source] = true;
    pairs.set(id, pair);
  };
  for (const review of reviews) if (within(review)) include(review, "github");
  const signals = new Map<number, Map<string, Event>>();
  const ordered = [...comments, ...reviews]
    .filter(within)
    .toSorted(
      (a, b) =>
        Date.parse(a.occurred_at) - Date.parse(b.occurred_at) ||
        compareID(a.id, b.id),
    );
  for (const event of ordered) {
    const actor = signals.get(event.actor!.id) ?? new Map<string, Event>();
    for (const command of event.review_commands ?? []) {
      const kind = command.startsWith("lgtm") ? "lgtm" : "approve";
      if (command.endsWith("_cancel")) actor.delete(kind);
      else if (!actor.has(kind)) actor.set(kind, event);
    }
    signals.set(event.actor!.id, actor);
  }
  for (const actor of signals.values())
    for (const event of actor.values()) include(event, "prow");
  return pairs;
}
