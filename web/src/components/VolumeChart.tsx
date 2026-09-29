import { useMemo, useState } from "react";
import type { TimeseriesPoint } from "../types";
import { addDays, dayKey, daysBetween, fmtDay, fmtDayRange, fmtNum, parseDay } from "../format";

const MAX_BARS = 48;

interface Bucket {
  start: number;
  end: number;
  positive: number;
  neutral: number;
  negative: number;
  total: number;
}

/** Fills missing days with zeros; aggregates into equal N-day buckets when the range exceeds MAX_BARS days. */
function buildBuckets(points: TimeseriesPoint[]): { buckets: Bucket[]; size: number } {
  if (points.length === 0) return { buckets: [], size: 1 };
  const byDay = new Map<string, TimeseriesPoint>();
  let first = Infinity;
  let last = -Infinity;
  for (const p of points) {
    const t = parseDay(p.date);
    if (Number.isNaN(t)) continue;
    byDay.set(dayKey(t), p);
    first = Math.min(first, t);
    last = Math.max(last, t);
  }
  if (!Number.isFinite(first)) return { buckets: [], size: 1 };
  const days = daysBetween(first, last) + 1;
  const size = days > MAX_BARS ? Math.ceil(days / MAX_BARS) : 1;
  const count = Math.ceil(days / size);
  // Align buckets to end on the last day so every bucket spans exactly `size` days.
  const origin = addDays(last, -(count * size - 1));
  const buckets: Bucket[] = [];
  for (let b = 0; b < count; b++) {
    const start = addDays(origin, b * size);
    const bucket: Bucket = { start, end: addDays(start, size - 1), positive: 0, neutral: 0, negative: 0, total: 0 };
    for (let d = 0; d < size; d++) {
      const p = byDay.get(dayKey(addDays(start, d)));
      if (!p) continue;
      bucket.positive += p.positive;
      bucket.neutral += p.neutral;
      bucket.negative += p.negative;
      bucket.total += p.total;
    }
    buckets.push(bucket);
  }
  return { buckets, size };
}

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const mag = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 3, 4, 5, 10]) {
    if (m * mag >= v) return m * mag;
  }
  return 10 * mag;
}

function tickIndexes(n: number): number[] {
  if (n <= 1) return n === 1 ? [0] : [];
  const t = Math.min(n, n >= 20 ? 5 : n >= 8 ? 4 : 3);
  const out = new Set<number>();
  for (let k = 0; k < t; k++) out.add(Math.round((k * (n - 1)) / (t - 1)));
  return [...out];
}

function periodLabel(b: Bucket): string {
  return b.start === b.end ? fmtDay(b.start, true) : fmtDayRange(b.start, b.end);
}

function describe(b: Bucket): string {
  const unscored = b.total - b.positive - b.neutral - b.negative;
  return (
    `${periodLabel(b)}: ${fmtNum(b.total)} mentions — ${fmtNum(b.positive)} positive, ` +
    `${fmtNum(b.neutral)} neutral, ${fmtNum(b.negative)} negative` +
    (unscored > 0 ? `, ${fmtNum(unscored)} unscored` : "")
  );
}

