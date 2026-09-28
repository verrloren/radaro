import type { SourceLookup } from "../sources";
import type { SummaryResponse } from "../types";
import { fmtNum } from "../format";
import { SourceBadge } from "./SourceBadge";

export function StatsRow({ data, lookup }: { data: SummaryResponse; lookup: SourceLookup }) {
  const sources = Object.entries(data.summary.by_source).sort((a, b) => b[1] - a[1]);
  const topTheme = data.themes.reduce<(typeof data.themes)[number] | null>(
    (best, t) => (best === null || t.count > best.count ? t : best),
    null,
  );
  const activeDays = Object.values(data.summary.by_day).filter((n) => n > 0).length;

  return (
    <div className="stats-row">
      <section className="panel stat stat-sources" aria-label="Mentions by source">
        <h2 className="stat-label">By source</h2>
        {sources.length === 0 ? (
          <p className="muted small">—</p>
        ) : (
          <div className="badge-row">
            {sources.map(([name, n]) => (
              <SourceBadge key={name} source={lookup(name)} count={n} showLabel />
            ))}
          </div>
        )}
      </section>
      <section className="panel stat" aria-label="Most discussed theme">
        <h2 className="stat-label">Most discussed</h2>
        {topTheme ? (
          <p className="stat-value">
            <span className="stat-theme">{topTheme.label}</span>
            <span className="muted mono small"> {fmtNum(topTheme.count)} mentions</span>
          </p>
        ) : (
          <p className="stat-value muted">—</p>
        )}
      </section>
      <section className="panel stat" aria-label="Active days">
        <h2 className="stat-label">Active days</h2>
        <p className="stat-value">
          <span className="mid-num">{fmtNum(activeDays)}</span>
          <span className="muted mono small"> with mentions</span>
        </p>
      </section>
    </div>
  );
}
