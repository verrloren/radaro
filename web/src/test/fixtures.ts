import type { AsyncState } from "../hooks";
import type { Account, AccountView, DraftDetail, Platform, Project, User } from "../types";

export const PLATFORMS: Platform[] = [
  { name: "reddit", label: "Reddit", replies: true, titles: true, max_chars: 40000 },
  { name: "bluesky", label: "Bluesky", replies: true, titles: false, max_chars: 300 },
  { name: "mastodon", label: "Mastodon", replies: true, titles: false, max_chars: 500 },
  { name: "devto", label: "Dev.to", replies: false, titles: true, max_chars: 0 },
];

const STAMP = "2026-09-01T10:00:00Z";

export function account(over: Partial<Account> = {}): Account {
  return {
    id: 1,
    platform: "reddit",
    handle: "u/alice",
    status: "live",
    status_detail: null,
    checked_at: STAMP,
    limited_until: null,
    paused: false,
    daily_limit: null,
    min_interval_sec: null,
    community_cooldown_h: null,
    created_at: STAMP,
    updated_at: STAMP,
    ...over,
  };
}

export function accountView(over: Partial<AccountView> = {}): AccountView {
  return {
    ...account(),
    limits: { daily: 5, min_interval_sec: 1800, community_cooldown_h: 24, custom: false },
    quota: { daily: 5, used_24h: 1, remaining: 4, next_at: null, ready: true },
    activity: {
      published_24h: 1,
      published_7d: 3,
      published_30d: 10,
      failed_30d: 0,
      removed_30d: 1,
      last_published_at: STAMP,
      last_error: null,
    },
    ...over,
  };
}

export function draft(over: Partial<DraftDetail> = {}): DraftDetail {
  return {
    id: 7,
    project_id: 1,
    platform: "reddit",
    account_id: null,
    kind: "post",
    community: "golang",
    title: "A tool for listening",
    body: "Hello there",
    reply_to: null,
    query: null,
    mention_id: null,
    status: "draft",
    remote_id: null,
    remote_url: null,
    error: null,
    metrics: null,
    metrics_at: null,
    removed_at: null,
    created_at: STAMP,
    updated_at: STAMP,
    approved_at: null,
    published_at: null,
    plan: null,
    ...over,
  };
}

export const USER: User = { id: 1, email: "ann@example.com", is_admin: false, created_at: STAMP };

export function project(over: Partial<Project> = {}): Project {
  return {
    id: 1,
    name: "Radaro",
    is_default: false,
    created_at: STAMP,
    updated_at: STAMP,
    query_count: 1,
    mention_count: 12,
    ...over,
  };
}

/** A settled AsyncState. */
export function loaded<T>(data: T | undefined, error: string | null = null, loading = false): AsyncState<T> {
  return { data, error, loading };
}

/** A fetch Response with a JSON body. */
export function json(body: unknown, status = 200): Response {
  return new Response(body === undefined ? "" : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
