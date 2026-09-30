import { beforeEach, describe, expect, it, vi } from "vitest";
import { accountsApi, api, ApiError, draftsApi, errorMessage, isAbort, onSignedOut, retryAt, scopeParams } from "../api";
import { json } from "./fixtures";

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});

/** Every call gets a fresh Response: a body can be read once. */
function respond(body: unknown, status = 200) {
  fetchMock.mockImplementation(async () => json(body, status));
}

function lastCall(): { url: string; init: RequestInit } {
  const [url, init] = fetchMock.mock.calls.at(-1) ?? [];
  return { url: String(url), init: init ?? {} };
}

describe("accountsApi", () => {
  it("patches limits as JSON", async () => {
    respond({ id: 3 });
    await accountsApi.update(3, { paused: true });
    const { url, init } = lastCall();
    expect(url).toBe("/api/accounts/3");
    expect(init.method).toBe("PATCH");
    expect(init.body).toBe(JSON.stringify({ paused: true }));
    expect(init.headers).toMatchObject({ "Content-Type": "application/json" });
  });

  it("checks one account and all accounts", async () => {
    respond({});
    await accountsApi.check(4);
    expect(lastCall()).toMatchObject({ url: "/api/accounts/4/check", init: { method: "POST" } });
    await accountsApi.checkAll();
    expect(lastCall()).toMatchObject({ url: "/api/accounts/check", init: { method: "POST" } });
  });

  it("asks for stats over a number of days", async () => {
    respond({ days: 7 });
    await accountsApi.stats(7);
    expect(lastCall().url).toBe("/api/accounts/stats?days=7");
    await accountsApi.stats();
    expect(lastCall().url).toBe("/api/accounts/stats?days=30");
  });
});

describe("draftsApi", () => {
  it("lists drafts, filtered or not, and counts them per status", async () => {
    respond([]);
    await expect(draftsApi.list("approved")).resolves.toEqual([]);
    expect(lastCall().url).toBe("/api/drafts?status=approved&limit=500");
    await draftsApi.list(undefined);
    expect(lastCall().url).toBe("/api/drafts?limit=500");
    respond({ draft: 2 });
    await expect(draftsApi.counts()).resolves.toEqual({ draft: 2 });
    expect(lastCall()).toMatchObject({ url: "/api/drafts/counts", init: { method: "GET" } });
  });

  it("reads, edits and moves a draft through review", async () => {
    respond({ id: 9 });
    await draftsApi.get(9);
    expect(lastCall()).toMatchObject({ url: "/api/drafts/9", init: { method: "GET" } });
    await draftsApi.edit(9, { body: "new", account_id: null });
    expect(lastCall()).toMatchObject({ url: "/api/drafts/9", init: { method: "PATCH", body: '{"body":"new","account_id":null}' } });
    await draftsApi.approve(9);
    expect(lastCall()).toMatchObject({ url: "/api/drafts/9/approve", init: { method: "POST" } });
    await draftsApi.skip(9);
    expect(lastCall()).toMatchObject({ url: "/api/drafts/9/skip", init: { method: "POST" } });
    await draftsApi.publish(9);
    expect(lastCall()).toMatchObject({ url: "/api/drafts/9/publish", init: { method: "POST" } });
    respond([{ code: "self_promo", text: "t" }]);
    await expect(draftsApi.warnings(9)).resolves.toEqual([{ code: "self_promo", text: "t" }]);
    expect(lastCall().url).toBe("/api/drafts/9/warnings");
  });

  it("turns a refused publish into an ApiError with the server's message", async () => {
    respond({ error: "limit reached", next_at: "2026-10-01T10:00:00Z" }, 409);
    const err = await draftsApi.publish(9).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 409, message: "limit reached" });
    expect(retryAt(err)).toBe("2026-10-01T10:00:00Z");
  });

  it("finds a retry time only on a 409 that carries one", () => {
    expect(retryAt(new ApiError(409, "not approved", { error: "not approved" }))).toBeNull();
    expect(retryAt(new ApiError(502, "refused", { error: "refused", next_at: "2026-10-01T10:00:00Z" }))).toBeNull();
    expect(retryAt(new ApiError(409, "limit", null))).toBeNull();
    expect(retryAt(new Error("plain"))).toBeNull();
  });

  it("falls back to the status line when the body has no error", async () => {
    fetchMock.mockImplementation(async () => new Response("oops", { status: 502, statusText: "Bad Gateway" }));
    await expect(draftsApi.get(1)).rejects.toMatchObject({ status: 502, message: "502 Bad Gateway" });
    fetchMock.mockImplementation(async () => new Response("", { status: 500 }));
    await expect(draftsApi.get(1)).rejects.toMatchObject({ message: "500 request failed" });
  });

  it("reports an unreachable server and passes aborts through", async () => {
    fetchMock.mockRejectedValue(new TypeError("network"));
    await expect(draftsApi.skip(1)).rejects.toMatchObject({ status: 0, message: expect.stringContaining("Cannot reach") });
    const abort = new DOMException("aborted", "AbortError");
    fetchMock.mockRejectedValue(abort);
    await expect(draftsApi.list(undefined)).rejects.toBe(abort);
  });
});

