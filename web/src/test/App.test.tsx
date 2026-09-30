import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "../api";
import App from "../App";
import type { SummaryResponse, TrackResult } from "../types";
import { project, USER } from "./fixtures";

// The views have their own tests; here they only show which one is routed.
vi.mock("../components/DraftsView", () => ({
  DraftsView: () => <p>drafts view</p>,
}));
vi.mock("../components/AccountsView", () => ({
  AccountsView: ({ onSetup }: Readonly<{ onSetup: () => void }>) => (
    <button type="button" onClick={onSetup}>
      accounts view
    </button>
  ),
}));
vi.mock("../components/ScanCard", () => ({
  ScanCard: ({ onScanned, onSetup, onListen }: Readonly<{ onScanned: (r: TrackResult) => void; onSetup: () => void; onListen: () => void }>) => (
    <div>
      <p>scan view</p>
      <button type="button" onClick={() => onScanned({ query: "golang" } as TrackResult)}>
        scanned
      </button>
      <button type="button" onClick={onSetup}>
        to setup
      </button>
      <button type="button" onClick={onListen}>
        to listen
      </button>
    </div>
  ),
}));
vi.mock("../components/MentionsFeed", () => ({ MentionsFeed: () => <p>mentions feed</p> }));
vi.mock("../components/Report", () => ({ Report: ({ subject }: Readonly<{ subject: string }>) => <p>report on {subject}</p> }));
vi.mock("../components/SentimentCard", () => ({ SentimentCard: () => <p>sentiment</p> }));
vi.mock("../components/VolumeChart", () => ({ VolumeChart: () => <p>volume</p> }));
vi.mock("../components/Themes", () => ({ Themes: () => <p>themes</p> }));
vi.mock("../components/EmptyState", () => ({
  EmptyState: ({ onScan }: Readonly<{ onScan: () => void }>) => (
    <button type="button" onClick={onScan}>
      empty state
    </button>
  ),
}));
vi.mock("../components/SourcesCard", () => ({
  SourcesCard: ({ notice }: Readonly<{ notice: { ok: boolean; text: string } | null }>) => <p>sources card{notice ? `: ${notice.text}` : ""}</p>,
}));
vi.mock("../components/ProjectsPanel", () => ({
  ProjectsPanel: ({ onSelect }: Readonly<{ onSelect: (id: number | null) => void }>) => (
    <button type="button" onClick={() => onSelect(2)}>
      projects panel
    </button>
  ),
}));
vi.mock("../components/ProxySettingsForm", () => ({ ProxySettingsForm: () => <p>proxy settings</p> }));

const SUMMARY: SummaryResponse = {
  summary: { total: 4, by_sentiment: {}, by_source: {}, by_day: {} },
  timeseries: [],
  net: 0,
  themes: [],
};

beforeEach(() => {
  vi.spyOn(api, "meta").mockResolvedValue({
    version: "v1.2.3",
    sources: [{ name: "x", label: "X", glyph: "X", color: "#000", needs_config: true, configured: false }],
    default_sources: [],
  });
  vi.spyOn(api, "projects").mockResolvedValue([project({ id: 2, name: "Radaro" })]);
  vi.spyOn(api, "project").mockResolvedValue(project({ id: 2, name: "Radaro" }));
  vi.spyOn(api, "queries").mockResolvedValue(["radaro"]);
  vi.spyOn(api, "tracking").mockRejectedValue(new ApiError(404, "not tracked"));
  vi.spyOn(api, "summary").mockRejectedValue(new ApiError(500, "no summary"));
  vi.spyOn(api, "sourceSettings").mockResolvedValue([]);
  vi.spyOn(api, "accounts").mockResolvedValue({ platforms: [], accounts: [] });
});

function open(search: string) {
  window.history.replaceState(null, "", `/${search}`);
  const onSignOut = vi.fn();
  render(<App user={USER} onSignOut={onSignOut} />);
  return { user: userEvent.setup(), onSignOut };
}

const nav = (section: string) => within(screen.getByRole("navigation", { name: "Sections" })).getByRole("button", { name: new RegExp(`^${section}`) });

const view = () => screen.getByRole("region", { name: /Listen|Scan|Drafts|Accounts|Setup/ });

