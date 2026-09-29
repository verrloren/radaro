import type { Theme } from "../types";
import { fmtNum } from "../format";

export function Themes({ themes }: { themes: Theme[] }) {
  const sorted = [...themes].sort((a, b) => b.count - a.count);
  const max = sorted.reduce((m, t) => Math.max(m, t.count), 0) || 1;
  return (
    <section className="card pad" aria-labelledby="themes-h">
      <div className="card-head">
        <h2 className="card-title" id="themes-h">
          Themes
        </h2>
        <span className="muted small">{sorted.length ? `${sorted.length} found` : ""}</span>
      </div>
      {sorted.length === 0 ? (
        <p className="muted small">No themes yet — they appear once enough mentions are analysed.</p>
      ) : (
        <ul className="theme-list" aria-label="Mentions per theme">
          {sorted.map((t, i) => (
            <li key={t.label} className={i === 0 ? "is-top" : ""}>
              <span className="theme-row">
                <span className="theme-label" title={t.label}>
                  {t.label}
                </span>
                {i === 0 && <span className="badge b-mari">most discussed</span>}
                <span className="num strong theme-count">{fmtNum(t.count)}</span>
              </span>
              <span className="track" aria-hidden="true">
                <span className="fill" style={{ width: `${(t.count / max) * 100}%` }} />
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
