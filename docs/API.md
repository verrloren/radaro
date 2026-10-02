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
| GET | `/api/projects/{id}/accounts` | — | `ProjectPool[]`, one per publishing platform, each with an `accounts` array |
| PUT | `/api/projects/{id}/accounts/{platform}` | `{"account_id": number}` | `ProjectPool[]`; adds the account to the pool, `404` unknown account, `422` account of another platform |
| DELETE | `/api/projects/{id}/accounts/{platform}` | — | `ProjectPool[]`; empties that platform's pool |
| DELETE | `/api/projects/{id}/accounts/{platform}/{account_id}` | — | `ProjectPool[]`; removes only that account from the pool |
| GET | `/api/summary` | `?q=` or `?p=` (mutually exclusive; neither = everything) | `{"summary": Summary, "timeseries": TimeseriesPoint[], "net": number, "themes": Theme[]}` |
| GET | `/api/mentions` | `?q=` or `?p=`, `&source=`, `&sentiment=`, `&limit=` (1–1000, default 200) | `Mention[]`, newest first |
| POST | `/api/track` | `{"query": string, "sources"?: string[], "mode": "incremental" \| "backfill", "pages": number, "limit"?: number, "project_id"?: number}` | `TrackResult` (may take minutes). Without `sources`, the keyword's own, else `RADARO_SOURCES`; `limit` (1–100) is items per source page. The keyword is filed under `project_id`, or stays where it is, or goes to Default |
| GET | `/api/report` | `?q=` or `?p=` | `Report` |
| GET | `/api/export` | `?q=` or `?p=` | every `Mention` in scope, complete (for backups and CSV) |
| GET | `/api/opportunities` | `?q=` or `?p=`, `&days=` (1–365, default 14), `&limit=` (1–200, default 20), `&source=` | `Mention[]`: recent mentions with a link and no draft yet |
| GET | `/api/drafts` | `?status=`, `&limit=` (1–1000, default 50) | `Draft[]`, newest first |
| GET | `/api/drafts/counts` | — | counts by draft status, including zeroes |
| POST | `/api/drafts` | `{"platform": string, "body": string, "project_id"?, "account_id"?, "kind"?: "post" \| "reply", "community"?, "title"?, "reply_to"?, "query"?, "mention_id"?}` | `201 Draft`. With `mention_id` and no `reply_to`, a reply answers the mention's thread on the same platform. `422` when the platform's rules reject it |
| GET | `/api/drafts/{id}` | — | `Draft` with `plan` (selected account, quota, next possible time and reason) |
| PATCH | `/api/drafts/{id}` | `{"title"?, "body"?, "community"?, "account_id"?: number \| null}` | `Draft` with `plan`, back in review (`draft`); `null` restores automatic account selection; `409` once published |
| GET | `/api/drafts/{id}/warnings` | — | `DraftWarning[]` for self-promotion, duplicate posts and subreddit rules; warnings advise but do not block publishing |
| POST | `/api/drafts/{id}/approve` | — | `Draft` with `plan` (`approved`); `422` if the platform would reject it |
| POST | `/api/drafts/{id}/skip` | — | `Draft` (`skipped`) |
| POST | `/api/drafts/{id}/publish` | — | `{"draft": Draft, "account": Account}`. Only `approved` drafts (`409` otherwise); the draft is claimed (`publishing`) before any network call. A limit returns `409 {"error", "draft", "account", "next_at"}` without publishing; a platform failure returns `502` and marks the draft `failed` |
| GET | `/api/stats` | `?refresh=true` fetches fresh metrics | `{"published": Draft[], "errors": string[]}` |
| GET | `/api/activity` | `?limit=` (1–1000, default 50) | `Activity[]`, newest first |
| GET | `/api/status` | — | the user, sources, accounts, keywords and draft counts; the admin also sees the database path |
| GET | `/api/settings/sources` | — | `SourceSettings[]` for the sources that take keys (RSS, X, YouTube); Reddit and Mastodon scan with a connected account instead |
| PUT | `/api/settings/sources/{name}` | `{"values": Record<string, string>}` | admin only (`403` otherwise). `SourceSettings`; a blank secret keeps the saved one, all blank removes the saved settings; `422` unknown field; `404` source without settings |
| DELETE | `/api/settings/sources/{name}` | — | admin only. `SourceSettings` after falling back to the environment |
| GET | `/api/settings/proxy` | — | admin only. `{"configured": boolean, "origin": "ui" \| "env" \| ""}`; never returns the proxy URL, which may contain credentials |
| PUT | `/api/settings/proxy` | `{"url": "http://host:port"}` (also HTTPS or SOCKS5) | admin only. Saves one instance-wide outbound proxy for scans and publishing, active without restart; `422` invalid URL |
| DELETE | `/api/settings/proxy` | — | admin only. Clears the saved proxy and restores `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` from the server environment |
| GET | `/api/accounts` | — | `{"platforms": Platform[], "accounts": AccountView[]}` with health, limits, quota and activity for this user |
| POST | `/api/accounts` | `{"platform": "bluesky" \| "mastodon" \| "devto", "handle"?: string, "instance"?: string, "secret": string, "project_id"?: number}` | `201 Account` once the platform accepts the key, bound to `project_id` when given; `422` with the platform's refusal |
| POST | `/api/accounts/import` | `{"text": string}` containing CSV or a JSON array | `{"added": number, "updated": number, "errors": number, "results": [{"row", "platform", "handle", "status", "account_id"?, "error"?}]}`; up to 500 rows, at most three credential checks at once, row errors do not stop valid rows, no secrets returned; Reddit rows direct the user to browser sign-in |
| PATCH | `/api/accounts/{id}` | `{"paused"?: boolean, "daily_limit"?: number, "min_interval_sec"?: number, "community_cooldown_h"?: number}` | `AccountView`; a limit of `0` restores its platform default |
| POST | `/api/accounts/{id}/check` | — | `AccountView` with refreshed health; `502` if the check could not run |
| POST | `/api/accounts/check` | — | account list after checking this user's accounts; per-account failures are reported in `error` and `errors` |
| GET | `/api/accounts/stats` | `?days=` (1–90, default 30) | status totals and daily ban-rate series for this user's accounts |
| DELETE | `/api/accounts/{id}` | — | `{"deleted": true}`; drafts keep their text |
| POST | `/api/accounts/reddit/authorize` | `{"client_id"?: string, "client_secret"?: string, "project_id"?: number}` | `{"authorize_url": string, "redirect_uri": string}` — open `authorize_url`; omitted client ID reuses the user's first connected Reddit account's app; the state is single use and expires in 10 minutes |
| GET | `/api/accounts/reddit/app` | — | `{"configured": boolean}` for this user's reusable Reddit app; no app secret returned |
| GET | `/api/accounts/reddit/browser` | — | `{"available": boolean}` for Chromium on this server |
| POST | `/api/accounts/reddit/browser` | `{"username": string, "password": string, "proxy_url"?: string, "project_id"?: number}` | `201 {"session_id", "expires_at", "screen": {"image", "width", "height"}}`; starts an isolated Reddit browser sign-in; image is base64 PNG, passwords are not persisted |
| POST | `/api/accounts/reddit/browser/{session}/input` | `{"kind": "refresh" \| "click" \| "text" \| "key" \| "scroll", "x"?, "y"?, "text"?, "key"?, "delta"?}` | Updated browser screen; coordinates are in the returned viewport pixels; CAPTCHA and 2FA are completed by the user |
| POST | `/api/accounts/reddit/browser/{session}/finish` | `{}` | `201 Account` after the browser identity is verified and cookies saved server-side; `409` while verification is incomplete |
| DELETE | `/api/accounts/reddit/browser/{session}` | — | `{"cancelled": true}`; closes the pending browser |
| PUT | `/api/accounts/{id}/browser/proxy` | `{"proxy_url": string}` | `{"configured": boolean}`; changes this browser account's proxy, empty restores a direct connection |
| GET | `/oauth/reddit/callback` | Reddit's `?state=&code=` | `303` to `/?v=setup&connected=reddit`, or `&connect_error=<message>` |

