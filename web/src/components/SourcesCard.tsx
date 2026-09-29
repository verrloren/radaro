import type { AsyncState } from "../hooks";
import type { SourceLookup } from "../sources";
import type { Meta, Tracking } from "../types";
import { ago, fmtDateTime } from "../format";
import { SourceIcon } from "./SourceBadge";
import { ErrorLine, Loading } from "./Status";

// How each source gets going; env names match .env.example.
const HOW: Record<string, { ready: string; setup?: string }> = {
  hackernews: { ready: "Public API — works out of the box, no key needed." },
  reddit: { ready: "Searches Reddit with your app credentials.", setup: "Set RADARO_REDDIT_CLIENT_ID and RADARO_REDDIT_CLIENT_SECRET (or an access token)." },
  mastodon: { ready: "Searches through your instance.", setup: "Set RADARO_MASTODON_ACCESS_TOKEN (and RADARO_MASTODON_INSTANCE)." },
  bluesky: { ready: "Public search — no login needed to listen." },
  rss: { ready: "Watches the feeds you listed.", setup: "Set RADARO_RSS_FEEDS to a comma-separated list of feed URLs." },
  stackoverflow: { ready: "Public API — anonymous quota, no key needed." },
  x: { ready: "Searches recent posts with your bearer token.", setup: "Set RADARO_X_BEARER_TOKEN from an X developer account." },
  youtube: { ready: "Searches videos with your API key.", setup: "Set RADARO_YOUTUBE_API_KEY." },
};

export function SourcesCard({ meta, tracking, lookup }: { meta: AsyncState<Meta>; tracking: AsyncState<Tracking | null> | undefined; lookup: SourceLookup }) {
  const states = new Map((tracking?.data?.source_states ?? []).map((st) => [st.source, st]));
  const q = tracking?.data?.query;
  const used = new Set(tracking?.data?.sources ?? []);
  const scannedAt = tracking?.data?.last_scanned_at ?? null;

  return (
    <section className="card pad" aria-labelledby="sources-h">
      <div className="card-head">
        <h2 id="sources-h" className="card-title lg">
          Sources
        </h2>
        <span className="muted small">{q ? `last scans for “${q}”` : "Scans run on this machine"}</span>
      </div>
      <p className="muted small">Credentials live in your environment or .env file — restart Radaro after changing them.</p>
      {meta.loading && !meta.data && <Loading label="Loading sources" />}
      <ErrorLine error={meta.error} />
      <ul className="source-list">
        {(meta.data?.sources ?? []).map((s) => {
          const ready = !s.needs_config || s.configured;
          const st = states.get(s.name);
          const how = HOW[s.name];
          return (
            <li key={s.name} className={`source-row${ready ? "" : " is-off"}`}>
              <SourceIcon source={lookup(s.name)} size={28} />
              <span className="source-text">
                <span className="strong">{s.label}</span>
                <span className="muted small">{how ? (ready ? how.ready : (how.setup ?? how.ready)) : ready ? "Ready to scan." : "Needs credentials."}</span>
              </span>
              <span className={`badge ${ready ? "b-ok" : "b-mari"}`}>{ready ? "configured" : "needs setup"}</span>
              {q && used.has(s.name) && (
                <span className="source-last small" title={st?.last_error ?? fmtDateTime(st?.last_success_at ?? scannedAt)}>
                  <span className={`dot ${st?.last_error ? "tone-error" : st?.last_success_at ? "tone-ok" : "tone-none"}`} aria-hidden="true" />
                  {st?.last_error
                    ? `last scan error: ${st.last_error}`
                    : st?.last_success_at
                      ? `ok · ${ago(st.last_success_at)}`
                      : scannedAt
                        ? `scanned ${ago(scannedAt)}`
                        : "not scanned yet"}
                </span>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
