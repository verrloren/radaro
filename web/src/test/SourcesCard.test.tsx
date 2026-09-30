import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { SourcesCard } from "../components/SourcesCard";
import { makeSourceLookup } from "../sources";
import { loaded } from "./fixtures";

describe("SourcesCard", () => {
  it("opens account import from Setup and refreshes connected accounts", async () => {
    vi.spyOn(api, "importAccounts").mockResolvedValue({ added: 1, updated: 0, errors: 0, results: [{ row: 1, platform: "devto", handle: "alice", status: "added" }] });
    const onChanged = vi.fn();
    const user = userEvent.setup();
    render(<SourcesCard isAdmin={false} meta={loaded({ version: "v1", sources: [], default_sources: [] })} settings={loaded([])} accounts={loaded({ platforms: [], accounts: [] })} notice={null} tracking={undefined} lookup={makeSourceLookup([])} onChanged={onChanged} />);
    await user.click(screen.getByRole("button", { name: "Import accounts" }));
    const dialog = screen.getByRole("dialog", { name: "Import accounts" });
    await user.type(within(dialog).getByLabelText("Or paste account rows"), "devto,alice,key");
    await user.click(within(dialog).getByRole("button", { name: "Import 1 account" }));
    expect(await within(dialog).findByText("1 added · 0 updated · 0 errors")).toBeTruthy();
    expect(onChanged).toHaveBeenCalled();
  });
});
