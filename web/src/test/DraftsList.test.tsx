import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { draftWhere, DraftsList, platformBadge, STATUS_LABEL } from "../components/DraftsList";
import { draft, PLATFORMS } from "./fixtures";

describe("platformBadge", () => {
  it("uses the platform label and color", () => {
    expect(platformBadge("reddit", PLATFORMS)).toEqual({ name: "reddit", label: "Reddit", glyph: "R", color: "#ff4500" });
  });

  it("gives Dev.to its initial and unknown platforms a neutral color", () => {
    expect(platformBadge("devto", PLATFORMS).glyph).toBe("D");
    expect(platformBadge("lemmy", [])).toEqual({ name: "lemmy", label: "lemmy", glyph: "L", color: "#8e8e8e" });
  });
});

describe("draftWhere", () => {
  it.each([
    [draft({ platform: "reddit", kind: "post", community: "golang" }), "r/golang"],
    [draft({ platform: "reddit", kind: "post", community: "/r/rust " }), "r/rust"],
    [draft({ platform: "reddit", kind: "reply", community: "r/golang" }), "reply in r/golang"],
    [draft({ platform: "reddit", kind: "reply", community: null }), "reply"],
    [draft({ platform: "devto", kind: "post", community: "go, cli" }), "go, cli"],
    [draft({ platform: "bluesky", kind: "post", community: null }), "post"],
    [draft({ platform: "devto", kind: "post", community: "  " }), "post"],
  ])("%#", (d, want) => {
    expect(draftWhere(d)).toBe(want);
  });
});

describe("DraftsList", () => {
  const drafts = [
    draft({ id: 1, title: "Launch post", body: "body one" }),
    draft({ id: 2, title: "  ", body: "Just the body", status: "published", published_at: "2026-09-02T10:00:00Z", removed_at: "2026-09-03T10:00:00Z" }),
    draft({ id: 3, platform: "devto", title: null, body: "Dev post", status: "skipped" }),
  ];

  it("lists drafts with their platform, target, text and status", () => {
    render(<DraftsList drafts={drafts} platforms={PLATFORMS} selected={2} onSelect={vi.fn()} stale={false} />);
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(items[0].textContent).toContain("Launch post");
    expect(items[0].textContent).toContain(STATUS_LABEL.draft);
    expect(items[1].textContent).toContain("Just the body");
    expect(items[1].textContent).toContain("removed");
    expect(items[2].textContent).toContain("Dev.to");
    expect(items[2].textContent).toContain("skipped");
    expect(screen.getByRole("button", { name: /Just the body/ }).getAttribute("aria-current")).toBe("true");
    expect(screen.getByRole("button", { name: /Launch post/ }).getAttribute("aria-current")).toBeNull();
    expect(screen.getByRole("list", { name: "Drafts" }).className).toBe("draft-list");
  });

  it("dims a stale list and reports the chosen draft", async () => {
    const onSelect = vi.fn();
    render(<DraftsList drafts={drafts} platforms={[]} selected={null} onSelect={onSelect} stale />);
    expect(screen.getByRole("list", { name: "Drafts" }).className).toContain("is-stale");
    await userEvent.click(screen.getByRole("button", { name: /Dev post/ }));
    expect(onSelect).toHaveBeenCalledWith(3);
  });
});
