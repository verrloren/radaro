import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, draftsApi } from "../api";
import { DraftsEditor } from "../components/DraftsEditor";
import type { AccountsResponse, DraftDetail, DraftWarning } from "../types";
import { account, accountView, draft, PLATFORMS } from "./fixtures";
import type { SelectOption } from "../components/ui/Select";

// Radix Select needs layout APIs jsdom lacks; a native select exercises the same props.
vi.mock("../components/ui/Select", () => ({
  Select: (p: Readonly<{ id?: string; label: string; value: string; options: SelectOption[]; disabled?: boolean; onChange: (v: string) => void }>) => (
    <select id={p.id} aria-label={p.label} value={p.value} disabled={p.disabled} onChange={(e) => p.onChange(e.target.value)}>
      {p.options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  ),
}));

const ACCOUNTS: AccountsResponse = {
  platforms: PLATFORMS,
  accounts: [
    accountView({ id: 1, handle: "u/alice" }),
    accountView({ id: 2, handle: "u/bob", paused: true }),
    accountView({ id: 3, handle: "u/carol", status: "limited", limited_until: "2026-10-01T10:00:00Z" }),
    accountView({ id: 4, handle: "u/dave", status: "unknown" }),
    accountView({ id: 5, platform: "bluesky", handle: "alice.bsky.social" }),
  ],
};

const livePlan = { account: accountView({ id: 1, handle: "u/alice" }), quota: { daily: 5, used_24h: 2, remaining: 3, next_at: null, ready: true }, next_at: null };

function setup(d: DraftDetail, warnings: DraftWarning[] = [], accounts: AccountsResponse | undefined = ACCOUNTS) {
  vi.spyOn(draftsApi, "get").mockResolvedValue(d);
  const warn = vi.spyOn(draftsApi, "warnings").mockResolvedValue(warnings);
  const onChanged = vi.fn();
  const onNotice = vi.fn();
  const user = userEvent.setup();
  render(<DraftsEditor id={d.id} rev={0} accounts={accounts} onChanged={onChanged} onNotice={onNotice} />);
  return { user, onChanged, onNotice, warn };
}

const body = () => screen.getByLabelText(/^Text/) as HTMLTextAreaElement;

describe("DraftsEditor", () => {
  beforeEach(() => {
    vi.spyOn(draftsApi, "edit").mockResolvedValue(draft());
  });

  it("shows loading, then an error with retry", async () => {
    let fail: (e: unknown) => void = () => {};
    vi.spyOn(draftsApi, "get").mockReturnValue(new Promise((_, rej) => (fail = rej)));
    const onChanged = vi.fn();
    render(<DraftsEditor id={1} rev={0} accounts={undefined} onChanged={onChanged} onNotice={vi.fn()} />);
    expect(screen.getByText(/Loading draft/)).toBeTruthy();
    fail(new ApiError(404, "draft not found"));
    expect(await screen.findByText("draft not found")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(onChanged).toHaveBeenCalled();
  });

  it("renders the head, reply target and the promoted keyword", async () => {
    setup(draft({ kind: "reply", reply_to: "https://reddit.com/r/golang/1", query: "radaro" }));
    expect((await screen.findByRole("heading", { level: 2 })).textContent).toBe("Reddit reply · #7");
    expect(screen.getByText("reply in r/golang", { exact: false })).toBeTruthy();
    expect(screen.getByRole("link", { name: /reddit.com\/r\/golang\/1/ }).getAttribute("target")).toBe("_blank");
    expect(screen.getByText("radaro")).toBeTruthy();
  });

  it("counts title and body characters against the platform limits", async () => {
    const { user } = setup(draft({ title: "Short", body: "Hello there" }));
    const title = await screen.findByLabelText(/^Title/);
    expect(screen.getByText("5/300")).toBeTruthy();
    expect(screen.getByText("11/40,000")).toBeTruthy();
    await user.clear(title);
    await user.type(title, "x".repeat(301));
    const count = screen.getByText("301/300");
    expect(count.className).toContain("is-over");
    expect(screen.getByLabelText("Subreddit")).toBeTruthy();
  });

  it("flags a body over the limit and counts without a limit", async () => {
    const { user } = setup(draft({ platform: "bluesky", community: null, title: null, body: "hi 👋" }));
    await screen.findByText("4/300");
    expect(screen.queryByLabelText(/^Title/)).toBeNull();
    expect(screen.queryByLabelText("Subreddit")).toBeNull();
    await user.type(body(), "y".repeat(300));
    expect(screen.getByText("304/300").className).toContain("is-over");
  });

  it("shows Dev.to tags and a plain character count", async () => {
    setup(draft({ platform: "devto", community: "go, cli", title: "Post" }), [], { platforms: PLATFORMS.map((p) => ({ ...p, max_chars: 0 })), accounts: [] });
    expect(await screen.findByText("11 characters")).toBeTruthy();
    expect((screen.getByLabelText("Tags (comma-separated)") as HTMLInputElement).value).toBe("go, cli");
    expect(screen.getByText(/No Dev.to account is connected yet/)).toBeTruthy();
  });

  it("saves edits, and discards them", async () => {
    const { user, onChanged } = setup(draft());
    await user.type(await screen.findByLabelText(/^Title/), "!");
    await user.clear(screen.getByLabelText("Subreddit"));
    await user.type(screen.getByLabelText("Subreddit"), "rust");
    await user.type(body(), " again");
    expect(screen.getByText("Saving sends it back to review.")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(draftsApi.edit).toHaveBeenCalledWith(7, { title: "A tool for listening!", community: "rust", body: "Hello there again" });
    await waitFor(() => expect(onChanged).toHaveBeenCalled());

    await user.type(body(), "?");
    await user.click(screen.getByRole("button", { name: "Discard" }));
    expect(body().value).toBe("Hello there");
    expect(screen.queryByRole("button", { name: "Save changes" })).toBeNull();
  });

  it("lists the platform's accounts with Auto and sends the choice", async () => {
    const { user } = setup(draft({ account_id: 2 }));
    const select = (await screen.findByRole("combobox", { name: "Account" })) as HTMLSelectElement;
    const labels = [...select.options].map((o) => o.textContent);
    expect(labels[0]).toBe("Auto — the best account when publishing");
    expect(labels).toContain("u/alice · live");
    expect(labels).toContain("u/bob · paused");
    expect(labels.some((l) => l?.startsWith("u/carol · limited until"))).toBe(true);
    expect(labels).toContain("u/dave · not checked yet");
    expect(labels.some((l) => l?.includes("bsky"))).toBe(false);
    expect(select.value).toBe("2");

    await user.selectOptions(select, "");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(draftsApi.edit).toHaveBeenLastCalledWith(7, { account_id: null });

    await user.selectOptions(select, "3");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    expect(draftsApi.edit).toHaveBeenLastCalledWith(7, { account_id: 3 });
  });

  it("explains who publishes and when", async () => {
    setup(draft({ plan: livePlan }));
    const plan = await screen.findByText(/used in the last 24 h/);
    expect(plan.closest("output")).toBeTruthy();
    expect(plan.textContent).toContain("Auto picks u/alice (live) · 2 of 5 used in the last 24 h");
    expect(screen.getByText("can publish now")).toBeTruthy();
    expect(plan.closest("output")?.querySelector(".dot")?.className).toContain("tone-ok");
  });

  it("shows when a limited plan may publish, and why", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 8, 30, 9, 0));
    const later = new Date(2026, 8, 30, 15, 30).toISOString();
    setup(draft({ account_id: 1, plan: { ...livePlan, next_at: later, reason: "daily limit reached" } }));
    expect(await screen.findByText(/^can publish at /)).toBeTruthy();
    expect(screen.getByText(/Publishes as/)).toBeTruthy();
    expect(screen.getByText(/daily limit reached/)).toBeTruthy();
    expect(document.querySelector(".draft-plan .dot")?.className).toContain("tone-setup");
  });

  it("dates a plan on another day and says when nothing can publish", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 8, 30, 9, 0));
    setup(draft({ plan: { account: null, quota: null, next_at: new Date(2026, 9, 2, 8, 0).toISOString(), reason: "no account" } }));
    expect(await screen.findByText(/^can publish on /)).toBeTruthy();
    expect(screen.getByText(/No account can publish it right now/)).toBeTruthy();
  });

  it("marks a plan with no account and no time as blocked", async () => {
    setup(draft({ plan: { account: null, quota: null, next_at: null, reason: "all accounts dead" } }));
    await screen.findByText(/No account can publish it right now/);
    expect(document.querySelector(".draft-plan .dot")?.className).toContain("tone-error");
  });

  it("does not claim a paused account can publish now", async () => {
    setup(draft({ account_id: 2, plan: { account: accountView({ id: 2, handle: "u/bob", paused: true }), quota: { daily: 5, used_24h: 0, remaining: 5, next_at: null, ready: false, reason: "account is paused" }, next_at: null, reason: "account is paused" } }));
    expect(await screen.findByText("needs attention before publishing")).toBeTruthy();
    expect(screen.queryByText("can publish now")).toBeNull();
  });

  it("hides the plan while the draft has unsaved edits", async () => {
    const { user } = setup(draft({ plan: livePlan }));
    await screen.findByText("can publish now");
    await user.type(body(), "!");
    expect(screen.queryByText("can publish now")).toBeNull();
  });

  it("approves a draft, and skips it", async () => {
    const approve = vi.spyOn(draftsApi, "approve").mockResolvedValue(draft({ status: "approved" }));
    const skip = vi.spyOn(draftsApi, "skip").mockResolvedValue(draft({ status: "skipped" }));
    const { user, onChanged } = setup(draft());
    const publish = await screen.findByRole("button", { name: "Publish…" });
    expect((publish as HTMLButtonElement).disabled).toBe(true);
    expect(publish.getAttribute("title")).toBe("Approve the draft first");
    await user.click(screen.getByRole("button", { name: "Approve" }));
    expect(approve).toHaveBeenCalledWith(7);
    await user.click(screen.getByRole("button", { name: "Skip" }));
    expect(skip).toHaveBeenCalledWith(7);
    expect(onChanged).toHaveBeenCalledTimes(2);
  });

  it("retries a failed draft and shows why it was refused", async () => {
    const approve = vi.spyOn(draftsApi, "approve").mockRejectedValue(new ApiError(409, "already publishing"));
    const first = setup(draft({ status: "failed", error: "timeout" }));
    expect(await screen.findByText("Last attempt failed: timeout")).toBeTruthy();
    await first.user.click(screen.getByRole("button", { name: "Approve retry" }));
    expect(approve).toHaveBeenCalled();
    expect(await screen.findByText("already publishing")).toBeTruthy();
    expect(first.onChanged).toHaveBeenCalled();
  });

  it("restores a skipped draft to review", async () => {
    const { user, warn } = setup(draft({ status: "skipped" }));
    await user.click(await screen.findByRole("button", { name: "Restore to review" }));
    expect(draftsApi.edit).toHaveBeenCalledWith(7, { body: "Hello there" });
    expect(screen.queryByRole("button", { name: "Skip" })).toBeNull();
    expect(warn).not.toHaveBeenCalled();
  });

  it("confirms before publishing and reports the result", async () => {
    const publish = vi.spyOn(draftsApi, "publish").mockResolvedValue({
      draft: draft({ status: "published", remote_url: "https://reddit.com/x" }),
      account: account({ handle: "u/alice" }),
    });
    const { user, onNotice, onChanged } = setup(draft({ status: "approved", plan: livePlan }));
    await user.click(await screen.findByRole("button", { name: "Publish…" }));
    const dialog = screen.getByRole("dialog", { name: "Publish this draft?" });
    expect(dialog.textContent).toContain("This post goes out publicly on Reddit in r/golang as u/alice. Radaro cannot remove it from the platform after publishing.");
    await user.click(within(dialog).getByRole("button", { name: "Publish now" }));
    expect(publish).toHaveBeenCalledWith(7);
    expect(onNotice).toHaveBeenCalledWith({ text: "Published draft 7 as u/alice.", url: "https://reddit.com/x" });
    expect(onChanged).toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("keeps the dialog open with the reason when a limit refuses the publish", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 8, 30, 9, 0));
    vi.spyOn(draftsApi, "publish").mockRejectedValue(new ApiError(409, "u/alice may publish again at 15:30"));
    const plan = { ...livePlan, next_at: new Date(2026, 8, 30, 15, 30).toISOString() };
    const rules = [{ code: "community_bans_promo", text: "No self-promotion" }];
    const { user, onNotice } = setup(draft({ status: "approved", account_id: 1, plan }), rules);
    await screen.findByText("No self-promotion");
    await user.click(screen.getByRole("button", { name: "Publish…" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText(/The limits say it can go out at .*; publishing earlier will be refused./)).toBeTruthy();
    expect(within(dialog).getByText(/This subreddit restricts self-promotion/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "Publish now" }));
    expect(await within(dialog).findByText("u/alice may publish again at 15:30")).toBeTruthy();
    expect(onNotice).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("says when a refused publish can go out, from the 409's next_at", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 8, 30, 9, 0));
    const next = new Date(2026, 8, 30, 15, 30).toISOString();
    vi.spyOn(draftsApi, "publish").mockRejectedValue(new ApiError(409, "daily limit reached", { error: "daily limit reached", next_at: next }));
    const { user } = setup(draft({ status: "approved", account_id: 1, plan: livePlan }));
    await user.click(await screen.findByRole("button", { name: "Publish…" }));
    const dialog = screen.getByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Publish now" }));
    expect(await within(dialog).findByText(/daily limit reached It can go out at .+\./)).toBeTruthy();
  });

  it("describes a reply published from an account picked at publish time", async () => {
    const { user } = setup(draft({ platform: "bluesky", kind: "reply", community: null, title: null, status: "approved" }));
    await user.click(await screen.findByRole("button", { name: "Publish…" }));
    const dialog = screen.getByRole("dialog");
    expect(dialog.textContent).toContain("This reply goes out publicly on Bluesky from the account picked when publishing");
    await user.click(within(dialog).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("splits advice from subreddit rules", async () => {
    setup(draft(), [
      { code: "self_promo", text: "Mentions your project twice" },
      { code: "duplicate", text: "Similar to draft 3" },
      { code: "subreddit_rules", text: "Be civil" },
    ]);
    const advice = await screen.findByRole("list", { name: "Advice before publishing" });
    expect(within(advice).getAllByRole("listitem")).toHaveLength(2);
    const rules = screen.getByText(/Subreddit rules \(1\)/).closest("details") as HTMLDetailsElement;
    expect(rules.open).toBe(false);
    expect(within(rules).getByText("Be civil")).toBeTruthy();
    expect(screen.queryByText("restricts self-promotion")).toBeNull();
  });

  it("opens the rules when the subreddit bans self-promotion", async () => {
    setup(draft(), [{ code: "community_bans_promo", text: "No self-promotion" }]);
    const rules = (await screen.findByText(/Subreddit rules \(1\)/)).closest("details") as HTMLDetailsElement;
    expect(rules.open).toBe(true);
    expect(screen.getByText("restricts self-promotion")).toBeTruthy();
  });

  it("says when there is no advice", async () => {
    setup(draft());
    expect(await screen.findByText("No advice for this draft.")).toBeTruthy();
  });

  it("shows a failed advice check and the loading state", async () => {
    vi.spyOn(draftsApi, "get").mockResolvedValue(draft());
    let fail: (e: unknown) => void = () => {};
    vi.spyOn(draftsApi, "warnings").mockReturnValue(new Promise((_, rej) => (fail = rej)));
    render(<DraftsEditor id={7} rev={0} accounts={ACCOUNTS} onChanged={vi.fn()} onNotice={vi.fn()} />);
    expect(await screen.findByText(/Checking the draft/)).toBeTruthy();
    fail(new ApiError(502, "reddit is down"));
    expect(await screen.findByText("Could not check the draft: reddit is down")).toBeTruthy();
  });

  it("shows a published draft read-only with its metrics", async () => {
    const { warn } = setup(
      draft({
        status: "published",
        published_at: "2026-09-01T10:00:00Z",
        remote_url: "https://reddit.com/r/golang/2",
        removed_at: "2026-09-02T10:00:00Z",
        metrics: { score: 1200, comments: 4, removed: true, flair: "tool" },
        metrics_at: "2026-09-03T10:00:00Z",
      }),
    );
    expect(await screen.findByRole("link", { name: /View on Reddit/ })).toBeTruthy();
    expect(screen.getByText(/Removed by the platform or its moderators/).tagName).toBe("OUTPUT");
    expect(screen.getByText("1,200")).toBeTruthy();
    expect(screen.getByText("tool")).toBeTruthy();
    expect(screen.queryByText("removed", { selector: ".tag" })).toBeNull();
    expect(screen.getByText(/updated/)).toBeTruthy();
    expect(body().disabled).toBe(true);
    expect(screen.queryByRole("button", { name: "Publish…" })).toBeNull();
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(warn).not.toHaveBeenCalled();
  });

  it("shows a published draft without metrics", async () => {
    setup(draft({ status: "published" }));
    expect(await screen.findByText("No metrics yet.")).toBeTruthy();
  });

  it("warns about an interrupted publish", async () => {
    setup(draft({ status: "publishing" }), [], undefined);
    expect(await screen.findByText(/Being published/)).toBeTruthy();
  });
});