describe("api", () => {
  const signal = new AbortController().signal;
  const all: [string, () => Promise<unknown>, string, string, string?][] = [
    ["authConfig", () => api.authConfig(signal), "GET", "/api/auth/config"],
    ["me", () => api.me(signal), "GET", "/api/auth/me"],
    ["login", () => api.login("a@b.c", "pw"), "POST", "/api/auth/login", '{"email":"a@b.c","password":"pw"}'],
    ["register", () => api.register("a@b.c", "pw"), "POST", "/api/auth/register", '{"email":"a@b.c","password":"pw"}'],
    ["logout", () => api.logout(), "POST", "/api/auth/logout"],
    ["health", () => api.health(signal), "GET", "/health"],
    ["meta", () => api.meta(signal), "GET", "/api/meta"],
    ["queries", () => api.queries(3, signal), "GET", "/api/queries?p=3"],
    ["tracking", () => api.tracking("go", signal), "GET", "/api/tracking?q=go"],
    ["projects", () => api.projects(signal), "GET", "/api/projects"],
    ["project", () => api.project(3, signal), "GET", "/api/projects/3"],
    ["createProject", () => api.createProject("P"), "POST", "/api/projects", '{"name":"P"}'],
    ["deleteProject", () => api.deleteProject(3), "DELETE", "/api/projects/3"],
    ["renameProject", () => api.renameProject(3, "Q"), "PATCH", "/api/projects/3", '{"name":"Q"}'],
    ["keywords", () => api.keywords(3, signal), "GET", "/api/projects/3/keywords"],
    ["addKeywords", () => api.addKeywords(3, ["a"]), "POST", "/api/projects/3/keywords", '{"queries":["a"]}'],
    ["removeKeyword", () => api.removeKeyword(3, 8), "DELETE", "/api/projects/3/keywords/8"],
    ["projectAccounts", () => api.projectAccounts(3, signal), "GET", "/api/projects/3/accounts"],
    ["bindAccount", () => api.bindAccount(3, "reddit", 5), "PUT", "/api/projects/3/accounts/reddit", '{"account_id":5}'],
    ["unbindAccount", () => api.unbindAccount(3, "reddit"), "DELETE", "/api/projects/3/accounts/reddit"],
    ["unbindProjectAccount", () => api.unbindProjectAccount(3, "reddit", 5), "DELETE", "/api/projects/3/accounts/reddit/5"],
    ["summary", () => api.summary({ kind: "query", q: "go" }, signal), "GET", "/api/summary?q=go"],
    ["mentions", () => api.mentions({ kind: "project", p: 3 }, { sentiment: "negative", limit: 5 }, signal), "GET", "/api/mentions?p=3&sentiment=negative&limit=5"],
    ["track", () => api.track({ query: "go", sources: [], mode: "incremental", pages: 1 }), "POST", "/api/track"],
    ["sourceSettings", () => api.sourceSettings(signal), "GET", "/api/settings/sources"],
    ["saveSourceSettings", () => api.saveSourceSettings("x", { k: "v" }), "PUT", "/api/settings/sources/x", '{"values":{"k":"v"}}'],
    ["resetSourceSettings", () => api.resetSourceSettings("x"), "DELETE", "/api/settings/sources/x"],
    ["accounts", () => api.accounts(signal), "GET", "/api/accounts"],
    ["connectAccount", () => api.connectAccount({ platform: "devto", secret: "k" }), "POST", "/api/accounts", '{"platform":"devto","secret":"k"}'],
    ["importAccounts", () => api.importAccounts("platform,handle,secret\nbluesky,a,k"), "POST", "/api/accounts/import", '{"text":"platform,handle,secret\\nbluesky,a,k"}'],
    ["deleteAccount", () => api.deleteAccount(5), "DELETE", "/api/accounts/5"],
    ["redditAuthorize", () => api.redditAuthorize("id", "s", 3), "POST", "/api/accounts/reddit/authorize", '{"client_id":"id","client_secret":"s","project_id":3}'],
    ["redditApp", () => api.redditApp(signal), "GET", "/api/accounts/reddit/app"],
    ["proxySettings", () => api.proxySettings(signal), "GET", "/api/settings/proxy"],
    ["saveProxy", () => api.saveProxy("http://proxy:8080"), "PUT", "/api/settings/proxy", '{"url":"http://proxy:8080"}'],
    ["resetProxy", () => api.resetProxy(), "DELETE", "/api/settings/proxy"],
  ];

  it.each(all)("%s calls the documented route", async (_name, call, method, url, body) => {
    respond({});
    await call();
    expect(lastCall().url).toBe(url);
    expect(lastCall().init.method).toBe(method);
    if (body !== undefined) expect(lastCall().init.body).toBe(body);
  });

  it("maps a scope to its query parameters", () => {
    expect(scopeParams({ kind: "all" })).toEqual({});
    expect(scopeParams({ kind: "query", q: "go" })).toEqual({ q: "go" });
    expect(scopeParams({ kind: "project", p: 2 })).toEqual({ p: 2 });
  });

  it("keeps a non-JSON success body as null", async () => {
    fetchMock.mockImplementation(async () => new Response("not json", { status: 200 }));
    await expect(api.health()).resolves.toBeNull();
  });

  it("names errors and aborts", () => {
    expect(errorMessage(new Error("boom"))).toBe("boom");
    expect(errorMessage("text")).toBe("text");
    expect(isAbort(new DOMException("x", "AbortError"))).toBe(true);
    expect(isAbort(new Error("x"))).toBe(false);
  });
});

