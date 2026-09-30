# Radaro HTTP API

`radaro serve` exposes a JSON API on the same origin as the dashboard
(default `http://127.0.0.1:8042`). Every response is JSON. Errors use
`{"error": "message"}` with a 4xx/5xx status.

Mutating requests (`POST`, `PUT`, `DELETE`) are rejected with `403` when the
browser sends an `Origin` header that does not match the server host.

## Authentication

Everything under `/api/` except `/api/auth/{config,register,login,refresh,logout}`
needs a signed-in user; without one the answer is `401`. `/health` and the
Reddit OAuth callback are public.

- **Dashboard (browser):** sign-in sets two httpOnly, `SameSite=Lax` cookies:
  `radaro_access` (a JWT, 15 minutes) and `radaro_refresh` (30 days, sent only
  to `/api/auth`). Scripts never see a token. On `401`, call
  `POST /api/auth/refresh` once and retry.
- **CLI and agents:** send `X-Radaro-Client: cli`. Sign-in then returns the
  tokens in the body and sets no cookies; send `Authorization: Bearer <access_token>`
  and refresh with `{"refresh_token": ...}` in the body.

Refresh tokens rotate on every use. Presenting an already rotated token again
(after a 30-second grace for parallel tabs) signs out every session of that
user. Register, login and refresh allow 10 attempts per minute per client
(`429` beyond). Behind a reverse proxy on the same machine or a private network (such as a
Docker bridge), the client is the last `X-Forwarded-For` entry, and `X-Forwarded-Proto: https` makes the cookies
`Secure`.

The first user to register becomes the admin and is the only one who may
change instance-wide source keys. After that, registration is open only with
`RADARO_REGISTRATION=open`.

```ts
interface User {
  id: number;
  email: string;
  is_admin: boolean;
  created_at: string;
}

interface Session {               // CLI only (X-Radaro-Client: cli)
  user: User;
  access_token: string;
  access_expires_at: string;      // RFC 3339
  refresh_token: string;
  refresh_expires_at: string;
}
```

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/auth/config` | — | `{"registration_open": boolean}` |
| POST | `/api/auth/register` | `{"email": string, "password": string}` | `201`: browser `{"user": User, "access_expires_at": string}` + cookies, CLI `Session`; `403` registration closed; `409` email taken; `422` invalid email or password (8 characters to 72 bytes) |
| POST | `/api/auth/login` | `{"email": string, "password": string}` | as register, `200`; `401` `invalid email or password` |
| POST | `/api/auth/refresh` | CLI: `{"refresh_token": string}`; browser: the cookie | as login; `401` when the token is unknown, expired, signed out or reused |
| POST | `/api/auth/logout` | as refresh | `{"signed_out": true}`; clears the cookies |
| GET | `/api/auth/me` | — | `User` |

## Types

```ts
type Sentiment = "positive" | "neutral" | "negative";

interface SourceInfo {
  name: string;          // "hackernews"
  label: string;         // "Hacker News"
  glyph: string;         // short badge text, e.g. "Y", "r/", "◈"
  color: string;         // accent hex, e.g. "#ff6a3d"
  needs_config: boolean; // needs credentials/feeds to work
  configured: boolean;   // credentials present in this instance
}

interface Mention {
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
  created_at: string;         // RFC 3339
  score: number | null;       // upvotes / likes, source-dependent
  sentiment: Sentiment | null;
  sentiment_score: number | null; // [-1, 1]
  theme: string | null;
}

interface Project {
  id: number;
  name: string;
  is_default: boolean;        // every user has one; it cannot be deleted
  created_at: string;
  updated_at: string;
  query_count: number;
  mention_count: number;
  queries?: string[];         // only on single-project responses
}

interface Keyword {
  id: number;
  project_id: number;
  query: string;
  sources: string[];          // what a scan of this keyword covers
  added_at: string;
  last_scanned_at: string | null;
  mention_count: number;
}

interface ProjectAccount {
  platform: Platform;
  account: Account | null;    // the account this project publishes and scans with
}

interface SourceState {
  source: string;
  newest_at: string | null;
  oldest_at: string | null;
  backfill_complete: boolean;
  last_success_at: string | null;
  last_error: string | null;
}

interface Tracking {
  query: string;
  sources: string[];
  created_at: string;
  updated_at: string;
  last_scanned_at: string | null;
  source_states: SourceState[];
}

