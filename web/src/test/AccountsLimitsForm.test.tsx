import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { accountsApi, ApiError } from "../api";
import { AccountsLimitsForm } from "../components/AccountsLimitsForm";
import { accountView } from "./fixtures";

const daily = () => screen.getByLabelText("Publications per 24 hours") as HTMLInputElement;
const gap = () => screen.getByLabelText(/Minimum gap between publications/) as HTMLInputElement;
const cooldown = () => screen.getByLabelText(/Cooldown per community/) as HTMLInputElement;

function setup(over = {}) {
  const onDone = vi.fn();
  render(<AccountsLimitsForm account={accountView({ id: 2, handle: "u/alice", ...over })} onDone={onDone} />);
  return { onDone, user: userEvent.setup() };
}

describe("AccountsLimitsForm", () => {
  beforeEach(() => {
    vi.spyOn(accountsApi, "update").mockResolvedValue(accountView());
  });

  it("fills the account's own limits and shows the defaults it is on", () => {
    setup({ daily_limit: 3, min_interval_sec: 5400, community_cooldown_h: null, limits: { daily: 3, min_interval_sec: 5400, community_cooldown_h: 24, custom: true } });
    expect(screen.getByRole("form", { name: "Limits for u/alice" })).toBeTruthy();
    expect(daily().value).toBe("3");
    expect(gap().value).toBe("5400");
    expect(cooldown().value).toBe("");
    // An overridden limit hides its default; the others show it.
    expect(daily().placeholder).toBe("default: 3");
    expect(gap().placeholder).toBe("default: 5400");
    expect(cooldown().placeholder).toBe("default: 24");
  });

  it("says when a default sets no limit", () => {
    setup({ limits: { daily: 5, min_interval_sec: 0, community_cooldown_h: 24, custom: false } });
    expect(daily().placeholder).toBe("default: 5");
    expect(gap().placeholder).toBe("default: none");
  });

  it("saves seconds exactly and blanks as platform defaults", async () => {
    const { user, onDone } = setup();
    await user.type(daily(), "10");
    await user.type(gap(), "45");
    await user.click(screen.getByRole("button", { name: "Save limits" }));
    expect(accountsApi.update).toHaveBeenCalledWith(2, { daily_limit: 10, min_interval_sec: 45, community_cooldown_h: 0 });
    expect(onDone).toHaveBeenCalled();
  });

  it("preserves a non-minute interval when saving other limits", async () => {
    const { user } = setup({ min_interval_sec: 91 });
    expect(gap().value).toBe("91");
    await user.type(daily(), "10");
    await user.click(screen.getByRole("button", { name: "Save limits" }));
    expect(accountsApi.update).toHaveBeenCalledWith(2, { daily_limit: 10, min_interval_sec: 91, community_cooldown_h: 0 });
  });

  it("shows the platform default even when the account has an override", () => {
    setup({ daily_limit: 3, default_limits: { daily: 5, min_interval_sec: 1800, community_cooldown_h: 24 } });
    expect(daily().placeholder).toBe("default: 5");
    expect(gap().placeholder).toBe("default: 1800");
  });

  it("rejects anything but a whole number", async () => {
    const { user, onDone } = setup();
    await user.type(daily(), "2.5");
    await user.click(screen.getByRole("button", { name: "Save limits" }));
    expect(screen.getByRole("alert").textContent).toContain("Publications per 24 hours: enter a whole number or leave it blank.");
    expect(accountsApi.update).not.toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();
  });

  it("caps each field with its unit", async () => {
    const { user } = setup();
    await user.type(cooldown(), "9999");
    await user.click(screen.getByRole("button", { name: "Save limits" }));
    expect(screen.getByRole("alert").textContent).toContain("Cooldown per community: at most 2160 hours.");
    await user.clear(cooldown());
    await user.type(daily(), "1001");
    await user.click(screen.getByRole("button", { name: "Save limits" }));
    expect(screen.getByRole("alert").textContent).toContain("Publications per 24 hours: at most 1000.");
  });

  it("resets every field to the platform default", async () => {
    const { user } = setup({ daily_limit: 3, min_interval_sec: 600, community_cooldown_h: 12 });
    await user.click(screen.getByRole("button", { name: "Use platform defaults" }));
    expect([daily().value, gap().value, cooldown().value]).toEqual(["", "", ""]);
    await user.click(screen.getByRole("button", { name: "Save limits" }));
    expect(accountsApi.update).toHaveBeenCalledWith(2, { daily_limit: 0, min_interval_sec: 0, community_cooldown_h: 0 });
  });

  it("shows a server error and lets the user try again", async () => {
    let fail: (e: unknown) => void = () => {};
    vi.spyOn(accountsApi, "update").mockReturnValue(new Promise((_, rej) => (fail = rej)));
    const { user, onDone } = setup();
    await user.click(screen.getByRole("button", { name: "Save limits" }));
    expect((screen.getByRole("button", { name: "Saving…" }) as HTMLButtonElement).disabled).toBe(true);
    expect(daily().disabled).toBe(true);
    fail(new ApiError(400, "daily_limit out of range"));
    expect(await screen.findByText("daily_limit out of range")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Save limits" }) as HTMLButtonElement).disabled).toBe(false);
    expect(onDone).not.toHaveBeenCalled();
  });
});