describe("session refresh", () => {
  it("refreshes once on a 401 and retries the request", async () => {
    let first = true;
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === "/api/auth/refresh") return json({ ok: true });
      if (first) {
        first = false;
        return json({ error: "expired" }, 401);
      }
      return json([]);
    });
    await expect(draftsApi.list(undefined)).resolves.toEqual([]);
    expect(fetchMock.mock.calls.map(([u]) => String(u))).toEqual(["/api/drafts?limit=500", "/api/auth/refresh", "/api/drafts?limit=500"]);
  });

  it("signs out when the refresh fails", async () => {
    const out = vi.fn();
    const off = onSignedOut(out);
    fetchMock.mockImplementation(async (input) => (String(input) === "/api/auth/refresh" ? json({}, 401) : json({ error: "expired" }, 401)));
    await expect(accountsApi.stats()).rejects.toMatchObject({ status: 401 });
    expect(out).toHaveBeenCalledTimes(1);
    off();
  });

  it("treats an unreachable refresh as signed out", async () => {
    const out = vi.fn();
    const off = onSignedOut(out);
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === "/api/auth/refresh") throw new TypeError("network");
      return json({ error: "expired" }, 401);
    });
    await expect(api.meta()).rejects.toMatchObject({ status: 401 });
    expect(out).toHaveBeenCalledTimes(1);
    off();
  });

  it("never refreshes for the sign-in routes themselves", async () => {
    respond({ error: "bad password" }, 401);
    await expect(api.login("a@b.c", "x")).rejects.toMatchObject({ status: 401, message: "bad password" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
