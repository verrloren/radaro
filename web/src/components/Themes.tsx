import type { Theme } from "../types";
import { fmtNum } from "../format";

export function Themes({ themes }: { themes: Theme[] }) {
  const sorted = [...themes].sort((a, b) => b.count - a.count);
  const max = sorted.reduce((m, t) => Math.max(m, t.count), 0) || 1;
  return (
    <section className="panel" aria-labelledby="themes-h">
      <h2 className="panel-title" id="themes-h">
        Themes
      </h2>
      {sorted.length === 0 ? (
        <p className="muted small">No themes yet — they appear once enough mentions are analysed.</p>
      ) : (
        <ul className="theme-list" aria-label="Mentions per theme">
          {sorted.map((t) => (
            <li key={t.label}>
              <span className="theme-label" title={t.label}>
                {t.label}
              </span>
              <span className="theme-track" aria-hidden="true">
                <span className="theme-fill" style={{ width: `${(t.count / max) * 100}%` }} />
              </span>
              <span className="theme-count mono">{fmtNum(t.count)}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
