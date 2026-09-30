import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { api, ApiError } from "../api";
import { AccountImportForm, importRowCount } from "../components/AccountImportForm";

describe("AccountImportForm", () => {
  it("counts CSV records and JSON objects before import", () => {
    expect(importRowCount('platform,handle,secret\nbluesky,a,k\n"mastodon","b\nmore","k"')).toBe(2);
    expect(importRowCount("secret,platform\nk,bluesky")).toBe(1);
    expect(importRowCount('[{"platform":"bluesky"},{"platform":"mastodon"}]')).toBe(2);
    expect(importRowCount("[broken")).toBe(0);
  });

  it("imports rows and shows each result without credentials", async () => {
    vi.spyOn(api, "importAccounts").mockResolvedValue({ added: 1, updated: 0, errors: 1, results: [
      { row: 2, platform: "bluesky", handle: "alice", status: "added" },
      { row: 3, platform: "reddit", handle: "bob", status: "error", error: "Use Reddit OAuth" },
    ] });
    const onChanged = vi.fn();
    const user = userEvent.setup();
    render(<AccountImportForm onChanged={onChanged} />);
    const input = 'platform,handle,secret\nbluesky,alice,private-key\nreddit,bob,another-key';
    await user.type(screen.getByLabelText("Or paste account rows"), input);
    expect(screen.getByText("2 rows ready to import.")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Import 2 accounts" }));
    expect(api.importAccounts).toHaveBeenCalledWith(input);
    expect(await screen.findByText("1 added · 0 updated · 1 errors")).toBeTruthy();
    expect(within(screen.getByRole("table")).getByText("Use Reddit OAuth")).toBeTruthy();
    expect(screen.getByRole("table").textContent).not.toContain("private-key");
    expect(onChanged).toHaveBeenCalled();
  });

  it("reads a selected CSV file into the preview", async () => {
    const user = userEvent.setup();
    render(<AccountImportForm onChanged={vi.fn()} />);
    const file = new File(["devto,alice,key"], "accounts.csv", { type: "text/csv" });
    Object.defineProperty(file, "text", { value: async () => "devto,alice,key" });
    await user.upload(screen.getByLabelText("Choose a CSV or JSON file"), file);
    expect(await screen.findByText("1 row ready to import.")).toBeTruthy();
    expect((screen.getByLabelText("Or paste account rows") as HTMLTextAreaElement).value).toBe("devto,alice,key");
  });

  it("shows import errors and keeps input for correction", async () => {
    vi.spyOn(api, "importAccounts").mockRejectedValue(new ApiError(422, "invalid CSV"));
    const user = userEvent.setup();
    render(<AccountImportForm onChanged={vi.fn()} />);
    await user.type(screen.getByLabelText("Or paste account rows"), "bluesky,alice,key");
    await user.click(screen.getByRole("button", { name: "Import 1 account" }));
    expect(await screen.findByText("invalid CSV")).toBeTruthy();
    expect((screen.getByLabelText("Or paste account rows") as HTMLTextAreaElement).value).toBe("bluesky,alice,key");
  });
});
