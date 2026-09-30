import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { accountsApi, api, ApiError } from "../api";
import { AccountsTable, platformSource } from "../components/AccountsTable";
import type { AccountView } from "../types";
import { accountView, PLATFORMS } from "./fixtures";

const inTwoHours = () => new Date(Date.now() + 2 * 3600_000 + 60_000).toISOString();

function setup(accounts: AccountView[]) {
  const onChanged = vi.fn();
  render(<AccountsTable accounts={accounts} platforms={PLATFORMS} onChanged={onChanged} />);
  return { onChanged, user: userEvent.setup() };
}

const row = (handle: string) => screen.getByRole("rowheader", { name: new RegExp(handle) }).closest("tbody") as HTMLElement;

describe("platformSource", () => {
  it("gives Dev.to its own glyph and others their initial", () => {
    expect(platformSource("devto", "Dev.to")).toMatchObject({ glyph: "D", color: "#3b49df" });
    expect(platformSource("bluesky", "Bluesky")).toMatchObject({ name: "bluesky", glyph: "B" });
  });
});

describe("AccountsTable", () => {
  beforeEach(() => {
    vi.spyOn(accountsApi, "check").mockResolvedValue(accountView());
    vi.spyOn(accountsApi, "update").mockResolvedValue(accountView());
    vi.spyOn(api, "deleteAccount").mockResolvedValue({ deleted: true });
  });

  it("shows status, activity, quota and failure shares per account", () => {
    setup([
      accountView({ id: 1, handle: "u/alice" }),
      accountView({
        id: 2,
        handle: "u/bob",
        status: "limited",
        limited_until: "2026-10-01T10:00:00Z",
        paused: true,
        checked_at: null,
        quota: { daily: 5, used_24h: 5, remaining: 0, next_at: inTwoHours(), reason: "daily limit", ready: false },
        activity: { ...accountView().activity, published_30d: 0, removed_30d: 0, failed_30d: 0, last_error: "429 Too Many Requests" },
      }),
      accountView({ id: 3, handle: "u/carol", status: "suspended", quota: undefined, status_detail: "Account suspended by Reddit" }),
    ]);
    const alice = row("u/alice");
    expect(within(alice).getByText("live")).toBeTruthy();
    expect(alice.textContent).toContain("1 / 3");
    expect(alice.textContent).toContain("4 / 5 left");
    expect(alice.textContent).toContain("10%");
    expect(alice.textContent).toContain("0%");

    const bob = row("u/bob");
    expect(within(bob).getByText("rate-limited")).toBeTruthy();
    expect(within(bob).getByText("paused")).toBeTruthy();
    expect(within(bob).getByText("never checked")).toBeTruthy();
    expect(within(bob).getByText(/: The platform is rate-limiting this account. Until/)).toBeTruthy();
    expect(within(bob).getByText("0").className).toBe("neg");
    expect(within(bob).getByText(/^next in 2h$/)).toBeTruthy();
    expect(within(bob).getAllByText("—")).toHaveLength(2);
    expect(within(bob).getByText("429 Too Many Requests")).toBeTruthy();
    expect(within(bob).getByRole("button", { name: "Resume" })).toBeTruthy();

    const carol = row("u/carol");
    expect(carol.className).toBe("is-dead");
    expect(within(carol).getByText(/Account suspended by Reddit/)).toBeTruthy();
    expect(within(carol).getByText("—")).toBeTruthy();
  });

  it("hides a next time that has passed", () => {
    setup([accountView({ quota: { daily: 5, used_24h: 1, remaining: 4, next_at: "2020-01-01T00:00:00Z", ready: true } })]);
    expect(screen.queryByText(/^next /)).toBeNull();
    expect(screen.queryByText("on hold")).toBeNull();
  });

  it("puts an account no time will unblock on hold", () => {
    setup([accountView({ paused: true, quota: { daily: 5, used_24h: 0, remaining: 5, next_at: null, reason: "account is paused", ready: false } })]);
    const hold = screen.getByText("on hold");
    expect(hold.closest("[title]")?.getAttribute("title")).toBe("account is paused");
  });

  it("checks one account now", async () => {
    let done: (v: AccountView) => void = () => {};
    vi.spyOn(accountsApi, "check").mockReturnValue(new Promise((res) => (done = res)));
    const { user, onChanged } = setup([accountView({ id: 4, handle: "u/dave" })]);
    await user.click(screen.getByRole("button", { name: "Check now" }));
    expect(accountsApi.check).toHaveBeenCalledWith(4);
    expect((screen.getByRole("button", { name: "Checking…" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Pause" }) as HTMLButtonElement).disabled).toBe(true);
    done(accountView());
    expect(await screen.findByRole("button", { name: "Check now" })).toBeTruthy();
    expect(onChanged).toHaveBeenCalled();
  });

  it("pauses and resumes", async () => {
    const { user } = setup([accountView({ id: 1, handle: "u/alice" }), accountView({ id: 2, handle: "u/bob", paused: true })]);
    await user.click(within(row("u/alice")).getByRole("button", { name: "Pause" }));
    expect(accountsApi.update).toHaveBeenCalledWith(1, { paused: true });
    await user.click(within(row("u/bob")).getByRole("button", { name: "Resume" }));
    expect(accountsApi.update).toHaveBeenCalledWith(2, { paused: false });
  });

  it("shows why an action failed", async () => {
    vi.spyOn(accountsApi, "update").mockRejectedValue(new ApiError(404, "account not found"));
    const { user, onChanged } = setup([accountView()]);
    await user.click(screen.getByRole("button", { name: "Pause" }));
    expect(await screen.findByText("account not found")).toBeTruthy();
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("disconnects only after confirmation", async () => {
    const { user, onChanged } = setup([accountView({ id: 5, handle: "u/eve" })]);
    await user.click(screen.getByRole("button", { name: "Disconnect" }));
    await user.click(screen.getByRole("button", { name: "Keep" }));
    expect(api.deleteAccount).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Check now" })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Disconnect" }));
    await user.click(screen.getByRole("button", { name: "Disconnect u/eve?" }));
    expect(api.deleteAccount).toHaveBeenCalledWith(5);
    expect(onChanged).toHaveBeenCalled();
    expect(await screen.findByRole("button", { name: "Check now" })).toBeTruthy();
  });

  it("edits limits in a modal", async () => {
    const { user, onChanged } = setup([accountView({ id: 6, handle: "u/frank", daily_limit: 3 })]);
    await user.click(screen.getByRole("button", { name: "Limits" }));
    const dialog = screen.getByRole("dialog", { name: "Limits · u/frank" });
    expect((within(dialog).getByLabelText("Publications per 24 hours") as HTMLInputElement).value).toBe("3");
    await user.click(within(dialog).getByRole("button", { name: "Save limits" }));
    expect(accountsApi.update).toHaveBeenCalledWith(6, { daily_limit: 3, min_interval_sec: 0, community_cooldown_h: 0 });
    expect(onChanged).toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("closes the limits modal without saving", async () => {
    const { user } = setup([accountView()]);
    await user.click(screen.getByRole("button", { name: "Limits" }));
    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(accountsApi.update).not.toHaveBeenCalled();
  });
});
