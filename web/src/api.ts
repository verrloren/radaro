import type {
  Account,
  AccountsResponse,
  AuthConfig,
  ConnectRequest,
  Keyword,
  Meta,
  Mention,
  Project,
  ProjectAccount,
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
  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
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
    throw new ApiError(res.status, msg);
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

  projectAccounts: (id: number, signal?: AbortSignal) =>
    request<ProjectAccount[]>("GET", `/api/projects/${id}/accounts`, { signal }),

  bindAccount: (id: number, platform: string, accountId: number) =>
    request<ProjectAccount[]>("PUT", `/api/projects/${id}/accounts/${encodeURIComponent(platform)}`, {
      body: { account_id: accountId },
    }),

  unbindAccount: (id: number, platform: string) =>
    request<ProjectAccount[]>("DELETE", `/api/projects/${id}/accounts/${encodeURIComponent(platform)}`),

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

  deleteAccount: (id: number) => request<{ deleted: boolean }>("DELETE", `/api/accounts/${id}`),

  redditAuthorize: (clientId: string, clientSecret: string, projectId?: number) =>
    request<{ authorize_url: string; redirect_uri: string }>("POST", "/api/accounts/reddit/authorize", {
      body: { client_id: clientId, client_secret: clientSecret, project_id: projectId },
    }),
};

export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

export function isAbort(e: unknown): boolean {
  return e instanceof DOMException && e.name === "AbortError";
}