describe("App routing", () => {
  it("shows server proxy settings only to the admin in Setup", async () => {
    open("?v=setup");
    expect(screen.queryByText("proxy settings")).toBeNull();
    window.history.replaceState(null, "", "/?v=setup");
    render(<App user={{ ...USER, is_admin: true }} onSignOut={vi.fn()} />);
    expect(await screen.findByText("proxy settings")).toBeTruthy();
  });

  it.each([
    ["?v=drafts", "Drafts", "drafts view"],
    ["?v=accounts", "Accounts", "accounts view"],
    ["?v=scan", "Scan", "scan view"],
    ["?v=setup", "Setup", "sources card"],
    ["", "Listen", "mentions feed"],
    ["?v=bogus", "Listen", "mentions feed"],
  ])("opens %s as %s", async (search, name, content) => {
    open(search);
    expect(view().getAttribute("aria-label")).toBe(name);
    expect(await screen.findByText(content)).toBeTruthy();
  });

  it("marks the current section in the rail", () => {
    open("?v=drafts");
    const drafts = nav("Drafts");
    expect(drafts.getAttribute("aria-current")).toBe("page");
    expect(drafts.className).toContain("is-on");
    expect(nav("Accounts").getAttribute("aria-current")).toBeNull();
  });

  it("navigates between Drafts and Accounts and keeps the URL in step", async () => {
    const { user } = open("");
    await user.click(nav("Drafts"));
    expect(screen.getByText("drafts view")).toBeTruthy();
    expect(window.location.search).toBe("?v=drafts");

    await user.click(nav("Accounts"));
    expect(screen.getByText("accounts view")).toBeTruthy();
    expect(window.location.search).toBe("?v=accounts");

    await user.click(screen.getByRole("button", { name: "accounts view" }));
    expect(view().getAttribute("aria-label")).toBe("Setup");
    expect(window.location.search).toBe("?v=setup");

    await user.click(nav("Listen"));
    expect(window.location.search).toBe("");
  });

  it("follows the browser's back button", async () => {
    open("?v=accounts");
    window.history.replaceState(null, "", "/?v=drafts");
    act(() => {
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(await screen.findByText("drafts view")).toBeTruthy();
  });
});

describe("App views", () => {
  it("shows the summary error with a retry", async () => {
    open("");
    expect(await screen.findByText("no summary")).toBeTruthy();
    expect(screen.getByText("All projects / Every keyword")).toBeTruthy();
  });

  it("reports on a keyword inside a project", async () => {
    vi.spyOn(api, "summary").mockResolvedValue(SUMMARY);
    open("?p=2&q=radaro");
    expect(await screen.findByText("report on radaro")).toBeTruthy();
    expect(screen.getByText("sentiment")).toBeTruthy();
    expect(await screen.findByText("Radaro / radaro")).toBeTruthy();
    expect(api.tracking).toHaveBeenCalledWith("radaro", expect.anything());
  });

  it("names a whole project", async () => {
    vi.spyOn(api, "summary").mockResolvedValue(SUMMARY);
    open("?p=2");
    expect(await screen.findByText("Radaro / Whole project")).toBeTruthy();
  });

  it("shows the empty state until there are keywords, which leads to Scan", async () => {
    vi.spyOn(api, "queries").mockResolvedValue([]);
    vi.spyOn(api, "summary").mockResolvedValue({ ...SUMMARY, summary: { ...SUMMARY.summary, total: 0 } });
    const { user } = open("");
    await user.click(await screen.findByRole("button", { name: "empty state" }));
    expect(view().getAttribute("aria-label")).toBe("Scan");
  });

  it("opens the scanned keyword after a scan and moves between views from Scan", async () => {
    const { user } = open("?v=scan");
    await user.click(await screen.findByRole("button", { name: "scanned" }));
    expect(window.location.search).toBe("?q=golang&v=scan");
    await user.click(screen.getByRole("button", { name: "to setup" }));
    expect(view().getAttribute("aria-label")).toBe("Setup");
    await user.click(nav("Scan"));
    await user.click(screen.getByRole("button", { name: "to listen" }));
    expect(view().getAttribute("aria-label")).toBe("Listen");
  });

  it("counts sources that need setup and picks a project from Setup", async () => {
    const { user } = open("?v=setup");
    expect(await screen.findByText("0 of 1 sources are ready — 1 need setup.")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "projects panel" }));
    expect(window.location.search).toBe("?p=2&v=setup");
  });

  it("passes the Reddit sign-in result to Setup once and cleans the URL", async () => {
    open("?v=setup&connected=reddit");
    expect(await screen.findByText("sources card: Reddit account connected.")).toBeTruthy();
    expect(window.location.search).toBe("?v=setup");
  });

  it("passes a failed sign-in through", async () => {
    open("?v=setup&connect_error=denied");
    expect(await screen.findByText("sources card: denied")).toBeTruthy();
  });

  it("signs out from the rail", async () => {
    const { user, onSignOut } = open("");
    await user.click(screen.getAllByRole("button", { name: "Sign out" })[0]);
    expect(onSignOut).toHaveBeenCalled();
  });
});
