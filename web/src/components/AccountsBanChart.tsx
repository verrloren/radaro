import { useMemo, useState, type MouseEvent } from "react";
import type { BanRatePoint, Platform } from "../types";
import { fmtDay, fmtDayRange, fmtNum, parseDay } from "../format";

interface Day {
  day: number;
  total: number;
  dead: number;
  /** dead / total, null when there were no accounts. */
  rate: number | null;
}

function buildDays(series: BanRatePoint[], platform: string): Day[] {
  const byDay = new Map<string, { total: number; dead: number }>();
  for (const p of series) {
    if (platform && p.platform !== platform) continue;
    const d = byDay.get(p.day) ?? { total: 0, dead: 0 };
    d.total += p.total;
    d.dead += p.dead;
    byDay.set(p.day, d);
  }
  return [...byDay.entries()]
    .map(([day, d]) => ({ day: parseDay(day), ...d, rate: d.total > 0 ? d.dead / d.total : null }))
    .filter((d) => !Number.isNaN(d.day))
    .sort((a, b) => a.day - b.day);
}

// Percent axis top: the next of 10/20/25/50/100 above the peak.
function topPct(peak: number): number {
  const pct = peak * 100;
  for (const t of [10, 20, 25, 50, 100]) if (pct <= t) return t;
  return 100;
}

function tickCount(n: number): number {
  if (n >= 20) return 5;
  if (n >= 8) return 4;
  return Math.min(n, 3);
}

function tickIndexes(n: number): number[] {
  if (n <= 1) return n === 1 ? [0] : [];
  const t = tickCount(n);
  const out = new Set<number>();
  for (let k = 0; k < t; k++) out.add(Math.round((k * (n - 1)) / (t - 1)));
  return [...out];
}

const pct = (r: number | null) => (r === null ? "—" : `${Math.round(r * 1000) / 10}%`);

// The first and last tick labels align to the chart edges; the rest center on their tick.
function tickTransform(i: number, n: number): string {
  if (i === 0) return "none";
  return i === n - 1 ? "translateX(-100%)" : "translateX(-50%)";
}

/** SVG path through the days that have a rate; a day without one breaks the line. */
function linePath(days: Day[], x: (i: number) => number, y: (r: number) => number): string {
  const parts: string[] = [];
  days.forEach((d, i) => {
    if (d.rate === null) return;
    const cmd = i > 0 && days[i - 1].rate !== null ? "L" : "M";
    parts.push(`${cmd}${x(i)} ${y(d.rate)}`);
  });
  return parts.join(" ");
}

/** Share of dead (invalid or suspended) accounts per day, overall or for one platform. */
export function AccountsBanChart({ series, platforms }: Readonly<{ series: BanRatePoint[]; platforms: Platform[] }>) {
  const present = useMemo(() => {
    const names = new Set(series.map((p) => p.platform));
    const known = platforms.filter((p) => names.has(p.name));
    const extra = [...names].filter((n) => !platforms.some((p) => p.name === n)).map((n) => ({ name: n, label: n }));
    return [...known, ...extra];
  }, [series, platforms]);
  const [platform, setPlatform] = useState("");
  const shown = present.some((p) => p.name === platform) ? platform : "";
  const days = useMemo(() => buildDays(series, shown), [series, shown]);
  const [hover, setHover] = useState<number | null>(null);

  const n = days.length;
  const peak = days.reduce((m, d) => Math.max(m, d.rate ?? 0), 0);
  const top = topPct(peak);
  const x = (i: number) => (n <= 1 ? 50 : (i / (n - 1)) * 100);
  const y = (r: number) => 100 - Math.min(1, (r * 100) / top) * 100;
  const path = linePath(days, x, y);
  const range = n ? fmtDayRange(days[0].day, days[n - 1].day) : "";
  const hovered = hover !== null ? days[hover] : undefined;
  const label = shown ? (present.find((p) => p.name === shown)?.label ?? shown) : "All platforms";

  const onMove = (e: MouseEvent<HTMLDivElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    if (n === 0 || r.width === 0) return;
    const i = Math.round(((e.clientX - r.left) / r.width) * (n - 1));
    setHover(Math.max(0, Math.min(n - 1, i)));
  };

  return (
    <section className="card pad chart-card" aria-labelledby="ban-h">
      <div className="card-head">
        <h2 className="card-title" id="ban-h">
          Ban rate, daily
        </h2>
        <span className="muted small">{range}</span>
      </div>
      {present.length > 1 && (
        <fieldset className="chips" aria-label="Platform">
          {[{ name: "", label: "All" }, ...present].map((p) => (
            <button
              key={p.name}
              type="button"
              className={`chip acc-chip${shown === p.name ? " is-on" : ""}`}
              aria-pressed={shown === p.name}
              onClick={() => setPlatform(p.name)}
            >
              {p.label}
            </button>
          ))}
        </fieldset>
      )}

      {n === 0 ? (
        <p className="muted small chart-empty">No accounts yet, so no ban rate.</p>
      ) : (
        <>
          <div
            className="chart acc-line-chart"
            role="img"
            aria-label={`${label}: share of dead accounts per day, ${range}. Peak ${pct(peak)}.`}
            onMouseMove={onMove}
            onMouseLeave={() => setHover(null)}
          >
            <div className="chart-grid" aria-hidden="true">
              <span className="grid-line" style={{ bottom: "100%" }}>
                <span className="grid-label">{top}%</span>
              </span>
              <span className="grid-line" style={{ bottom: "50%" }}>
                <span className="grid-label">{top / 2}%</span>
              </span>
              <span className="grid-line base" style={{ bottom: 0 }}>
                <span className="grid-label">0%</span>
              </span>
            </div>
            <svg className="acc-line" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
              <path d={path} vectorEffect="non-scaling-stroke" />
            </svg>
            {hovered && (
              <>
                <span className="acc-cross" style={{ left: `${x(hover as number)}%` }} aria-hidden="true" />
                {hovered.rate !== null && (
                  <span className="acc-marker" style={{ left: `${x(hover as number)}%`, top: `${y(hovered.rate)}%` }} aria-hidden="true" />
                )}
              </>
            )}
          </div>
          <div className="x-axis" aria-hidden="true">
            {tickIndexes(n).map((i) => (
              <span
                key={i}
                className="x-tick"
                style={{
                  left: `${x(i)}%`,
                  transform: tickTransform(i, n),
                }}
              >
                {fmtDay(days[i].day)}
              </span>
            ))}
          </div>
          <div className="chart-foot">
            <span className="muted small">{label} · dead = invalid or suspended</span>
            <p className="readout small" aria-live="polite">
              {hovered ? (
                <>
                  <span>{fmtDay(hovered.day, true)}</span> · <strong>{pct(hovered.rate)}</strong>{" "}
                  <span>
                    ({fmtNum(hovered.dead)} of {fmtNum(hovered.total)})
                  </span>
                </>
              ) : (
                <span className="muted">Hover the chart for details</span>
              )}
            </p>
          </div>
          <div className="sr-only">
            <table>
              <caption>{label}: dead accounts per day</caption>
              <thead>
                <tr>
                  <th scope="col">Day</th>
                  <th scope="col">Dead</th>
                  <th scope="col">Accounts</th>
                  <th scope="col">Ban rate</th>
                </tr>
              </thead>
              <tbody>
                {days.map((d) => (
                  <tr key={d.day}>
                    <th scope="row">{fmtDay(d.day, true)}</th>
                    <td>{d.dead}</td>
                    <td>{d.total}</td>
                    <td>{pct(d.rate)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  );
}