`net` is `(positive − negative) / total`, in `[-1, 1]`.

Source keys saved through `/api/settings/sources` override the matching `RADARO_*` variables field by field and apply to the next scan without a restart. The Reddit redirect URI is the callback on the address the dashboard is open at (for example `http://127.0.0.1:8042/oauth/reddit/callback`); it must match the one registered in the Reddit app.

Browser sign-ins belong to the signed-in user, expire after ten minutes, and are limited to two pending sessions per user and four simultaneous Chromium processes per server. Another user's sign-in returns `404`. Screens are not cached. Cookies and proxy credentials never appear in API responses; `AccountView.browser.proxy_configured` reports only whether a browser account has its own proxy. HTTP, HTTPS and SOCKS5 proxies support authentication. A failed proxy never falls back to a direct connection. Existing OAuth accounts retain their API connector. Browser accounts use website forms for publication and website pages for scans, metrics and rules. Their publication ID is the resulting Reddit permalink. If submission was dispatched but confirmation is missing, the draft stays `publishing` and must be checked on Reddit before any retry.

When Reddit displays its IP rate-limit or network-security block page, browser operations return an explicit error and refuse further sign-in input. The dashboard stops automatic screenshot polling on that error; cancel the pending sign-in and wait before retrying an IP rate limit. These blocks do not indicate whether the account password is correct.

