import type { PullRequest } from "../../../../packages/analysis/src/index.js";

export interface Population {
  inspected_prs: number;
  human_authored_prs: number;
  bot_authored_prs: number;
  draft_prs: number;
  missing_author_prs: number;
  outside_window_prs: number;
  after_observation_prs: number;
}

export function classifyPopulation(
  prs: PullRequest[],
  start: number | null,
  observed: number,
  botIds: number[],
): Population {
  const result: Population = {
    inspected_prs: 0,
    human_authored_prs: 0,
    bot_authored_prs: 0,
    draft_prs: 0,
    missing_author_prs: 0,
    outside_window_prs: 0,
    after_observation_prs: 0,
  };
  for (const pr of prs) {
    result.inspected_prs++;
    const created = Date.parse(pr.created_at);
    if (created >= observed) result.after_observation_prs++;
    else if (start !== null && created < start) result.outside_window_prs++;
    else if (pr.draft) result.draft_prs++;
    else if (pr.author === null) result.missing_author_prs++;
    else if (pr.author.type === "Bot" || botIds.includes(pr.author.id))
      result.bot_authored_prs++;
    else result.human_authored_prs++;
  }
  return result;
}

export function populationMatches(
  population: Population,
  inspected: number,
  eligible: number,
): boolean {
  return (
    population.inspected_prs === inspected &&
    population.human_authored_prs + population.bot_authored_prs === eligible &&
    population.human_authored_prs +
      population.bot_authored_prs +
      population.draft_prs +
      population.missing_author_prs +
      population.outside_window_prs +
      population.after_observation_prs ===
      inspected
  );
}
