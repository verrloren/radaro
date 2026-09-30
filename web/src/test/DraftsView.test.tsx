import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError, draftsApi } from "../api";
import { DraftsView } from "../components/DraftsView";
import type { Draft, DraftCounts, DraftDetail } from "../types";
import { account, accountView, draft, PLATFORMS } from "./fixtures";

const DRAFTS: DraftDetail[] = [draft({ id: 7, title: "First draft" }), draft({ id: 8, platform: "bluesky", title: null, community: null, body: "Second draft" })];

interface ListRes {
  drafts: Draft[];
  counts: DraftCounts;
}

const LIST: ListRes = {
  drafts: DRAFTS,
  counts: { draft: 2, approved: 1, published: 3 },
};

/** GET /api/drafts and GET /api/drafts/counts; a pending promise holds both. */
function mockList(res: ListRes | Promise<ListRes>) {
  vi.spyOn(draftsApi, "counts").mockImplementation(() => Promise.resolve(res).then((r) => r.counts));
  return vi.spyOn(draftsApi, "list").mockImplementation(() => Promise.resolve(res).then((r) => r.drafts));
}

beforeEach(() => {
  vi.spyOn(api, "accounts").mockResolvedValue({ platforms: PLATFORMS, accounts: [accountView()] });
  vi.spyOn(draftsApi, "get").mockImplementation(async (id) => DRAFTS.find((d) => d.id === id) ?? draft({ id, status: "approved", plan: null }));
  vi.spyOn(draftsApi, "warnings").mockResolvedValue([]);
});

describe("DraftsView", () => {
  it("shows loading until the drafts arrive", () => {
    mockList(new Promise(() => {}));
    render(<DraftsView rev={0} onChanged={vi.fn()} />);
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Drafts");
    expect(screen.getByText(/Loading drafts/)).toBeTruthy();
  });

  it("shows an error with retry", async () => {
    vi.spyOn(draftsApi, "list").mockRejectedValue(new ApiError(500, "database is locked"));
    vi.spyOn(draftsApi, "counts").mockResolvedValue({});
    const onChanged = vi.fn();
    render(<DraftsView rev={0} onChanged={onChanged} />);
    expect(await screen.findByText("database is locked")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(onChanged).toHaveBeenCalled();
  });

  it("explains how drafts get here when there are none", async () => {
    mockList({ drafts: [], counts: {} });
    render(<DraftsView rev={0} onChanged={vi.fn()} />);
    expect(await screen.findByText("No drafts yet.")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "no drafts to review" })).toBeTruthy();
    expect(screen.queryByRole("tablist")).toBeNull();
  });

  it("counts drafts per tab and opens the first one", async () => {
    mockList(LIST);
    render(<DraftsView rev={0} onChanged={vi.fn()} />);
    expect(await screen.findByText("2 drafts wait for your review, 1 approved to publish.")).toBeTruthy();
    const tabs = screen.getAllByRole("tab");
    expect(tabs.map((t) => t.textContent)).toEqual(["To review2", "Approved1", "Published3", "Failed0", "Skipped0", "All6"]);
    expect(tabs[0].getAttribute("aria-selected")).toBe("true");
    await waitFor(() => expect(draftsApi.get).toHaveBeenCalledWith(7, expect.anything()));
    expect(screen.getByRole("button", { name: /First draft/ }).getAttribute("aria-current")).toBe("true");
  });

  it("filters by status and selects another draft", async () => {
    const list = mockList(LIST);
    const user = userEvent.setup();
    render(<DraftsView rev={0} onChanged={vi.fn()} />);
    await user.click(await screen.findByRole("button", { name: /Second draft/ }));
    await waitFor(() => expect(draftsApi.get).toHaveBeenCalledWith(8, expect.anything()));

    await user.click(screen.getByRole("tab", { name: /Approved/ }));
    expect(list).toHaveBeenLastCalledWith("approved", expect.anything());
    expect(screen.getByRole("tab", { name: /Approved/ }).getAttribute("aria-selected")).toBe("true");
    await user.click(screen.getByRole("tab", { name: /All/ }));
    expect(list).toHaveBeenLastCalledWith(undefined, expect.anything());
  });

  it("says when the current tab is empty", async () => {
    mockList({ drafts: [], counts: { published: 2 } });
    render(<DraftsView rev={0} onChanged={vi.fn()} />);
    expect(await screen.findByText("Nothing waits for review.")).toBeTruthy();
    expect(screen.getByText("No drafts here. Pick another tab.")).toBeTruthy();
  });

  it.each([
    [{ draft: 1 }, "1 draft waits for your review."],
    [{ approved: 1 }, "1 approved draft is ready to publish."],
    [{ approved: 3 }, "3 approved drafts are ready to publish."],
  ])("headline for %o", async (counts, text) => {
    mockList({ drafts: [], counts });
    render(<DraftsView rev={0} onChanged={vi.fn()} />);
    expect(await screen.findByText(text)).toBeTruthy();
  });

  it("keeps the link to a published draft in view until dismissed", async () => {
    const approved = draft({ id: 9, status: "approved", plan: null, body: "Ready" });
    mockList({ drafts: [approved], counts: { approved: 1 } });
    vi.spyOn(draftsApi, "get").mockResolvedValue(approved);
    vi.spyOn(draftsApi, "publish").mockResolvedValue({ draft: { ...approved, remote_url: "https://reddit.com/p/9" }, account: account() });
    const user = userEvent.setup();
    render(<DraftsView rev={0} onChanged={vi.fn()} />);
    await user.click(await screen.findByRole("button", { name: "Publish…" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Publish now" }));

    const notice = await screen.findByText(/Published draft 9 as u\/alice./);
    expect(notice.tagName).toBe("OUTPUT");
    expect(within(notice).getByRole("link", { name: /View it/ }).getAttribute("href")).toBe("https://reddit.com/p/9");
    await user.click(within(notice).getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText(/Published draft 9/)).toBeNull();
  });

  it("clears the notice when the tab changes", async () => {
    const approved = draft({ id: 9, status: "approved", plan: null, platform: "bluesky", title: null, community: null });
    mockList({ drafts: [approved], counts: { approved: 1 } });
    vi.spyOn(draftsApi, "get").mockResolvedValue(approved);
    vi.spyOn(draftsApi, "publish").mockResolvedValue({ draft: approved, account: account({ handle: "alice.bsky.social" }) });
    const user = userEvent.setup();
    render(<DraftsView rev={0} onChanged={vi.fn()} />);
    await user.click(await screen.findByRole("button", { name: "Publish…" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Publish now" }));
    const notice = await screen.findByText(/Published draft 9 as alice.bsky.social./);
    expect(within(notice).queryByRole("link")).toBeNull();
    await user.click(screen.getByRole("tab", { name: /Published/ }));
    expect(screen.queryByText(/Published draft 9/)).toBeNull();
  });
});
