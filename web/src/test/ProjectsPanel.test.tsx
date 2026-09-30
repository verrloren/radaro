import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "../api";
import { ProjectsPanel } from "../components/ProjectsPanel";
import type { AccountsResponse, Keyword, Platform, Project } from "../types";
import { accountView, loaded, PLATFORMS, project } from "./fixtures";

vi.mock("../components/ConnectForms", () => ({
  ConnectForm: ({ platform, projectId, onDone }: Readonly<{ platform: Platform; projectId?: number; onDone: () => void }>) => (
    <button type="button" onClick={onDone}>
      connect {platform.name} to {projectId}
    </button>
  ),
  RedditForm: ({ projectId }: Readonly<{ projectId?: number }>) => <p>reddit sign-in for {projectId}</p>,
}));

const PROJECTS: Project[] = [project({ id: 1, name: "Default", is_default: true, query_count: 1 }), project({ id: 2, name: "Radaro", query_count: 2 })];
const ACCOUNTS: AccountsResponse = { platforms: PLATFORMS, accounts: [accountView({ id: 1, handle: "u/alice" })] };

function keyword(id: number, query: string): Keyword {
  return { id, project_id: 2, query, sources: ["hn"], added_at: "2026-09-01T10:00:00Z", last_scanned_at: null, mention_count: 3 };
}

function setup(selectedId: number | null = 2, projects = loaded(PROJECTS)) {
  const onSelect = vi.fn();
  const onChanged = vi.fn();
  render(<ProjectsPanel projects={projects} selectedId={selectedId} accounts={loaded(ACCOUNTS)} rev={0} onSelect={onSelect} onChanged={onChanged} />);
  return { onSelect, onChanged, user: userEvent.setup() };
}

beforeEach(() => {
  vi.spyOn(api, "keywords").mockResolvedValue([keyword(1, "radaro"), keyword(2, "social listening")]);
  vi.spyOn(api, "projectAccounts").mockResolvedValue(PLATFORMS.map((platform) => ({ platform, accounts: [] })));
});

