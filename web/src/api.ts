import type {
  Account,
  AccountsResponse,
  ConnectRequest,
  Meta,
  Mention,
  Project,
  Scope,
  Sentiment,
  SourceSettings,
  SummaryResponse,
  TrackRequest,
  TrackResult,
  Tracking,
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

async function request<T>(
  method: "GET" | "POST" | "PUT" | "DELETE",
  path: string,
  opts: { params?: Params; body?: unknown; signal?: AbortSignal } = {},
): Promise<T> {
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
    throw new ApiError(0, "Cannot reach the Radaro server. Is `radaro serve` running?");
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

  addProjectQuery: (id: number, query: string) =>
    request<{ added: boolean; project: Project }>("POST", `/api/projects/${id}/queries`, {
      body: { query },
    }),

  removeProjectQuery: (id: number, query: string) =>
    request<{ removed: boolean; project: Project }>("DELETE", `/api/projects/${id}/queries`, {
      body: { query },
    }),

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

  redditAuthorize: (clientId: string, clientSecret: string) =>
    request<{ authorize_url: string; redirect_uri: string }>("POST", "/api/accounts/reddit/authorize", {
      body: { client_id: clientId, client_secret: clientSecret },
    }),
};

export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

export function isAbort(e: unknown): boolean {
  return e instanceof DOMException && e.name === "AbortError";
}
