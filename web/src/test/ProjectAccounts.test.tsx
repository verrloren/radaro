import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "../api";
import { ProjectAccounts } from "../components/ProjectAccounts";
import type { SelectOption } from "../components/ui/Select";
import type { AccountView, ProjectPool } from "../types";
import { accountView, PLATFORMS } from "./fixtures";

// Radix Select needs layout APIs jsdom lacks; a native select exercises the same props.
vi.mock("../components/ui/Select", () => ({
  Select: (p: Readonly<{ label: string; value: string; options: SelectOption[]; disabled?: boolean; onChange: (v: string) => void }>) => (
    <select aria-label={p.label} value={p.value} disabled={p.disabled} onChange={(e) => p.onChange(e.target.value)}>
      {p.options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  ),
}));

const ALICE = accountView({ id: 1, handle: "u/alice" });
const BOB = accountView({ id: 2, handle: "u/bob", status: "suspended", status_detail: "banned sitewide" });
const CAROL = accountView({ id: 3, handle: "u/carol", paused: true });
const SKY = accountView({ id: 5, platform: "bluesky", handle: "alice.bsky.social" });
const OWN = [ALICE, BOB, CAROL, SKY];

function pools(reddit: AccountView[], bluesky: AccountView[] = []): ProjectPool[] {
  return [
    { platform: PLATFORMS[0], accounts: reddit },
    { platform: PLATFORMS[1], accounts: bluesky },
    { platform: PLATFORMS[2], accounts: [] },
  ];
}

function setup(own: AccountView[] = OWN, ownError: string | null = null) {
  const onChanged = vi.fn();
  const onConnect = vi.fn();
  render(<ProjectAccounts projectId={4} projectName="Radaro" own={own} ownError={ownError} rev={0} onConnect={onConnect} onChanged={onChanged} />);
  return { onChanged, onConnect, user: userEvent.setup() };
}

const row = (label: string) => screen.getByText(label, { selector: ".binding-name" }).closest("li") as HTMLElement;

describe("ProjectAccounts", () => {
  beforeEach(() => {
    vi.spyOn(api, "projectAccounts").mockResolvedValue(pools([ALICE, BOB]));
    vi.spyOn(api, "bindAccount").mockResolvedValue(pools([ALICE, BOB, CAROL]));
    vi.spyOn(api, "unbindProjectAccount").mockResolvedValue(pools([ALICE]));
    vi.spyOn(api, "unbindAccount").mockResolvedValue(pools([]));
  });

  it("shows loading until the pool arrives", () => {
    vi.spyOn(api, "projectAccounts").mockReturnValue(new Promise(() => {}));
    setup();
    expect(screen.getByText(/Loading accounts/)).toBeTruthy();
  });

  it("lists several accounts per platform, each with its status", async () => {
    setup();
    const list = await screen.findByRole("list", { name: "Reddit accounts in Radaro" });
    const items = within(list).getAllByRole("listitem");
    expect(items.map((li) => li.querySelector(".pool-handle")?.textContent)).toEqual(["u/alice", "u/bob"]);
    expect(within(items[0]).getByText("live")).toBeTruthy();
    expect(within(items[1]).getByText("suspended")).toBeTruthy();
    expect(within(items[1]).getByText(/banned sitewide/)).toBeTruthy();
    expect(api.projectAccounts).toHaveBeenCalledWith(4, expect.anything());
  });

  it("explains an empty pool: any account of the user's, or none yet", async () => {
    setup();
    await screen.findByRole("list", { name: "Reddit accounts in Radaro" });
    expect(within(row("Bluesky")).getByText("Any of your Bluesky accounts")).toBeTruthy();
    expect(within(row("Mastodon")).getByText("No Mastodon account yet")).toBeTruthy();
  });

  it("offers only the accounts not in the pool, and adds one", async () => {
    const { user, onChanged } = setup();
    const add = await screen.findByRole("combobox", { name: "Add a Reddit account to Radaro" });
    expect(within(add).getAllByRole("option").map((o) => o.textContent)).toEqual(["Add an account…", "u/carol"]);
    await user.selectOptions(add, "3");
    expect(api.bindAccount).toHaveBeenCalledWith(4, "reddit", 3);
    expect(onChanged).toHaveBeenCalled();
    // No other Mastodon account to add, so no picker there.
    expect(within(row("Mastodon")).queryByRole("combobox")).toBeNull();
  });

  it("removes one account from the pool", async () => {
    const { user, onChanged } = setup();
    await user.click(await screen.findByRole("button", { name: "Remove u/bob from Radaro" }));
    expect(api.unbindProjectAccount).toHaveBeenCalledWith(4, "reddit", 2);
    expect(onChanged).toHaveBeenCalled();
  });

  it("clears a platform with more than one account", async () => {
    vi.spyOn(api, "projectAccounts").mockResolvedValue(pools([ALICE, BOB], [SKY]));
    const { user, onChanged } = setup();
    await user.click(await screen.findByRole("button", { name: "Clear Reddit accounts of Radaro" }));
    expect(api.unbindAccount).toHaveBeenCalledWith(4, "reddit");
    expect(onChanged).toHaveBeenCalled();
    // One account: removing it is the same as clearing.
    expect(screen.queryByRole("button", { name: "Clear Bluesky accounts of Radaro" })).toBeNull();
  });

  it("asks to connect a new account on a platform", async () => {
    const { user, onConnect } = setup();
    await screen.findByRole("list", { name: "Reddit accounts in Radaro" });
    await user.click(within(row("Mastodon")).getByRole("button", { name: "Connect new" }));
    expect(onConnect).toHaveBeenCalledWith(PLATFORMS[2]);
  });

  it("shows a refused change and disables the row while it runs", async () => {
    let fail: (e: unknown) => void = () => {};
    vi.spyOn(api, "unbindProjectAccount").mockReturnValue(new Promise((_, rej) => (fail = rej)));
    const { user, onChanged } = setup();
    await user.click(await screen.findByRole("button", { name: "Remove u/alice from Radaro" }));
    expect((screen.getByRole("button", { name: "Remove u/bob from Radaro" }) as HTMLButtonElement).disabled).toBe(true);
    fail(new ApiError(404, "account not found"));
    expect(await screen.findByText("account not found")).toBeTruthy();
    expect(onChanged).not.toHaveBeenCalled();
    expect((screen.getByRole("button", { name: "Remove u/bob from Radaro" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("shows why the pool or the accounts could not load", async () => {
    vi.spyOn(api, "projectAccounts").mockRejectedValue(new ApiError(500, "pool unavailable"));
    setup([], "accounts unavailable");
    expect(await screen.findByText("pool unavailable")).toBeTruthy();
  });

  it("shows the accounts error when the pool loads", async () => {
    setup(OWN, "accounts unavailable");
    expect(await screen.findByText("accounts unavailable")).toBeTruthy();
  });

  it("ignores the empty choice of the picker", async () => {
    const { user } = setup();
    const add = await screen.findByRole("combobox", { name: "Add a Reddit account to Radaro" });
    await user.selectOptions(add, "3");
    await user.selectOptions(add, "");
    expect(api.bindAccount).toHaveBeenCalledTimes(1);
  });
});
