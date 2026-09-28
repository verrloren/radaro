import type { SummaryResponse } from "../types";
import { SENTIMENTS } from "../types";
import { fmtNet, fmtNum } from "../format";

interface Props {
  title: string;
  subtitle: string;
  data: SummaryResponse;
  loading: boolean;
}

export function Overview({ title, subtitle, data, loading }: Props) {
  const { summary, net } = data;
  const counts = SENTIMENTS.map((s) => ({ s, n: summary.by_sentiment[s] ?? 0 }));
  const scored = counts.reduce((a, c) => a + c.n, 0);
  const unscored = Math.max(0, summary.total - scored);
  const denom = Math.max(1, scored + unscored);
  const netClass = net > 0.05 ? "pos" : net < -0.05 ? "neg" : "neu";
  const barLabel = counts.map((c) => `${fmtNum(c.n)} ${c.s}`).join(", ") + (unscored ? `, ${fmtNum(unscored)} unscored` : "");

  return (
    <section className="panel overview" aria-busy={loading} aria-label="Overview">
      <p className="eyebrow">{subtitle}</p>
      <h2 className="overview-title" title={title}>
        {title}
      </h2>
      <div className="overview-figures">
        <div>
          <div className="big-num">{fmtNum(summary.total)}</div>
          <div className="figure-label">mentions</div>
        </div>
        <div>
          <div className={`mid-num ${netClass}`}>{summary.total > 0 ? fmtNet(net) : "—"}</div>
          <div className="figure-label">net sentiment</div>
        </div>
      </div>

      <div className="sent-bar" role="img" aria-label={`Sentiment split: ${barLabel}`}>
        {counts.map(
          (c) =>
            c.n > 0 && (
              <span
                key={c.s}
                className={`seg seg-${c.s}`}
                style={{ width: `${(c.n / denom) * 100}%` }}
                title={`${c.s}: ${fmtNum(c.n)}`}
              />
            ),
        )}
        {unscored > 0 && (
          <span className="seg seg-unscored" style={{ width: `${(unscored / denom) * 100}%` }} title={`unscored: ${fmtNum(unscored)}`} />
        )}
      </div>
      <ul className="sent-legend">
        {counts.map((c) => (
          <li key={c.s}>
            <span className={`dot dot-${c.s}`} aria-hidden="true" />
            <span className="legend-name">{c.s}</span>
            <span className="mono">{fmtNum(c.n)}</span>
            <span className="muted mono">{summary.total > 0 ? `${Math.round((c.n / denom) * 100)}%` : ""}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}
