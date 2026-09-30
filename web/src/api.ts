import type {
  Account,
  AccountImportResult,
  AccountsResponse,
  AccountStats,
  AccountUpdate,
  AccountView,
  AuthConfig,
  ConnectRequest,
  Draft,
  DraftCounts,
  DraftDetail,
  DraftEdit,
  DraftStatus,
  DraftWarning,
  Keyword,
  Meta,
  Mention,
  Project,
  ProjectPool,
  ProxySettings,
  PublishResult,
  Scope,
  Sentiment,
  SourceSettings,
  SummaryResponse,
  TrackRequest,
  TrackResult,
  Tracking,
  User,
} from "./types";

export class ApiError extends Error {
  readonly status: number;
  /** The parsed error body, e.g. a refused publish's {error, next_at}. */
  readonly data: unknown;
  constructor(status: number, message: string, data: unknown = null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.data = data;
  }
}

type Params = Record<string, string | number | undefined>;

function buildUrl(path: string, params?: Params): string {
  if (!params) return path;
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") qs.set(k, String(v));
  }
  const s = qs.toString();
  return s ? `${path}?${s}` : path;
}

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
type Opts = { params?: Params; body?: unknown; signal?: AbortSignal };

// The session lives in httpOnly cookies the page cannot read. The access
// cookie expires every 15 minutes: on a 401 the client refreshes once, shared
// by every request that failed meanwhile, and retries.
let refreshing: Promise<boolean> | null = null;
const signedOutListeners = new Set<() => void>();

/** Called when the session is gone for good (refresh failed). */
export function onSignedOut(fn: () => void): () => void {
  signedOutListeners.add(fn);
  return () => signedOutListeners.delete(fn);
}

function refreshSession(): Promise<boolean> {
  refreshing ??= fetch("/api/auth/refresh", { method: "POST", headers: { Accept: "application/json" } })
    .then((r) => r.ok, () => false)
    .finally(() => {
      refreshing = null;
    });
  return refreshing;
}

async function request<T>(method: Method, path: string, opts: Opts = {}): Promise<T> {
  try {
    return await send<T>(method, path, opts);
  } catch (e) {
    if (!(e instanceof ApiError) || e.status !== 401 || path.startsWith("/api/auth/")) throw e;
    if (await refreshSession()) return send<T>(method, path, opts);
    signedOutListeners.forEach((fn) => fn());
    throw e;
  }
}

async function send<T>(method: Method, path: string, opts: Opts): Promise<T> {
  const init: RequestInit = { method, signal: opts.signal, headers: { Accept: "application/json" } };
  if (opts.body !== undefined) {
    init.headers = { ...init.headers, "Content-Type": "application/json" };
    init.body = JSON.stringify(opts.body);
  }
  let res: Response;
  try {
    res = await fetch(buildUrl(path, opts.params), init);
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new ApiError(0, "Cannot reach the Radaro server.");
  }
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    const msg =
      data && typeof data === "object" && "error" in data && typeof (data as { error: unknown }).error === "string"
        ? (data as { error: string }).error
        : `${res.status} ${res.statusText || "request failed"}`;
    throw new ApiError(res.status, msg, data);
  }
  return data as T;
}

/** Maps a scope to the `q` / `p` query parameters (mutually exclusive). */
export function scopeParams(scope: Scope): Params {
  if (scope.kind === "query") return { q: scope.q };
  if (scope.kind === "project") return { p: scope.p };
  return {};
}

