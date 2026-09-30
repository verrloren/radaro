import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { AccountsBanChart } from "../components/AccountsBanChart";
import type { BanRatePoint } from "../types";
import { PLATFORMS } from "./fixtures";

function days(n: number, platform: string, dead: (i: number) => number, total = 4): BanRatePoint[] {
  return Array.from({ length: n }, (_, i) => ({
    day: `2026-09-${String(i + 1).padStart(2, "0")}`,
    platform,
    total,
    dead: dead(i),
  }));
}

const table = () => screen.getByRole("table");
const rows = () => within(table()).getAllByRole("row").slice(1);
const chart = () => screen.getByRole("img");

describe("AccountsBanChart", () => {
  it("has nothing to draw without accounts", () => {
    render(<AccountsBanChart series={[]} platforms={PLATFORMS} />);
    expect(screen.getByText("No accounts yet, so no ban rate.")).toBeTruthy();
    expect(screen.queryByRole("group")).toBeNull();
  });

  it("draws one platform without a filter", () => {
    render(<AccountsBanChart series={days(3, "reddit", (i) => i)} platforms={PLATFORMS} />);
    expect(screen.queryByRole("group")).toBeNull();
    expect(chart().getAttribute("aria-label")).toBe("All platforms: share of dead accounts per day, Sep 1 – Sep 3, 2026. Peak 50%.");
    expect(rows().map((r) => r.textContent)).toEqual(["Sep 1, 2026040%", "Sep 2, 20261425%", "Sep 3, 20262450%"]);
    expect(document.querySelector(".acc-line path")?.getAttribute("d")).toBe("M0 100 L50 50 L100 0");
    // 50% peak: the axis tops out at 50%, with ticks at both ends and the middle.
    expect(screen.getByText("50%", { selector: ".grid-label" })).toBeTruthy();
    const ticks = [...document.querySelectorAll<HTMLElement>(".x-tick")];
    expect(ticks.map((t) => t.style.transform)).toEqual(["none", "translateX(-50%)", "translateX(-100%)"]);
  });

  it("switches between platforms", async () => {
    const series = [...days(2, "reddit", () => 1, 2), ...days(2, "bluesky", () => 0, 2), ...days(1, "lemmy", () => 1, 1)];
    render(<AccountsBanChart series={series} platforms={PLATFORMS} />);
    const group = screen.getByRole("group", { name: "Platform" });
    expect(group.tagName).toBe("FIELDSET");
    const chips = within(group).getAllByRole("button");
    expect(chips.map((c) => c.textContent)).toEqual(["All", "Reddit", "Bluesky", "lemmy"]);
    expect(chips[0].getAttribute("aria-pressed")).toBe("true");
    // Day 1 across all platforms: 2 dead of 5.
    expect(rows()[0].textContent).toBe("Sep 1, 20262540%");

    await userEvent.click(chips[2]);
    expect(chips[2].getAttribute("aria-pressed")).toBe("true");
    expect(chart().getAttribute("aria-label")).toMatch(/^Bluesky: /);
    expect(rows().map((r) => r.textContent)).toEqual(["Sep 1, 2026020%", "Sep 2, 2026020%"]);
    expect(screen.getByText("Bluesky · dead = invalid or suspended")).toBeTruthy();

    await userEvent.click(chips[3]);
    expect(chart().getAttribute("aria-label")).toMatch(/^lemmy: .*Sep 1, 2026\. Peak 100%\.$/);
    expect(document.querySelector(".acc-line path")?.getAttribute("d")).toBe("M50 0");
  });

  it("breaks the line on days without accounts", () => {
    const series = [
      { day: "2026-09-01", platform: "reddit", total: 10, dead: 1 },
      { day: "2026-09-02", platform: "reddit", total: 0, dead: 0 },
      { day: "2026-09-03", platform: "reddit", total: 10, dead: 1 },
      { day: "bad", platform: "reddit", total: 10, dead: 1 },
    ];
    render(<AccountsBanChart series={series} platforms={PLATFORMS} />);
    expect(rows()).toHaveLength(3);
    expect(rows()[1].textContent).toBe("Sep 2, 202600—");
    expect(document.querySelector(".acc-line path")?.getAttribute("d")).toBe("M0 0 M100 0");
    expect(screen.getByText("10%", { selector: ".grid-label" })).toBeTruthy();
  });

  it.each([
    [8, 4],
    [20, 5],
  ])("places %i days on %i ticks", (n, ticks) => {
    render(<AccountsBanChart series={days(n, "reddit", () => 0)} platforms={PLATFORMS} />);
    expect(document.querySelectorAll(".x-tick")).toHaveLength(ticks);
  });

  it("reads out the hovered day", () => {
    render(<AccountsBanChart series={days(5, "reddit", (i) => (i === 4 ? 4 : 1))} platforms={PLATFORMS} />);
    const el = chart();
    expect(screen.getByText("Hover the chart for details")).toBeTruthy();
    vi.spyOn(el, "getBoundingClientRect").mockReturnValue({ left: 0, width: 400, top: 0, height: 100 } as DOMRect);
    fireEvent.mouseMove(el, { clientX: 390 });
    const readout = document.querySelector(".readout") as HTMLElement;
    expect(readout.textContent).toBe("Sep 5, 2026 · 100% (4 of 4)");
    expect(document.querySelector(".acc-marker")).toBeTruthy();
    expect((document.querySelector(".acc-cross") as HTMLElement).style.left).toBe("100%");
    fireEvent.mouseMove(el, { clientX: -50 });
    expect(readout.textContent).toBe("Sep 1, 2026 · 25% (1 of 4)");
    fireEvent.mouseLeave(el);
    expect(screen.getByText("Hover the chart for details")).toBeTruthy();
  });

  it("ignores the pointer before the chart has a size, and hovers a day without a rate", () => {
    render(<AccountsBanChart series={[{ day: "2026-09-01", platform: "reddit", total: 0, dead: 0 }]} platforms={PLATFORMS} />);
    const el = chart();
    vi.spyOn(el, "getBoundingClientRect").mockReturnValue({ left: 0, width: 0 } as DOMRect);
    fireEvent.mouseMove(el, { clientX: 10 });
    expect(screen.getByText("Hover the chart for details")).toBeTruthy();
    vi.spyOn(el, "getBoundingClientRect").mockReturnValue({ left: 0, width: 100 } as DOMRect);
    fireEvent.mouseMove(el, { clientX: 10 });
    expect((document.querySelector(".readout") as HTMLElement).textContent).toBe("Sep 1, 2026 · — (0 of 0)");
    expect(document.querySelector(".acc-marker")).toBeNull();
    expect((document.querySelector(".acc-cross") as HTMLElement).style.left).toBe("50%");
  });
});