export function VolumeChart({ points }: { points: TimeseriesPoint[] }) {
  const { buckets, size } = useMemo(() => buildBuckets(points), [points]);
  const [hover, setHover] = useState<number | null>(null);

  const peak = buckets.reduce((m, b) => Math.max(m, b.total), 0);
  const top = niceMax(peak);
  const ticks = tickIndexes(buckets.length);
  const range = buckets.length ? fmtDayRange(buckets[0].start, buckets[buckets.length - 1].end) : "";
  const hovered = hover !== null ? buckets[hover] : undefined;

  return (
    <section className="card pad chart-card" aria-labelledby="vol-h">
      <div className="card-head">
        <h2 className="card-title" id="vol-h">
          Volume and sentiment{size > 1 ? "" : ", daily"}
        </h2>
        <span className="muted small">
          {range}
          {size > 1 && ` · ${size}-day bars`}
        </span>
      </div>

      {buckets.length === 0 ? (
        <p className="muted small chart-empty">No mentions in this range yet.</p>
      ) : (
        <>
          <div
            className="chart"
            role="img"
            aria-label={`Mentions per ${size > 1 ? `${size}-day period` : "day"}, ${range}. Peak ${fmtNum(peak)}.`}
            onMouseLeave={() => setHover(null)}
          >
            <div className="chart-grid" aria-hidden="true">
              <span className="grid-line" style={{ bottom: "100%" }}>
                <span className="grid-label">{fmtNum(top)}</span>
              </span>
              <span className="grid-line" style={{ bottom: "50%" }}>
                <span className="grid-label">{fmtNum(top / 2)}</span>
              </span>
              <span className="grid-line base" style={{ bottom: 0 }} />
            </div>
            <div className="bars">
              {buckets.map((b, i) => {
                const unscored = Math.max(0, b.total - b.positive - b.neutral - b.negative);
                const h = (b.total / top) * 100;
                return (
                  <div
                    key={b.start}
                    className={`bar-col${hover === i ? " is-hover" : ""}`}
                    title={describe(b)}
                    onMouseEnter={() => setHover(i)}
                  >
                    {b.total > 0 && (
                      <div className="bar" style={{ height: `max(${h}%, 2px)` }}>
                        {b.negative > 0 && <span className="seg-negative" style={{ flexGrow: b.negative }} />}
                        {unscored > 0 && <span className="seg-unscored" style={{ flexGrow: unscored }} />}
                        {b.neutral > 0 && <span className="seg-neutral" style={{ flexGrow: b.neutral }} />}
                        {b.positive > 0 && <span className="seg-positive" style={{ flexGrow: b.positive }} />}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
          <div className="x-axis" aria-hidden="true">
            {ticks.map((i) => (
              <span
                key={i}
                className="x-tick"
                style={{
                  left: `${((i + 0.5) / buckets.length) * 100}%`,
                  transform: i === 0 ? "translateX(-0.5em)" : i === buckets.length - 1 ? "translateX(calc(-100% + 0.5em))" : "translateX(-50%)",
                }}
              >
                {fmtDay(buckets[i].start)}
              </span>
            ))}
          </div>
          <div className="chart-foot">
            <ul className="legend" aria-hidden="true">
              <li><span className="key key-positive" />positive</li>
              <li><span className="key key-neutral" />neutral</li>
              <li><span className="key key-negative" />negative</li>
            </ul>
          <p className="readout small" aria-live="polite">
            {hovered ? (
              <>
                <span>{periodLabel(hovered)}</span> · <strong>{fmtNum(hovered.total)}</strong>{" "}
                <span className="pos">▲{fmtNum(hovered.positive)}</span>{" "}
                <span className="neu">●{fmtNum(hovered.neutral)}</span>{" "}
                <span className="neg">▼{fmtNum(hovered.negative)}</span>
              </>
            ) : (
              <span className="muted">Hover a bar for details</span>
            )}
          </p>
          </div>
          {/* Tables ignore width/overflow, so the visually-hidden wrapper must be a block. */}
          <div className="sr-only">
          <table>
            <caption>Mentions per period</caption>
            <thead>
              <tr>
                <th scope="col">Period</th>
                <th scope="col">Total</th>
                <th scope="col">Positive</th>
                <th scope="col">Neutral</th>
                <th scope="col">Negative</th>
              </tr>
            </thead>
            <tbody>
              {buckets
                .filter((b) => b.total > 0)
                .map((b) => (
                  <tr key={b.start}>
                    <th scope="row">{periodLabel(b)}</th>
                    <td>{b.total}</td>
                    <td>{b.positive}</td>
                    <td>{b.neutral}</td>
                    <td>{b.negative}</td>
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
