import type { SourceLookup } from "../sources";
import type { SummaryResponse } from "../types";
import { SENTIMENTS } from "../types";
import { fmtNum } from "../format";
import { SourceIcon } from "./SourceBadge";

interface Props {
  data: SummaryResponse;
  lookup: SourceLookup;
  source: string | null;
  onSource: (s: string | null) => void;
}

const ARROW = { positive: "↗", neutral: "—", negative: "↘" } as const;

export function SentimentCard({ data, lookup, source, onSource }: Props) {
  const { summary } = data;
  const counts = SENTIMENTS.map((s) => ({ s, n: summary.by_sentiment[s] ?? 0 }));
  const scored = counts.reduce((a, c) => a + c.n, 0);
  const unscored = Math.max(0, summary.total - scored);
  const denom = Math.max(1, scored + unscored);
  const barLabel = counts.map((c) => `${fmtNum(c.n)} ${c.s}`).join(", ") + (unscored ? `, ${fmtNum(unscored)} unscored` : "");
  const sources = Object.entries(summary.by_source).sort((a, b) => b[1] - a[1]);
  const max = sources.reduce((m, [, n]) => Math.max(m, n), 0) || 1;

  return (
    <section className="card pad" aria-labelledby="split-h">
      <h2 id="split-h" className="card-title">
        Sentiment split
      </h2>
      <div className="split-bar" role="img" aria-label={`Sentiment split: ${barLabel}`}>
        {counts.map((c) => c.n > 0 && <span key={c.s} className={`seg seg-${c.s}`} style={{ flexGrow: c.n }} title={`${c.s}: ${fmtNum(c.n)}`} />)}
        {unscored > 0 && <span className="seg seg-unscored" style={{ flexGrow: unscored }} title={`unscored: ${fmtNum(unscored)}`} />}
      </div>
      <ul className="split-legend">
        {counts.map((c) => (
          <li key={c.s}>
            <span className={`key key-${c.s}`} aria-hidden="true" />
            <span className={`legend-name tone-text-${c.s}`}>
              <span aria-hidden="true">{ARROW[c.s]}</span> {c.s}
            </span>
            <span className="num strong">{fmtNum(c.n)}</span>
            <span className="num muted">{summary.total > 0 ? `${Math.round((c.n / denom) * 100)}%` : ""}</span>
          </li>
        ))}
      </ul>

      {sources.length > 0 && (
        <div className="by-source">
          <h3 className="sub-title">Mentions by source · tap to filter</h3>
          {sources.map(([name, n]) => {
            const on = source === name;
            const s = lookup(name);
            return (
              <button key={name} type="button" className={`srow${on ? " is-on" : ""}`} aria-pressed={on} onClick={() => onSource(on ? null : name)}>
                <span className="srow-name">
                  <SourceIcon source={s} size={16} />
                  {s.label}
                </span>
                <span className="track" aria-hidden="true">
                  <span className="fill" style={{ width: `${(n / max) * 100}%` }} />
                </span>
                <span className="num strong">{fmtNum(n)}</span>
              </button>
            );
          })}
        </div>
      )}
    </section>
  );
}