export const api = {
  authConfig: (signal?: AbortSignal) => request<AuthConfig>("GET", "/api/auth/config", { signal }),

  me: (signal?: AbortSignal) => request<User>("GET", "/api/auth/me", { signal }),

  login: (email: string, password: string) =>
    request<{ user: User }>("POST", "/api/auth/login", { body: { email, password } }),

  register: (email: string, password: string) =>
    request<{ user: User }>("POST", "/api/auth/register", { body: { email, password } }),

  logout: () => request<{ signed_out: boolean }>("POST", "/api/auth/logout"),

  health: (signal?: AbortSignal) =>
    request<{ status: string; database: string }>("GET", "/health", { signal }),

  meta: (signal?: AbortSignal) => request<Meta>("GET", "/api/meta", { signal }),

  queries: (projectId?: number, signal?: AbortSignal) =>
    request<string[]>("GET", "/api/queries", { params: { p: projectId }, signal }),

  tracking: (q: string, signal?: AbortSignal) =>
    request<Tracking>("GET", "/api/tracking", { params: { q }, signal }),

  projects: (signal?: AbortSignal) => request<Project[]>("GET", "/api/projects", { signal }),

  project: (id: number, signal?: AbortSignal) =>
    request<Project>("GET", `/api/projects/${id}`, { signal }),

  createProject: (name: string) => request<Project>("POST", "/api/projects", { body: { name } }),

  deleteProject: (id: number) => request<{ deleted: boolean }>("DELETE", `/api/projects/${id}`),

  renameProject: (id: number, name: string) => request<Project>("PATCH", `/api/projects/${id}`, { body: { name } }),

  keywords: (id: number, signal?: AbortSignal) => request<Keyword[]>("GET", `/api/projects/${id}/keywords`, { signal }),

  addKeywords: (id: number, queries: string[]) =>
    request<{ added: number; keywords: Keyword[] }>("POST", `/api/projects/${id}/keywords`, { body: { queries } }),

  removeKeyword: (id: number, keywordId: number) =>
    request<{ deleted: boolean }>("DELETE", `/api/projects/${id}/keywords/${keywordId}`),

  /** The project's account pool, one entry per publishing platform. */
  projectAccounts: (id: number, signal?: AbortSignal) =>
    request<ProjectPool[]>("GET", `/api/projects/${id}/accounts`, { signal }),

  /** Adds an account to the project's pool for its platform (idempotent). */
  bindAccount: (id: number, platform: string, accountId: number) =>
    request<ProjectPool[]>("PUT", `/api/projects/${id}/accounts/${encodeURIComponent(platform)}`, {
      body: { account_id: accountId },
    }),

  /** Empties the project's pool for one platform. */
  unbindAccount: (id: number, platform: string) =>
    request<ProjectPool[]>("DELETE", `/api/projects/${id}/accounts/${encodeURIComponent(platform)}`),

  /** Removes one account from the project's pool. */
  unbindProjectAccount: (id: number, platform: string, accountId: number) =>
    request<ProjectPool[]>("DELETE", `/api/projects/${id}/accounts/${encodeURIComponent(platform)}/${accountId}`),

  summary: (scope: Scope, signal?: AbortSignal) =>
    request<SummaryResponse>("GET", "/api/summary", { params: scopeParams(scope), signal }),

  mentions: (
    scope: Scope,
    filters: { source?: string; sentiment?: Sentiment; limit?: number },
    signal?: AbortSignal,
  ) =>
    request<Mention[]>("GET", "/api/mentions", {
      params: { ...scopeParams(scope), source: filters.source, sentiment: filters.sentiment, limit: filters.limit },
      signal,
    }),

  track: (body: TrackRequest) => request<TrackResult>("POST", "/api/track", { body }),

  sourceSettings: (signal?: AbortSignal) =>
    request<SourceSettings[]>("GET", "/api/settings/sources", { signal }),

  /** A blank secret keeps the one saved before. */
  saveSourceSettings: (name: string, values: Record<string, string>) =>
    request<SourceSettings>("PUT", `/api/settings/sources/${encodeURIComponent(name)}`, { body: { values } }),

  resetSourceSettings: (name: string) =>
    request<SourceSettings>("DELETE", `/api/settings/sources/${encodeURIComponent(name)}`),

  accounts: (signal?: AbortSignal) => request<AccountsResponse>("GET", "/api/accounts", { signal }),

  connectAccount: (body: ConnectRequest) => request<Account>("POST", "/api/accounts", { body }),

  importAccounts: (text: string) => request<AccountImportResult>("POST", "/api/accounts/import", { body: { text } }),

  deleteAccount: (id: number) => request<{ deleted: boolean }>("DELETE", `/api/accounts/${id}`),

  redditAuthorize: (clientId: string, clientSecret: string, projectId?: number) =>
    request<{ authorize_url: string; redirect_uri: string }>("POST", "/api/accounts/reddit/authorize", {
      body: { client_id: clientId, client_secret: clientSecret, project_id: projectId },
    }),

  redditApp: (signal?: AbortSignal) => request<{ configured: boolean }>("GET", "/api/accounts/reddit/app", { signal }),

  proxySettings: (signal?: AbortSignal) => request<ProxySettings>("GET", "/api/settings/proxy", { signal }),

  saveProxy: (url: string) => request<ProxySettings>("PUT", "/api/settings/proxy", { body: { url } }),

  resetProxy: () => request<ProxySettings>("DELETE", "/api/settings/proxy"),
};

export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

export function isAbort(e: unknown): boolean {
  return e instanceof DOMException && e.name === "AbortError";
}

// ---------- Accounts: health, limits and ban rate ----------

export const accountsApi = {
  /** A limit of 0 returns it to the platform default. */
  update: (id: number, body: AccountUpdate) => request<AccountView>("PATCH", `/api/accounts/${id}`, { body }),

  check: (id: number) => request<AccountView>("POST", `/api/accounts/${id}/check`),

  checkAll: () => request<AccountsResponse>("POST", "/api/accounts/check"),

  stats: (days = 30, signal?: AbortSignal) =>
    request<AccountStats>("GET", "/api/accounts/stats", { params: { days }, signal }),
};

// ---------- Drafts: the publishing queue ----------

export const draftsApi = {
  list: (status: DraftStatus | undefined, signal?: AbortSignal) =>
    request<Draft[]>("GET", "/api/drafts", { params: { status, limit: 500 }, signal }),

  /** Drafts per status, whatever the filter. */
  counts: (signal?: AbortSignal) => request<DraftCounts>("GET", "/api/drafts/counts", { signal }),

  get: (id: number, signal?: AbortSignal) => request<DraftDetail>("GET", `/api/drafts/${id}`, { signal }),

  /** Any change sends the draft back to review. */
  edit: (id: number, body: DraftEdit) => request<DraftDetail>("PATCH", `/api/drafts/${id}`, { body }),

  approve: (id: number) => request<Draft>("POST", `/api/drafts/${id}/approve`),

  skip: (id: number) => request<Draft>("POST", `/api/drafts/${id}/skip`),

  /** Only approved drafts; a limit answers 409 {error, next_at}. */
  publish: (id: number) => request<PublishResult>("POST", `/api/drafts/${id}/publish`),

  warnings: (id: number, signal?: AbortSignal) =>
    request<DraftWarning[]>("GET", `/api/drafts/${id}/warnings`, { signal }),
};

/** When a refused publish may be retried: the next_at of its 409 body, if any. */
export function retryAt(e: unknown): string | null {
  if (!(e instanceof ApiError) || e.status !== 409) return null;
  const d = e.data;
  if (d && typeof d === "object" && "next_at" in d && typeof d.next_at === "string") return d.next_at;
  return null;
}
