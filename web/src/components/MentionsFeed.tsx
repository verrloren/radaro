import { useState, type ReactNode } from "react";
import { RedditReplyDialog } from "./RedditReplyDialog";
import { api } from "../api";
import { useAsync } from "../hooks";
import type { SourceLookup } from "../sources";
import type { DraftDetail, Mention, Scope, Sentiment, Summary } from "../types";
import { SENTIMENTS } from "../types";
import { fmtDateTime, fmtNum, relTime } from "../format";
import { SourceIcon } from "./SourceBadge";
import { ErrorLine, Loading } from "./Status";

const LIMIT = 200;
const CLAMP_CHARS = 280;
const ARROW = { positive: "↗", neutral: "—", negative: "↘" } as const;

interface Props {
  scope: Scope;
  rev: number;
  summary: Summary | undefined;
  lookup: SourceLookup;
  source: string | null;
  onSource: (s: string | null) => void;
 projectId?: number;
 onDraft?: (d: DraftDetail)=>void;
}

export function MentionsFeed({ scope, rev, summary, lookup, source, onSource, projectId, onDraft }: Props) {
  const [reply,setReply]=useState<{mention:Mention;compose:boolean}|null>(null);
 const [sentiment, setSentiment] = useState<Sentiment | null>(null);

  const mentions = useAsync(
    (s) => api.mentions(scope, { sentiment: sentiment ?? undefined, source: source ?? undefined, limit: LIMIT }, s),
    [sentiment, source, rev],
  );

  const bySource = summary?.by_source ?? {};
  const present = Object.keys(bySource).sort((a, b) => bySource[b] - bySource[a]);
  const sourceChips = source && !present.includes(source) ? [...present, source] : present;
  const list = mentions.data ?? [];
  const total = summary?.total;

  return (
    <section className="feed-section" aria-labelledby="feed-h">
      <div className="section-head">
        <h2 className="section-title disp" id="feed-h">
          Mentions
        </h2>
        <span className="muted small" aria-live="polite">
          {mentions.loading && !mentions.data
            ? "loading…"
            : mentions.data
              ? `showing ${fmtNum(list.length)}${list.length >= LIMIT ? "+" : ""}${total !== undefined ? ` · ${fmtNum(total)} in total` : ""}`
              : ""}
        </span>
      </div>

      <div className="filters">
        <div className="chips" role="group" aria-label="Filter by sentiment">
          <Chip active={sentiment === null} onClick={() => setSentiment(null)} count={total}>
            All
          </Chip>
          {SENTIMENTS.map((s) => (
            <Chip key={s} active={sentiment === s} onClick={() => setSentiment(s)} count={summary?.by_sentiment[s] ?? 0}>
              <span className={`key key-${s}`} aria-hidden="true" />
              {s[0].toUpperCase() + s.slice(1)}
            </Chip>
          ))}
        </div>
        {sourceChips.length > 0 && (
          <>
            <span className="filters-sep" aria-hidden="true" />
            <div className="chips" role="group" aria-label="Filter by source">
              <Chip active={source === null} onClick={() => onSource(null)} count={total}>
                All sources
              </Chip>
              {sourceChips.map((name) => (
                <Chip key={name} active={source === name} onClick={() => onSource(name)} count={bySource[name] ?? 0}>
                  <SourceIcon source={lookup(name)} size={18} />
                  {lookup(name).label}
                </Chip>
              ))}
            </div>
          </>
        )}
      </div>

      <ErrorLine error={mentions.error} />
      {mentions.loading && !mentions.data && (
        <ul className="feed" aria-hidden="true">
          {[0, 1, 2].map((i) => (
            <li key={i} className="card mention skel-card">
              <span className="skel" style={{ width: "40%" }} />
              <span className="skel" style={{ width: "90%" }} />
              <span className="skel" style={{ width: "60%" }} />
            </li>
          ))}
        </ul>
      )}
      {mentions.loading && !mentions.data && <Loading label="Loading mentions" />}
      {mentions.data && list.length === 0 && !mentions.loading && (
        <div className="card pad empty-inline">
          <p>No mentions match these filters.</p>
          {(sentiment || source) && (
            <button
              type="button"
              className="btn sm"
              onClick={() => {
                setSentiment(null);
                onSource(null);
              }}
            >
              Clear filters
            </button>
          )}
        </div>
      )}

      <ul className={`feed${mentions.loading ? " is-stale" : ""}`}>
        {list.map((m) => (
          <MentionItem key={m.id} m={m} lookup={lookup} onReply={(compose)=>setReply({mention:m,compose})} />
        ))}
      </ul>
 {reply && <RedditReplyDialog mention={reply.mention} projectId={projectId} compose={reply.compose} onClose={()=>setReply(null)} onDraft={(d)=>{setReply(null);onDraft?.(d);}} />}
    </section>
  );
}

