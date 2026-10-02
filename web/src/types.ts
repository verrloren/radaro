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

export interface RedditStats {
 community: string;
 comments: number | null;
 upvote_ratio?: number | null;
}

export interface Mention {
 reddit?: RedditStats;
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

export interface User {
  id: number;
  email: string;
  is_admin: boolean;
  created_at: string;
}

export interface AuthConfig {
  registration_open: boolean;
}

export interface Project {
  id: number;
  name: string;
  /** Every user has one; it cannot be deleted. */
  is_default: boolean;
  created_at: string;
  updated_at: string;
  query_count: number;
  mention_count: number;
  queries?: string[];
}

export interface Keyword {
  id: number;
  project_id: number;
  query: string;
  sources: string[];
  added_at: string;
  last_scanned_at: string | null;
  mention_count: number;
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

export interface SourceField {
  key: string;
  label: string;
  secret: boolean;
  multiline?: boolean;
  placeholder?: string;
  env: string;
  set: boolean;
  /** Where the value comes from: saved in Radaro, the environment, or unset. */
  origin: "ui" | "env" | "";
  /** Current value; never sent for secret fields. */
  value?: string;
}

export interface SourceSettings {
  name: string;
  label: string;
  configured: boolean;
  saved: boolean;
  fields: SourceField[];
}

export interface Platform {
  name: string;
  label: string;
  replies: boolean;
  titles: boolean;
  max_chars: number;
}

export type AccountStatus = "unknown" | "live" | "invalid" | "suspended" | "limited";

export interface Account {
  id: number;
  platform: string;
  handle: string;
  status: AccountStatus;
  status_detail: string | null;
  checked_at: string | null;
  limited_until: string | null;
  paused: boolean;
  /** Per-account limits; null means the platform default. */
  daily_limit: number | null;
  min_interval_sec: number | null;
  community_cooldown_h: number | null;
  created_at: string;
  updated_at: string;
}

/** Where an account stands against its publishing limits. */
export interface AccountQuota {
  daily: number;
  used_24h: number;
  remaining: number;
  /** When it may publish next; null = now, or never without a person (see ready). */
  next_at: string | null;
  reason?: string;
  ready: boolean;
}

export interface AccountActivity {
  published_24h: number;
  published_7d: number;
  published_30d: number;
  failed_30d: number;
  /** Of the ones published in the last 30 days. */
  removed_30d: number;
  last_published_at: string | null;
  last_error: string | null;
}

/** An account's effective limits: its own overrides, else the platform defaults. */
export interface AccountLimits {
  daily: number;
  min_interval_sec: number;
  community_cooldown_h: number;
  /** At least one limit overrides the platform default. */
  custom: boolean;
}

/** An account as GET /api/accounts returns it. */
export interface AccountView extends Account {
  browser?: { proxy_configured: boolean };
  limits: AccountLimits;
  /** Platform defaults, independent of this account's overrides. */
  default_limits?: Omit<AccountLimits, "custom">;
  /** Missing when it could not be computed. */
  quota?: AccountQuota;
  activity: AccountActivity;
}

export interface AccountsResponse {
  platforms: Platform[];
  accounts: AccountView[];
  /** POST /api/accounts/check only: why some checks could not run. */
  error?: string;
  errors?: string[];
}

/** PATCH /api/accounts/{id}; a limit of 0 returns it to the platform default. */
export interface AccountUpdate {
  paused?: boolean;
  daily_limit?: number;
  min_interval_sec?: number;
  community_cooldown_h?: number;
}

export interface BanRatePoint {
  day: string;
  platform: string;
  total: number;
  dead: number;
}

export interface AccountTally {
  /** Absent on the total. */
  platform?: string;
  total: number;
  live: number;
  dead: number;
  limited: number;
  paused: number;
  unknown: number;
  /** dead / total, 0–1. */
  ban_rate: number;
}

export interface AccountStats {
  days: number;
  series: BanRatePoint[];
  platforms: AccountTally[];
  total: AccountTally;
}

export interface ConnectRequest {
  platform: string;
  handle?: string;
  instance?: string;
  secret: string;
  /** Also make this project publish with the new account. */
  project_id?: number;
}

export interface AccountImportRow {
  row: number;
  platform: string;
  handle: string;
  status: string;
  error?: string;
}

export interface BrowserScreen { image: string; width: number; height: number }
export type BrowserInput = { kind: "click"; x: number; y: number } | { kind: "text"; text: string } | { kind: "key"; key: string } | { kind: "scroll"; delta: number } | { kind: "refresh" };
export interface RedditBrowserRequest {username: string; password: string; proxy_url?: string; project_id?: number}

export interface AccountImportResult {
  results: AccountImportRow[];
  added: number;
  updated: number;
  errors: number;
}

export interface ProxySettings {
  configured: boolean;
  origin: "ui" | "env" | "";
}

/** A project's pool of accounts on one platform: publishing picks among them. */
export interface ProjectPool {
  platform: Platform;
  accounts: AccountView[];
}

// ---------- Drafts: the publishing queue ----------

export type DraftStatus = "draft" | "approved" | "publishing" | "published" | "failed" | "skipped";

export interface Draft {
  id: number;
  project_id: number | null;
  platform: string;
  /** null = the outbox picks an account at publish time. */
  account_id: number | null;
  kind: "post" | "reply";
  community: string | null;
  title: string | null;
  body: string;
  reply_to: string | null;
  query: string | null;
  mention_id: string | null;
  status: DraftStatus;
  remote_id: string | null;
  remote_url: string | null;
  error: string | null;
  metrics: Record<string, unknown> | null;
  metrics_at: string | null;
  /** Published, then found removed by the platform or moderators. */
  removed_at: string | null;
  created_at: string;
  updated_at: string;
  approved_at: string | null;
  published_at: string | null;
}

/** Drafts per status (GET /api/drafts/counts). */
export type DraftCounts = Partial<Record<DraftStatus, number>>;

/** Who would publish a draft now, and when it may go out. */
export interface PublishPlan {
  account: AccountView | null;
  quota: AccountQuota | null;
  /** null = now. */
  next_at: string | null;
  reason?: string;
}

export interface DraftDetail extends Draft {
  /** null once the draft is published, publishing or skipped. */
  plan: PublishPlan | null;
}

export interface DraftEdit {
  title?: string;
  body?: string;
  community?: string;
  /** null = pick automatically. */
  account_id?: number | null;
}

export interface DraftWarning {
  /** self_promo | duplicate | subreddit_rules | community_bans_promo | subreddit_rules_unavailable */
  code: string;
  text: string;
}

export interface PublishResult {
  draft: Draft;
  account: Account;
}

export interface LLMStatus {
 reasoning_effort?: string;
 provider: string; model: string; configured: boolean;
 status: "not_configured" | "unchecked" | "connected" | "error";
 checked_at: string | null; detail: string;
}
export interface ReplySettings { brief: string; instructions?: string; language: string; tone: string; }
export interface RedditComment { author: string; body: string; score: number | null; url: string; }
export interface RedditPostDetails {
 url: string; title: string; body: string; author: string; community: string; created_at: string | null;
 score: number | null; comments: number | null; upvote_ratio: number | null; flair: string;
 locked: boolean; archived: boolean; removed: boolean; nsfw: boolean; can_reply: boolean;
 replies: RedditComment[]; rules: {name: string; description: string}[]; fetched_at: string;
}
export interface RedditPostResponse { post: RedditPostDetails; project_id: number; draft?: DraftDetail | null; }
