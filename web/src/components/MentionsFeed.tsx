import { useState, type ReactNode } from "react";
import { api } from "../api";
import { useAsync } from "../hooks";
import type { SourceLookup } from "../sources";
import type { Mention, Scope, Sentiment } from "../types";
import { SENTIMENTS } from "../types";
import { fmtDateTime, fmtNum, relTime } from "../format";
import { SourceBadge } from "./SourceBadge";
import { ErrorLine, Loading } from "./Status";

const LIMIT = 200;
const CLAMP_CHARS = 280;

interface Props {
  scope: Scope;
  rev: number;
  sourcesPresent: string[];
  lookup: SourceLookup;
}

export function MentionsFeed({ scope, rev, sourcesPresent, lookup }: Props) {
  const [sentiment, setSentiment] = useState<Sentiment | null>(null);
  const [source, setSource] = useState<string | null>(null);

  const mentions = useAsync(
    (s) => api.mentions(scope, { sentiment: sentiment ?? undefined, source: source ?? undefined, limit: LIMIT }, s),
    [sentiment, source, rev],
  );

  const sourceChips = source && !sourcesPresent.includes(source) ? [...sourcesPresent, source] : sourcesPresent;
  const list = mentions.data ?? [];

  return (
    <section className="panel" aria-labelledby="feed-h">
      <div className="panel-head">
        <h2 className="panel-title" id="feed-h">
          Mentions
        </h2>
        <span className="muted mono small" aria-live="polite">
          {mentions.loading ? "loading…" : mentions.data ? `${fmtNum(list.length)}${list.length >= LIMIT ? "+" : ""} shown` : ""}
        </span>
      </div>

      <div className="chips" role="group" aria-label="Filter by sentiment">
        <Chip active={sentiment === null} onClick={() => setSentiment(null)}>
          all
        </Chip>
        {SENTIMENTS.map((s) => (
          <Chip key={s} active={sentiment === s} onClick={() => setSentiment(s)} tone={s}>
            {s}
          </Chip>
        ))}
      </div>
      {sourceChips.length > 0 && (
        <div className="chips" role="group" aria-label="Filter by source">
          <Chip active={source === null} onClick={() => setSource(null)}>
            all sources
          </Chip>
          {sourceChips.map((name) => (
            <Chip key={name} active={source === name} onClick={() => setSource(name)}>
              <SourceBadge source={lookup(name)} showLabel />
            </Chip>
          ))}
        </div>
      )}

      <ErrorLine error={mentions.error} />
      {mentions.loading && !mentions.data && <Loading label="Loading mentions" />}
      {mentions.data && list.length === 0 && !mentions.loading && (
        <p className="muted small">No mentions match these filters.</p>
      )}

      <ol className={`feed${mentions.loading ? " is-stale" : ""}`}>
        {list.map((m) => (
          <MentionItem key={m.id} m={m} lookup={lookup} />
        ))}
      </ol>
    </section>
  );
}

function Chip({
  active,
  onClick,
  tone,
  children,
}: {
  active: boolean;
  onClick: () => void;
  tone?: Sentiment;
  children: ReactNode;
}) {
  return (
    <button type="button" className={`chip${tone ? ` chip-${tone}` : ""}`} aria-pressed={active} onClick={onClick}>
      {children}
    </button>
  );
}

function MentionItem({ m, lookup }: { m: Mention; lookup: SourceLookup }) {
  const [open, setOpen] = useState(false);
  const long = m.text.length > CLAMP_CHARS || m.text.split("\n").length > 5;
  const src = m.glyph && m.color ? { label: m.source_label || m.source, glyph: m.glyph, color: m.color } : lookup(m.source);
  const sent = m.sentiment ?? "unscored";

  return (
    <li className={`mention sent-${sent}`}>
      <div className="mention-meta">
        <SourceBadge source={src} />
        <span className="author">{m.author ?? "anonymous"}</span>
        <time className="mono muted" dateTime={m.created_at} title={fmtDateTime(m.created_at)}>
          {relTime(m.created_at)}
        </time>
        {m.score !== null && (
          <span className="mono score" title="Score">
            ▲ {fmtNum(m.score)}
          </span>
        )}
        <span className={`tag tag-${sent}`}>{sent}</span>
        {m.theme && <span className="tag tag-theme">{m.theme}</span>}
      </div>
      {m.title && <h3 className="mention-title">{m.title}</h3>}
      {m.text && <p className={`mention-text${long && !open ? " clamped" : ""}`}>{m.text}</p>}
      <div className="mention-actions">
        {long && (
          <button type="button" className="link-btn" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
            {open ? "show less" : "show more"}
          </button>
        )}
        {m.url && (
          <a href={m.url} target="_blank" rel="noopener noreferrer" className="source-link">
            view source ↗<span className="sr-only"> (opens in a new tab)</span>
          </a>
        )}
      </div>
    </li>
  );
}