describe("ProjectsPanel", () => {
  it("lists projects and asks to pick one", () => {
    setup(null);
    const items = within(screen.getByRole("list", { name: "Projects" })).getAllByRole("button");
    expect(items.map((b) => b.textContent)).toEqual(["Default · default1 keyword · 12 mentions", "Radaro2 keywords · 12 mentions"]);
    expect(screen.getByText("Pick a project to manage its keywords and accounts.")).toBeTruthy();
  });

  it("toggles the selection", async () => {
    const { user, onSelect } = setup(2);
    await user.click(screen.getByRole("button", { name: /^Radaro2 keywords/ }));
    expect(onSelect).toHaveBeenCalledWith(null);
    await user.click(screen.getByRole("button", { name: /^Default/ }));
    expect(onSelect).toHaveBeenCalledWith(1);
  });

  it("shows loading and errors for the list", () => {
    setup(null, loaded<Project[]>(undefined, "projects unavailable", true));
    expect(screen.getByText(/Loading projects/)).toBeTruthy();
    expect(screen.getByText("projects unavailable")).toBeTruthy();
  });

  it("says when the selected project is gone", () => {
    setup(9);
    expect(screen.getByText("This project does not exist.")).toBeTruthy();
  });

  it("creates a project and selects it", async () => {
    vi.spyOn(api, "createProject").mockResolvedValue(project({ id: 3, name: "New" }));
    const { user, onSelect, onChanged } = setup(null);
    await user.type(screen.getByLabelText("New project name"), "  New ");
    await user.click(screen.getByRole("button", { name: "Create" }));
    expect(api.createProject).toHaveBeenCalledWith("New");
    expect(onSelect).toHaveBeenCalledWith(3);
    expect(onChanged).toHaveBeenCalled();
    expect((screen.getByLabelText("New project name") as HTMLInputElement).value).toBe("");
  });

  it("shows why a project could not be created", async () => {
    vi.spyOn(api, "createProject").mockRejectedValue(new ApiError(409, "name taken"));
    const { user } = setup(null);
    await user.type(screen.getByLabelText("New project name"), "Radaro{Enter}");
    expect(await screen.findByText("name taken")).toBeTruthy();
  });

  it("lists, adds and removes keywords", async () => {
    vi.spyOn(api, "addKeywords").mockResolvedValue({ added: 1, keywords: [] });
    vi.spyOn(api, "removeKeyword").mockResolvedValue({ deleted: true });
    const { user, onChanged } = setup();
    expect(await screen.findByText("social listening")).toBeTruthy();

    const box = screen.getByLabelText(/Add keywords/);
    await user.type(box, "go, radaro");
    expect(screen.getByRole("button", { name: "Add 2" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Add 2" }));
    expect(api.addKeywords).toHaveBeenCalledWith(2, ["go", "radaro"]);
    expect(await screen.findByText(/Added 1 keyword · 1 already in the project./)).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Remove “radaro” from Radaro" }));
    expect(api.removeKeyword).toHaveBeenCalledWith(2, 1);
    expect(onChanged).toHaveBeenCalledTimes(2);
  });

  it("adds keywords with Ctrl+Enter", async () => {
    vi.spyOn(api, "addKeywords").mockResolvedValue({ added: 2, keywords: [] });
    const { user } = setup();
    await user.type(screen.getByLabelText(/Add keywords/), "a{Enter}b");
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(api.addKeywords).toHaveBeenCalledWith(2, ["a", "b"]);
    expect(await screen.findByText(/Added 2 keywords\./)).toBeTruthy();
  });

  it("invites a first keyword", async () => {
    vi.spyOn(api, "keywords").mockResolvedValue([]);
    setup();
    expect(await screen.findByText(/No keywords yet/)).toBeTruthy();
  });

  it("renames a project, or cancels", async () => {
    vi.spyOn(api, "renameProject").mockResolvedValue(project({ id: 2, name: "Radar" }));
    const { user, onChanged } = setup();
    await user.click(screen.getByRole("button", { name: "Rename" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: "Rename" }));
    const name = screen.getByLabelText("Project name");
    await user.clear(name);
    await user.type(name, "Radar");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.renameProject).toHaveBeenCalledWith(2, "Radar");
    expect(onChanged).toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Rename" })).toBeTruthy();
  });

  it("deletes a project after confirming", async () => {
    vi.spyOn(api, "deleteProject").mockResolvedValue({ deleted: true });
    const { user, onSelect } = setup();
    await user.click(screen.getByRole("button", { name: "Delete project" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: "Delete project" }));
    await user.click(within(screen.getByRole("group", { name: "Confirm project deletion" })).getByRole("button", { name: "Yes, delete" }));
    expect(api.deleteProject).toHaveBeenCalledWith(2);
    expect(onSelect).toHaveBeenCalledWith(null);
  });

  it("never offers to delete the default project", () => {
    setup(1);
    expect(screen.queryByRole("button", { name: "Delete project" })).toBeNull();
  });

  it("shows a failed keyword change", async () => {
    vi.spyOn(api, "removeKeyword").mockRejectedValue(new ApiError(404, "keyword not found"));
    const { user } = setup();
    await user.click(await screen.findByRole("button", { name: "Remove “radaro” from Radaro" }));
    expect(await screen.findByText("keyword not found")).toBeTruthy();
  });

  it("shows the project's account pool", async () => {
    setup();
    expect(await screen.findByRole("heading", { name: "Accounts" })).toBeTruthy();
    expect(api.projectAccounts).toHaveBeenCalledWith(2, expect.anything());
  });

  it("connects a new account into the project", async () => {
    const { user, onChanged } = setup();
    const bluesky = (await screen.findByText("Bluesky", { selector: ".binding-name" })).closest("li") as HTMLElement;
    await user.click(within(bluesky).getByRole("button", { name: "Connect new" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByRole("heading", { name: "Connect Bluesky to Radaro" })).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "connect bluesky to 2" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(onChanged).toHaveBeenCalled();
  });

  it("signs in to Reddit for the project, and closes the dialog", async () => {
    const { user } = setup();
    const reddit = (await screen.findByText("Reddit", { selector: ".binding-name" })).closest("li") as HTMLElement;
    await user.click(within(reddit).getByRole("button", { name: "Connect new" }));
    expect(screen.getByText("reddit sign-in for 2")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