function Chip({ active, onClick, count, children }: { active: boolean; onClick: () => void; count?: number; children: ReactNode }) {
  return (
    <button type="button" className={`chip${active ? " is-on" : ""}`} aria-pressed={active} onClick={onClick}>
      {children}
      {count !== undefined && <span className="n">{fmtNum(count)}</span>}
    </button>
  );
}

function MentionItem({ m, lookup, onReply }: { m: Mention; lookup: SourceLookup; onReply:(compose:boolean)=>void }) {
  const [open, setOpen] = useState(false);
  const long = m.text.length > CLAMP_CHARS || m.text.split("\n").length > 5;
  const src = { ...lookup(m.source), name: m.source, ...(m.source_label ? { label: m.source_label } : {}) };
  const sent = m.sentiment ?? "unscored";

  return (
    <li className="card mention lift">
      <div className="mention-who">
        <span className="mention-src">
          <SourceIcon source={src} size={28} />
          {src.label}
        </span>
        <span className="muted mention-author">{m.author ?? "anonymous"}</span>
        <time className="muted small" dateTime={m.created_at} title={fmtDateTime(m.created_at)}>
          {relTime(m.created_at) === "now" ? "just now" : `${relTime(m.created_at)} ago`}
        </time>
      </div>
      <div className="mention-body">
        {m.title && <h3 className="mention-title disp">{m.title}</h3>}
        {m.text && <p className={`mention-text${long && !open ? " clamped" : ""}`}>{m.text}</p>}
        {long && (
          <button type="button" className="link-btn quiet" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
            {open ? "Show less" : "Show more"}
          </button>
        )}
        <div className="mention-meta">
          <span className={`sent tone-text-${sent}`}>
            {sent !== "unscored" && <span aria-hidden="true">{ARROW[sent]}</span>}
            {sent}
          </span>
          {m.reddit?.community && <span className="tag">r/{m.reddit.community}</span>}
 {m.reddit?.comments != null && <span className="small">{fmtNum(m.reddit.comments)} comments</span>}
 {m.reddit?.upvote_ratio != null && <span className="small">{Math.round(m.reddit.upvote_ratio*100)}% upvoted</span>}
          {m.theme ? <span className="tag">{m.theme}</span> : <span className="tag tag-empty">no theme</span>}
          {m.score !== null && (
            <span className="num score" title="Reddit score">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M6 15l6-6 6 6" />
              </svg>
              {m.source === "reddit" ? "Score: " : ""}{fmtNum(m.score)}
            </span>
          )}
        </div>
      </div>
      <div className="mention-actions">
 {m.source === "reddit" && m.url && <><button type="button" className="btn ghost sm" onClick={()=>onReply(false)}>Read post</button><button type="button" className="btn sm" onClick={()=>onReply(true)}>Reply</button></>}
        {m.url && (
          <a href={m.url} target="_blank" rel="noopener noreferrer" className="link">
            View source
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M14 4h6v6" />
              <path d="M20 4l-9 9" />
              <path d="M19 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h5" />
            </svg>
            <span className="sr-only"> (opens in a new tab)</span>
          </a>
        )}
      </div>
    </li>
  );
}
