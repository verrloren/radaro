// Types mirror docs/API.md exactly.

export type Sentiment = "positive" | "neutral" | "negative";
export const SENTIMENTS: readonly Sentiment[] = ["positive", "neutral", "negative"];

export interface SourceInfo {
  name: string;
  label: string;
  glyph: string;
  color: string;
  needs_config: boolean;
  configured: boolean;
}

export interface Mention {
  id: string;
  source: string;
  source_label: string;
  glyph: string;
  color: string;
  query: string;
  author: string | null;
  title: string | null;
  text: string;
  url: string | null;
  created_at: string;
  score: number | null;
  sentiment: Sentiment | null;
  sentiment_score: number | null;
  theme: string | null;
}

export interface Project {
  id: number;
  name: string;
  created_at: string;
  updated_at: string;
  query_count: number;
  mention_count: number;
  queries?: string[];
}

export interface SourceState {
  source: string;
  newest_at: string | null;
  oldest_at: string | null;
  backfill_complete: boolean;
  last_success_at: string | null;
  last_error: string | null;
}

export interface Tracking {
  query: string;
  sources: string[];
  created_at: string;
  updated_at: string;
  last_scanned_at: string | null;
  source_states: SourceState[];
}

export interface Summary {
  total: number;
  by_sentiment: Record<string, number>;
  by_source: Record<string, number>;
  by_day: Record<string, number>;
}

export interface TimeseriesPoint {
  date: string;
  positive: number;
  neutral: number;
  negative: number;
  total: number;
}

export interface Theme {
  label: string;
  count: number;
}

export type TrackMode = "incremental" | "backfill";

export interface TrackResult {
  query: string;
  project_id: number | null;
  mode: TrackMode;
  fetched: number;
  new: number;
  by_source: Record<string, number>;
  pages_by_source: Record<string, number>;
  backfill_complete: Record<string, boolean>;
  errors: Record<string, string>;
  sentiment_error: string | null;
  analysis_error: string | null;
  alerted: number;
  alert_pending: number;
  alert_error: string | null;
  threshold_alerted: number;
  threshold_pending: number;
  threshold_events: string[];
}

export interface Meta {
  version: string;
  sources: SourceInfo[];
  default_sources: string[];
}

export interface SummaryResponse {
  summary: Summary;
  timeseries: TimeseriesPoint[];
  net: number;
  themes: Theme[];
}

export interface TrackRequest {
  query: string;
  sources: string[];
  mode: TrackMode;
  pages: number;
  project_id?: number;
}

/** Current dashboard scope: a keyword, a project, or everything. */
export type Scope =
  | { kind: "all" }
  | { kind: "query"; q: string }
  | { kind: "project"; p: number };
