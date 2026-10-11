export interface Actor {
  id: number;
  login: string;
  type: "User" | "Bot";
}

export interface Event {
  id: string;
  actor: Actor | null;
  occurred_at: string;
}

export interface Review extends Event {
  state:
    "APPROVED" | "CHANGES_REQUESTED" | "COMMENTED" | "PENDING" | "DISMISSED";
}

export interface PullRequest {
  id: number;
  author: Actor | null;
  created_at: string;
  draft: boolean;
  outcome: "open" | "closed_unmerged" | "merged";
  coverage: {
    reviews: boolean;
    issue_comments: boolean;
    review_comments: boolean;
  };
  reviews: Review[];
  issue_comments: Event[];
  review_comments: Event[];
}

export interface AnalysisInput {
  schema_version: 1;
  repository: { id: number; full_name: string };
  mode: "live_sample" | "historical";
  observed_at: string;
  window: { start: string; end: string };
  enumeration: { creation_cohort: boolean; review_activity: boolean };
  bot_ids: number[];
  pull_requests: PullRequest[];
}

export type Coverage =
  "complete_for_sample" | "complete_for_window" | "partial" | "unavailable";

export interface Metric {
  value: number | null;
  status: "available" | "insufficient_data";
}

export interface AnalysisResult {
  schema_version: 1;
  methodology_version: "1.0.0";
  repository: AnalysisInput["repository"];
  mode: AnalysisInput["mode"];
  collected_at: string;
  window: AnalysisInput["window"];
  exclusions: {
    draft_prs: number;
    bot_authors: number;
    missing_authors: number;
    bot_events: number;
    missing_event_actors: number;
    self_events: number;
  };
  creation_cohort: {
    eligible_prs: number;
    coverage: Coverage;
    authors: number;
    outcomes: { open: number; closed_unmerged: number; merged: number };
  };
  review_activity: {
    eligible_prs: number;
    covered_prs: number;
    coverage: Coverage;
    review_units: number;
    participants: number;
    distribution: { actor: Actor; review_units: number }[];
    concentration_80: Metric;
    largest_reviewer_share: Metric;
  };
  first_response: {
    eligible_prs: number;
    covered_prs: number;
    coverage: Coverage;
    responded_prs: number;
    unanswered_prs: number;
    median_ms: Metric;
  };
}