interface Summary {
  total: number;
  by_sentiment: Record<string, number>; // keys: positive | neutral | negative
  by_source: Record<string, number>;
  by_day: Record<string, number>;       // "YYYY-MM-DD" -> count
}

interface TimeseriesPoint {
  date: string;  // "YYYY-MM-DD", only days that have mentions
  positive: number;
  neutral: number;
  negative: number;
  total: number;
}

interface Theme { label: string; count: number; }

interface TrackResult {
  query: string;
  project_id: number | null;
  mode: "incremental" | "backfill";
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

// Settings for a source that needs keys. Secret values are never returned.
interface SourceSettings {
  name: string;
  label: string;
  configured: boolean;
  saved: boolean;              // has values saved from the dashboard
  fields: {
    key: string;
    label: string;
    secret: boolean;
    multiline?: boolean;
    placeholder?: string;
    env: string;               // the RADARO_* variable it overrides
    set: boolean;
    origin: "ui" | "env" | ""; // saved value wins over the environment
    value?: string;            // non-secret fields only
  }[];
}

interface Platform { name: string; label: string; replies: boolean; titles: boolean; max_chars: number; }

// A connected publishing account; credentials are never returned.
interface Account { id: number; platform: string; handle: string; created_at: string; updated_at: string; }

interface Draft {
  id: number;
  project_id: number | null;
  platform: string;
  account_id: number | null;
  kind: "post" | "reply";
  community: string | null;
  title: string | null;
  body: string;
  reply_to: string | null;
  query: string | null;
  mention_id: string | null;
  status: "draft" | "approved" | "publishing" | "published" | "failed" | "skipped";
  remote_id: string | null;
  remote_url: string | null;
  error: string | null;
  metrics: Record<string, unknown> | null;
  metrics_at: string | null;
  created_at: string;
  updated_at: string;
  approved_at: string | null;
  published_at: string | null;
}

interface Activity { id: number; at: string; action: string; draft_id: number | null; detail: string; }

interface Report {
  query?: string;
  summary: Summary;
  net: number;
  themes: Theme[];
  top_positive: Mention[];
  top_negative: Mention[];
}
```

## Endpoints

A project has at most one account per platform. Drafts belong to a project
(the user's Default when none is given) and publish with the account the draft
names, else the project's account, else the user's only account on that
platform. Scans of a project's keyword use the project's Reddit and Mastodon
accounts first.

Every user sees only their own projects, keywords, accounts, drafts and
activity. Mentions belong to keywords, so a user sees the mentions of the
keywords in their projects; two users tracking the same keyword share the
scanned data. A resource of another user answers `404`, exactly like one that
does not exist.

| Method | Path | Body / query | Response |
|---|---|---|---|
| GET | `/health` | — | `{"status":"ok","database":"ok"}` |
| GET | `/api/meta` | — | `{"version": string, "sources": SourceInfo[], "default_sources": string[]}` |
| GET | `/api/queries` | `?p=<project id>` optional | `string[]` — tracked keywords, most recently active first |
| GET | `/api/tracking` | `?q=<keyword>` | `Tracking` (404 if unknown) |
| GET | `/api/projects` | — | the user's `Project[]`, Default first |
| GET | `/api/projects/{id}` | — | `Project` with `queries` |
| POST | `/api/projects` | `{"name": string}` | `201 Project`; `409` duplicate or beyond 100 projects |
| DELETE | `/api/projects/{id}` | — | `{"deleted": true}`; `409` for the Default project |
| PATCH | `/api/projects/{id}` | `{"name": string}` | `Project`; `409` duplicate |
| GET | `/api/projects/{id}/keywords` | — | `Keyword[]`, oldest first |
| POST | `/api/projects/{id}/keywords` | `{"query"?: string, "queries"?: string[], "sources"?: string[]}` | `201` (`200` when nothing was new) `{"added": number, "keywords": Keyword[]}`. Keywords already in the project, in any letter case, are skipped. New keywords scan `sources`, default the instance's `RADARO_SOURCES`. `409` beyond 500 keywords per project |
| DELETE | `/api/projects/{id}/keywords/{kid}` | — | `{"deleted": true}`; the keyword's mentions stay |
| GET | `/api/projects/{id}/accounts` | — | `ProjectAccount[]`, one per publishing platform |
| PUT | `/api/projects/{id}/accounts/{platform}` | `{"account_id": number}` | `ProjectAccount[]`; replaces the project's account for that platform; `404` unknown account, `422` account of another platform |
| DELETE | `/api/projects/{id}/accounts/{platform}` | — | `ProjectAccount[]` |
| GET | `/api/summary` | `?q=` or `?p=` (mutually exclusive; neither = everything) | `{"summary": Summary, "timeseries": TimeseriesPoint[], "net": number, "themes": Theme[]}` |
| GET | `/api/mentions` | `?q=` or `?p=`, `&source=`, `&sentiment=`, `&limit=` (1–1000, default 200) | `Mention[]`, newest first |
| POST | `/api/track` | `{"query": string, "sources"?: string[], "mode": "incremental" \| "backfill", "pages": number, "limit"?: number, "project_id"?: number}` | `TrackResult` (may take minutes). Without `sources`, the keyword's own, else `RADARO_SOURCES`; `limit` (1–100) is items per source page. The keyword is filed under `project_id`, or stays where it is, or goes to Default |
| GET | `/api/report` | `?q=` or `?p=` | `Report` |
| GET | `/api/export` | `?q=` or `?p=` | every `Mention` in scope, complete (for backups and CSV) |
| GET | `/api/opportunities` | `?q=` or `?p=`, `&days=` (1–365, default 14), `&limit=` (1–200, default 20), `&source=` | `Mention[]`: recent mentions with a link and no draft yet |
| GET | `/api/drafts` | `?status=`, `&limit=` (1–1000, default 50) | `Draft[]`, newest first |
| POST | `/api/drafts` | `{"platform": string, "body": string, "project_id"?, "account_id"?, "kind"?: "post" \| "reply", "community"?, "title"?, "reply_to"?, "query"?, "mention_id"?}` | `201 Draft`. With `mention_id` and no `reply_to`, a reply answers the mention's thread on the same platform. `422` when the platform's rules reject it |
| GET | `/api/drafts/{id}` | — | `Draft` |
| PATCH | `/api/drafts/{id}` | `{"title"?, "body"?, "community"?}` | `Draft`, back in review (`draft`); `409` once published |
| POST | `/api/drafts/{id}/approve` | — | `Draft` (`approved`); `422` if the platform would reject it |
| POST | `/api/drafts/{id}/skip` | — | `Draft` (`skipped`) |
| POST | `/api/drafts/{id}/publish` | — | `Draft` (`published`). Only `approved` drafts (`409` otherwise); the draft is claimed (`publishing`) before any network call. `502 {"error", "draft"}` when the platform fails (`failed`, approve again to retry) |
| GET | `/api/stats` | `?refresh=true` fetches fresh metrics | `{"published": Draft[], "errors": string[]}` |
| GET | `/api/activity` | `?limit=` (1–1000, default 50) | `Activity[]`, newest first |
| GET | `/api/status` | — | the user, sources, accounts, keywords and draft counts; the admin also sees the database path |
| GET | `/api/settings/sources` | — | `SourceSettings[]` for the sources that take keys (RSS, X, YouTube); Reddit and Mastodon scan with a connected account instead |
| PUT | `/api/settings/sources/{name}` | `{"values": Record<string, string>}` | admin only (`403` otherwise). `SourceSettings`; a blank secret keeps the saved one, all blank removes the saved settings; `422` unknown field; `404` source without settings |
| DELETE | `/api/settings/sources/{name}` | — | admin only. `SourceSettings` after falling back to the environment |
| GET | `/api/accounts` | — | `{"platforms": Platform[], "accounts": Account[]}` |
| POST | `/api/accounts` | `{"platform": "bluesky" \| "mastodon" \| "devto", "handle"?: string, "instance"?: string, "secret": string, "project_id"?: number}` | `201 Account` once the platform accepts the key, bound to `project_id` when given; `422` with the platform's refusal |
| DELETE | `/api/accounts/{id}` | — | `{"deleted": true}`; drafts keep their text |
| POST | `/api/accounts/reddit/authorize` | `{"client_id": string, "client_secret"?: string, "project_id"?: number}` | `{"authorize_url": string, "redirect_uri": string}` — open `authorize_url`; the state is single use and expires in 10 minutes |
| GET | `/oauth/reddit/callback` | Reddit's `?state=&code=` | `303` to `/?v=setup&connected=reddit`, or `&connect_error=<message>` |

`net` is `(positive − negative) / total`, in `[-1, 1]`.

Source keys saved through `/api/settings/sources` override the matching `RADARO_*` variables field by field and apply to the next scan without a restart. The Reddit redirect URI is the callback on the address the dashboard is open at (for example `http://127.0.0.1:8042/oauth/reddit/callback`); it must match the one registered in the Reddit app.