The Docker image runs Chromium in headed mode on a private Xvfb display for both sign-in and subsequent account operations. `RADARO_BROWSER_HEADED=true` enables this mode outside Docker and requires `DISPLAY`; a missing display fails explicitly. DevTools ports bind only to loopback and are allocated separately for concurrent browser sessions. Browser operations retain the same per-user ownership, proxy isolation and approved-draft requirements in either mode.

### Reddit reading and reply preparation

`GET /api/mentions/{mention}/reddit?project_id=<id>` returns `{post, project_id, draft}`. The mention and optional project must belong to the signed-in user's scope; omitting the project resolves one of their projects tracking that keyword. The post is read through the connected browser account and its proxy. `post` contains URL, title, full text (up to 40,000 characters), author, community, creation time, nullable `score`, `comments`, `upvote_ratio`, flair, `locked`, `archived`, `removed`, `nsfw`, `can_reply`, up to twelve loaded comments, subreddit rules and `fetched_at`. Missing statistics remain `null`. Score is the value displayed by Reddit. This request never submits a comment. `draft` is the existing reply draft for that mention/project, or null.

`POST /api/mentions/{mention}/reply?project_id=<id>` accepts `{body: string}` for a manual reply or `{generate: true}` to prepare one with the configured LLM. It returns a draft with its publish plan (`201` new, `200` existing). Repeated clicks reuse the draft, including published/publishing drafts; overlapping preparations return `409`. LLM preparation reads the post, loaded comments, subreddit rules and project context. It requires a configured provider and an open post. Preparation only creates status `draft`; approval and publication still use the existing outbox routes. A provider failure returns a sanitized error without saving a partial draft.

`GET /api/projects/{id}/reply-settings` and `PUT /api/projects/{id}/reply-settings` read/write `{brief, instructions, language, tone}` for an owned project. `brief` describes what to promote (verified product facts, audience, benefits and links); `instructions` specifies what to base the reply on, its focus, evidence, talking points and call to action. Limits: brief and instructions 8,000 bytes each, language 100 bytes, tone 500 bytes. Defaults use the post's language and a helpful, concise tone. No product claims are invented when the brief is empty. The dashboard shows these fields in project setup and Reply. It waits for saved context before automatic preparation; an explicit preparation without product context remains available for replies without promotion.

`POST /api/mentions/{mention}/reply?project_id=<id>` with `{generate: true, regenerate: true, draft_id: <existing reply id>}` explicitly replaces the saved draft text using current project settings. It preserves the draft ID and reply target, resets approval and returns status `draft`. Publishing/published replies cannot be regenerated. If the draft changes during generation, regeneration returns `409` and keeps the newer text/status. Provider failures keep the existing draft unchanged. Saving project settings alone never replaces existing drafts.

Mentions optionally include `reddit: {community, comments, upvote_ratio?}` from the scan. Comments and ratio are nullable; old mentions gain these values as they are scanned again.

### LLM connection status

`GET /api/settings/llm` returns `{provider, model, configured, status, checked_at, detail}` without keys or endpoint credentials. Status is `not_configured`, `unchecked`, `connected` or `error`. Configuration alone is not a successful connection check. `POST /api/settings/llm/check` is admin-only and performs one short completion (30-second timeout); its result and the result of reply preparation update the last request status. Checks are explicit and may consume provider tokens. Status is held in memory and becomes unchecked on server restart. Provider/model/credentials continue to be configured on the server through the existing `RADARO_LLM_*` variables; there is no credential-entry form in the dashboard.

`RADARO_LLM_PROVIDER=codex` uses a dedicated Codex CLI ChatGPT auth cache (`RADARO_CODEX_HOME/auth.json`), with CLI-managed token renewal. Set `RADARO_LLM_MODEL` and optionally `RADARO_CODEX_PATH`; the Docker image includes pinned Codex 0.160.0 and defaults its home to `/data/codex`. Keep that directory private and writable by the Radaro user. Each completion runs without saved conversation history or user/project configuration, in an empty temporary workspace with shell, browser, apps, MCP plugins, hooks and delegation disabled. It receives only the supplied prompt and uses a minimal environment. Requests are serialized to avoid concurrent token renewal. It prepares text only; the normal draft approval and publication routes apply. ChatGPT plan limits apply, and an expired/revoked login appears as a failed connection check.

`RADARO_LLM_REASONING_EFFORT` configures Codex reasoning (default `medium`). The LLM status response includes `reasoning_effort` for Codex, displayed alongside the model in Setup.

The dashboard has **Read post** and **Reply** on Reddit mentions. Reply prepares one draft automatically when an LLM is configured; manual editing remains available. Reddit replies offer **Approve & publish…** with the full text and target shown for confirmation, then call approval followed by publication. Approval failure prevents the publish call.
