import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Selection } from "../App";
import { Rail } from "../components/Rail";
import type { SelectOption } from "../components/ui/Select";
import type { Meta, Project } from "../types";
import { SENTIMENTS } from "../types";
import { loaded, project, USER } from "./fixtures";

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

const META: Meta = { version: "v2.0.0", sources: [], default_sources: [] };

function setup(sel: Selection, over: Partial<Parameters<typeof Rail>[0]> = {}) {
  const onSelect = vi.fn();
  const onView = vi.fn();
  render(
    <Rail
      projects={loaded<Project[]>([project({ id: 2, name: "Radaro" })])}
      keywords={["radaro", "go"]}
      keywordsLoading={false}
      keywordsError={null}
      sel={sel}
      onSelect={onSelect}
      view={sel.v}
      onView={onView}
      total={1234}
      needSetup={2}
      meta={loaded(META)}
      user={{ ...USER, is_admin: true }}
      onSignOut={vi.fn()}
      {...over}
    />,
  );
  return { onSelect, onView, user: userEvent.setup() };
}

describe("Rail", () => {
  it("lists five sections with the mention total and the setup badge", () => {
    setup({ p: null, q: null, v: "accounts" });
    const nav = screen.getByRole("navigation", { name: "Sections" });
    const items = within(nav).getAllByRole("button");
    expect(items.map((b) => b.querySelector(".nav-name")?.textContent)).toEqual(["Listen1,234", "Scan", "Drafts", "Accounts", "Setup2 need setup"]);
    expect(items[3].getAttribute("aria-current")).toBe("page");
    expect(items[3].className).toBe("nav-item nav-accounts is-on");
    expect(screen.getByText(/ · admin/)).toBeTruthy();
    expect(screen.getByText(/v2\.0\.0/)).toBeTruthy();
  });

  it("switches sections", async () => {
    const { user, onView } = setup({ p: null, q: null, v: "listen" });
    await user.click(screen.getByRole("button", { name: /^Drafts/ }));
    expect(onView).toHaveBeenCalledWith("drafts");
  });

  it("picks a project, which clears the keyword", async () => {
    const { user, onSelect } = setup({ p: null, q: "go", v: "drafts" });
    await user.selectOptions(screen.getByRole("combobox", { name: "Project" }), "2");
    expect(onSelect).toHaveBeenCalledWith({ p: 2, q: null, v: "drafts" });
    await user.selectOptions(screen.getByRole("combobox", { name: "Project" }), "");
    expect(onSelect).toHaveBeenLastCalledWith({ p: null, q: null, v: "drafts" });
  });

  it("picks a keyword within the project, and keeps an unknown one listed", async () => {
    const { user, onSelect } = setup({ p: 2, q: "elsewhere", v: "listen" });
    const kw = screen.getByRole("combobox", { name: "Keyword" });
    expect(within(kw).getAllByRole("option").map((o) => o.textContent)).toEqual(["Whole project", "elsewhere", "radaro", "go"]);
    await user.selectOptions(kw, "go");
    expect(onSelect).toHaveBeenCalledWith({ p: 2, q: "go", v: "listen" });
    await user.selectOptions(kw, "");
    expect(onSelect).toHaveBeenLastCalledWith({ p: 2, q: null, v: "listen" });
  });

  it("marks a missing project, errors and an unreachable server", () => {
    setup(
      { p: 9, q: null, v: "listen" },
      { meta: loaded<Meta>(undefined, "down"), keywordsError: "keywords unavailable", total: undefined, needSetup: 0 },
    );
    expect(screen.getByRole("option", { name: "Project #9 (missing)" })).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toBe("keywords unavailable");
    expect(screen.getByText("offline")).toBeTruthy();
    expect(screen.getByText("Can't reach the server")).toBeTruthy();
  });

  it("keeps the sentiment order the dashboard relies on", () => {
    expect(SENTIMENTS).toEqual(["positive", "neutral", "negative"]);
  });
});
