import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { accountsApi, api, ApiError } from "../api";
import { AccountsView } from "../components/AccountsView";
import type { AccountsResponse, AccountStats, AccountTally, AccountView } from "../types";
import { accountView, PLATFORMS } from "./fixtures";

function tally(over: Partial<AccountTally>): AccountTally {
  return { total: 0, live: 0, dead: 0, limited: 0, paused: 0, unknown: 0, ban_rate: 0, ...over };
}

const STATS: AccountStats = {
  days: 30,
  series: [
    { day: "2026-09-28", platform: "reddit", total: 2, dead: 0 },
    { day: "2026-09-29", platform: "reddit", total: 2, dead: 1 },
  ],
  platforms: [
    tally({ platform: "reddit", total: 2, live: 1, dead: 1, ban_rate: 0.5 }),
    tally({ platform: "bluesky", total: 1, live: 1, paused: 1, unknown: 1 }),
  ],
  total: tally({ total: 3, live: 2, dead: 1, ban_rate: 1 / 3 }),
};

function list(accounts: AccountView[]): AccountsResponse {
  return { platforms: PLATFORMS, accounts };
}

function setup(accounts: AccountView[], stats: AccountStats | Error = STATS) {
  vi.spyOn(api, "accounts").mockResolvedValue(list(accounts));
  if (stats instanceof Error) vi.spyOn(accountsApi, "stats").mockRejectedValue(stats);
  else vi.spyOn(accountsApi, "stats").mockResolvedValue(stats);
  const onChanged = vi.fn();
  const onSetup = vi.fn();
  render(<AccountsView rev={0} onChanged={onChanged} onSetup={onSetup} />);
  return { onChanged, onSetup, user: userEvent.setup() };
}

const THREE = [
  accountView({ id: 1, handle: "u/alice" }),
  accountView({ id: 2, handle: "u/bob", status: "suspended" }),
  accountView({ id: 3, platform: "bluesky", handle: "bob.bsky.social", paused: true }),
];

describe("AccountsView", () => {
  beforeEach(() => {
    vi.spyOn(accountsApi, "checkAll").mockResolvedValue(list([]));
  });

  it("shows loading, then an error with retry", async () => {
    let fail: (e: unknown) => void = () => {};
    vi.spyOn(api, "accounts").mockReturnValue(new Promise((_, rej) => (fail = rej)));
    vi.spyOn(accountsApi, "stats").mockReturnValue(new Promise(() => {}));
    const onChanged = vi.fn();
    render(<AccountsView rev={0} onChanged={onChanged} onSetup={vi.fn()} />);
    expect(screen.getByText(/Loading accounts/)).toBeTruthy();
    fail(new ApiError(500, "boom"));
    expect(await screen.findByText("boom")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(onChanged).toHaveBeenCalled();
  });

  it("points to Setup when no account is connected", async () => {
    const { onSetup, user } = setup([]);
    expect(await screen.findByRole("heading", { name: "no publishing accounts yet" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Setup" }));
    expect(onSetup).toHaveBeenCalled();
  });

  it("summarises live and dead accounts with the ban rate", async () => {
    setup(THREE);
    const h1 = await screen.findByRole("heading", { level: 1 });
    expect(h1.textContent).toBe("2 of 3 accounts are live — 1 dead.");
    const stats = screen.getByText("Ban rate", { selector: "dt" }).closest("dl") as HTMLElement;
    expect(within(stats).getByText("of 3 accounts")).toBeTruthy();
    expect(within(stats).getByText("33.3%")).toBeTruthy();
    const dead = within(stats).getByText("Dead").nextElementSibling as HTMLElement;
    expect(dead.textContent).toBe("1");
    expect(dead.className).toContain("neg");
    // One paused account is held back.
    expect((within(stats).getByText("Limited or paused").nextElementSibling as HTMLElement).textContent).toBe("1");
  });

  it("breaks the accounts down by platform", async () => {
    setup(THREE);
    const byPlatform = (await screen.findByRole("heading", { name: "By platform" })).closest("section") as HTMLElement;
    const rows = within(byPlatform).getAllByRole("listitem");
    expect(rows[0].textContent).toContain("Reddit");
    expect(rows[0].textContent).toContain("1 live · 1 dead · 0 limited");
    expect(rows[0].textContent).toContain("50%");
    expect(rows[0].textContent).toContain("1 of 2");
    expect(rows[1].textContent).toContain("· 1 paused · 1 not checked");
    expect(screen.getByRole("heading", { name: "Ban rate, daily" })).toBeTruthy();
  });

  it("says all accounts are live", async () => {
    setup([accountView({ id: 1 }), accountView({ id: 2, handle: "u/b" })], { ...STATS, total: tally({ total: 2, live: 2 }) });
    expect((await screen.findByRole("heading", { level: 1 })).textContent).toBe("All 2 accounts are live.");
  });

  it("counts from the list when stats fail, and names a single account", async () => {
    setup([accountView({ id: 1 })], new ApiError(500, "stats unavailable"));
    expect((await screen.findByRole("heading", { level: 1 })).textContent).toBe("All account is live.");
    expect(await screen.findByText("stats unavailable")).toBeTruthy();
    expect(screen.getByText("of 1 account")).toBeTruthy();
    expect(screen.getByText("—")).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "By platform" })).toBeNull();
  });

  it("counts a single live account among dead ones without stats", async () => {
    setup([accountView({ id: 1 }), accountView({ id: 2, status: "invalid" })], new ApiError(500, "x"));
    expect((await screen.findByRole("heading", { level: 1 })).textContent).toBe("1 of 2 accounts is live — 1 dead.");
  });

  it("checks all accounts and refreshes", async () => {
    let done: (v: AccountsResponse) => void = () => {};
    vi.spyOn(accountsApi, "checkAll").mockReturnValue(new Promise((res) => (done = res)));
    const { user, onChanged } = setup(THREE);
    await user.click(await screen.findByRole("button", { name: "Check all" }));
    expect((screen.getByRole("button", { name: "Checking…" }) as HTMLButtonElement).disabled).toBe(true);
    done(list(THREE));
    expect(await screen.findByRole("button", { name: "Check all" })).toBeTruthy();
    expect(onChanged).toHaveBeenCalled();
  });

  it("reports checks that could not run", async () => {
    vi.spyOn(accountsApi, "checkAll").mockResolvedValue({ ...list(THREE), error: "mastodon.social timed out" });
    const { user } = setup(THREE);
    await user.click(await screen.findByRole("button", { name: "Check all" }));
    expect(await screen.findByText("Some checks could not run: mastodon.social timed out")).toBeTruthy();
  });

  it("reports a failed check-all", async () => {
    vi.spyOn(accountsApi, "checkAll").mockRejectedValue(new ApiError(0, "Cannot reach the Radaro server."));
    const { user, onChanged } = setup(THREE);
    await user.click(await screen.findByRole("button", { name: "Check all" }));
    expect(await screen.findByText("Cannot reach the Radaro server.")).toBeTruthy();
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("opens Setup to add an account", async () => {
    const { user, onSetup } = setup(THREE);
    await user.click(await screen.findByRole("button", { name: "Add account" }));
    expect(onSetup).toHaveBeenCalled();
  });
});
