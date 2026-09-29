import { useEffect, useId, useState, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import type { AsyncState } from "../hooks";
import type { SourceLookup } from "../sources";
import type { Meta, SourceInfo, SourceState, TrackMode, TrackResult, Tracking } from "../types";
import { ago, fmtDateTime, fmtNum } from "../format";
import { SourceIcon } from "./SourceBadge";
import { Loading } from "./Status";

interface Props {
  meta: AsyncState<Meta>;
  selectedQuery: string | null;
  tracking: AsyncState<Tracking | null>;
  projectId: number | null;
  projectName: string | null;
  lookup: SourceLookup;
  onScanned: (res: TrackResult) => void;
  onSetup: () => void;
  onListen: () => void;
}

const PAGES = 3;

type Tone = "ok" | "done" | "error" | "none" | "setup";

function tileStatus(s: SourceInfo, st: SourceState | undefined, scannedAt: string | null): { tone: Tone; text: string; title?: string } {
  if (st?.last_error) return { tone: "error", text: "last scan error", title: st.last_error };
  if (s.needs_config && !s.configured) return { tone: "setup", text: "needs setup" };
  if (st?.backfill_complete) return { tone: "done", text: "history complete" };
  if (st?.last_success_at) return { tone: "ok", text: `ok · ${ago(st.last_success_at)}`, title: fmtDateTime(st.last_success_at) };
  if (scannedAt) return { tone: "none", text: `scanned ${ago(scannedAt)}`, title: fmtDateTime(scannedAt) };
  return { tone: "none", text: "not scanned yet" };
}

function joinNames(a: string[]): string {
  return a.length < 2 ? a.join("") : `${a.slice(0, -1).join(", ")} and ${a[a.length - 1]}`;
}

export function ScanCard({ meta, selectedQuery, tracking, projectId, projectName, lookup, onScanned, onSetup, onListen }: Props) {
  const id = useId();
  const [keyword, setKeyword] = useState(selectedQuery ?? "");
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState<TrackMode | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<TrackResult | null>(null);

  // Follow the selected keyword.
  useEffect(() => {
    setKeyword(selectedQuery ?? "");
    setResult(null);
    setError(null);
  }, [selectedQuery]);

  // Pre-check the keyword's saved sources, falling back to the defaults.
  const saved = tracking.data && tracking.data.query === selectedQuery ? tracking.data.sources : null;
  const trackingSettled = selectedQuery === null || !tracking.loading || saved !== null;
  const defaults = meta.data?.default_sources;
  const savedKey = saved ? saved.join(",") : "";
  useEffect(() => {
    if (!trackingSettled) return;
    const base = saved && saved.length > 0 ? saved : defaults;
    if (base) setChecked(new Set(base));
    // savedKey captures `saved` contents
  }, [selectedQuery, savedKey, defaults, trackingSettled]);

  const toggle = (name: string) =>
    setChecked((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });

  const run = async (mode: TrackMode) => {
    const query = keyword.trim();
    if (!query) {
      setError("Enter a keyword to scan.");
      return;
    }
    if (checked.size === 0) {
      setError("Pick at least one source.");
      return;
    }
    setBusy(mode);
    setError(null);
    setResult(null);
    try {
      const sources = (meta.data?.sources ?? []).map((s) => s.name).filter((n) => checked.has(n));
      const res = await api.track({
        query,
        sources,
        mode,
        pages: PAGES,
        ...(projectId !== null ? { project_id: projectId } : {}),
      });
      setResult(res);
      onScanned(res);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    void run("incremental");
  };

  const all = meta.data?.sources ?? [];
  const states = new Map((saved !== null && tracking.data ? tracking.data.source_states : []).map((st) => [st.source, st]));
  // Tiles: sources that can scan now, plus any the keyword already uses. The rest live in Setup.
  const tiles = all.filter((s) => !s.needs_config || s.configured || checked.has(s.name) || states.has(s.name));
  const hidden = all.length - tiles.length;
  const picked = all.filter((s) => checked.has(s.name));
  const kw = keyword.trim();

  return (
    <section className="card scan" aria-labelledby={`${id}-h`}>
      <div className="scan-head">
        <span className="scan-mark" aria-hidden="true">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <path d="M19.07 4.93A10 10 0 1 0 22 12" />
            <path d="M16.24 7.76A6 6 0 1 0 18 12" />
            <path d="M12 12l6.5-6.5" />
            <circle cx="12" cy="12" r="1.6" fill="currentColor" />
          </svg>
        </span>
        <div className="scan-title">
          <h2 id={`${id}-h`} className="disp">
            Scan
          </h2>
          <p className="muted">Pull the latest mentions for a keyword from the sources you pick.</p>
        </div>
        <span className="read-only">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z" />
            <circle cx="12" cy="12" r="3" />
          </svg>
          Only reads — nothing gets posted
        </span>
      </div>

      <form className="scan-steps" onSubmit={onSubmit} aria-busy={busy !== null}>
        <div className="step-col step-kw">
          <label htmlFor={`${id}-kw`} className="step">
            <b>1</b>Keyword
          </label>
          <div className="search-field">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <circle cx="11" cy="11" r="7" />
              <path d="M20 20l-3.5-3.5" />
            </svg>
            <input
              id={`${id}-kw`}
              type="text"
              value={keyword}
              placeholder="Brand or topic"
              onChange={(e) => setKeyword(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              disabled={busy !== null}
            />
          </div>
          <p className="hint">
            {projectName ? (
              <>
                What to search for. New keywords join <strong>{projectName}</strong>.
              </>
            ) : (
              "What to search for on every source."
            )}
          </p>
        </div>

        <fieldset className="step-col step-src" disabled={busy !== null} aria-labelledby={`${id}-src`}>
          <div className="step-row">
            <span id={`${id}-src`} className="step">
              <b>2</b>Sources
            </span>
            <span className="hint">
              {picked.length} of {tiles.length} selected
            </span>
          </div>
          {meta.loading && !meta.data && <Loading label="Loading sources" />}
          <div className="tiles">
            {tiles.map((s) => {
              const on = checked.has(s.name);
              const st = tileStatus(s, states.get(s.name), saved?.includes(s.name) ? (tracking.data?.last_scanned_at ?? null) : null);
              return (
                <label key={s.name} className={`tile${on ? " is-on" : ""}`}>
                  <input type="checkbox" checked={on} onChange={() => toggle(s.name)} />
                  <SourceIcon source={s} size={24} />
                  <span className="tile-name">{s.label}</span>
                  <span className="tile-status" title={st.title}>
                    <span className={`dot tone-${st.tone}`} aria-hidden="true" />
                    {st.text}
                  </span>
                </label>
              );
            })}
          </div>
          {hidden > 0 && (
            <button type="button" className="link-btn quiet" onClick={onSetup}>
              + {hidden} more source{hidden === 1 ? "" : "s"} — set up in Setup
            </button>
          )}
        </fieldset>

        <div className="step-col step-run">
          <span className="step">
            <b>3</b>Run
          </span>
          <button type="submit" className="btn primary lg" disabled={busy !== null || !meta.data}>
            {busy === "incremental" ? <Spinner /> : <RefreshIcon />}
            {busy === "incremental" ? "Scanning…" : "Scan latest"}
          </button>
          <button type="button" className="btn" disabled={busy !== null || !meta.data} onClick={() => void run("backfill")}>
            {busy === "backfill" && <Spinner />}
            {busy === "backfill" ? "Backfilling…" : `Backfill ${PAGES} pages`}
          </button>
          <p className="hint">Backfill digs {PAGES} pages into older posts.</p>
        </div>
      </form>

      <div className="scan-status" role="status" aria-live="polite">
        {busy ? (
          <>
            <span className="prog" aria-hidden="true" />
            <span className="muted">
              Scanning “{kw}” on {picked.length} source{picked.length === 1 ? "" : "s"} — this can take a few seconds…
            </span>
          </>
        ) : error ? (
          <span className="neg strong">
            <AlertIcon />
            {error}
          </span>
        ) : result ? (
          <ScanResult result={result} lookup={lookup} onSetup={onSetup} onListen={onListen} />
        ) : (
          <span className="muted">
            <span className={`dot ${picked.length && kw ? "tone-ok" : "tone-setup"}`} aria-hidden="true" />
            {!kw
              ? "Type a keyword to scan."
              : picked.length
                ? `Ready to scan “${kw}” on ${joinNames(picked.map((s) => s.label))}.`
                : "Pick at least one source to scan."}
          </span>
        )}
      </div>
    </section>
  );
}

function ScanResult({ result, lookup, onSetup, onListen }: { result: TrackResult; lookup: SourceLookup; onSetup: () => void; onListen: () => void }) {
  const errors = Object.entries(result.errors ?? {});
  const extra: string[] = [];
  if (result.alerted > 0) extra.push(`${result.alerted} alert${result.alerted === 1 ? "" : "s"} sent`);
  if (result.alert_pending > 0) extra.push(`${result.alert_pending} alerts pending`);
  if (result.threshold_alerted > 0) extra.push(`${result.threshold_alerted} threshold alert${result.threshold_alerted === 1 ? "" : "s"}`);
  const warnings = [
    result.sentiment_error && `Sentiment: ${result.sentiment_error}`,
    result.analysis_error && `Themes: ${result.analysis_error}`,
    result.alert_error && `Alerts: ${result.alert_error}`,
  ].filter(Boolean) as string[];

  return (
    <div className="scan-result pop">
      <span className="strong">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--pos)" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M5 12.5l4.5 4.5L19 7" />
        </svg>
        {result.mode === "backfill" ? "Backfill" : "Scan"} done · {fmtNum(result.fetched)} fetched · {fmtNum(result.new)} new
        {extra.length > 0 && ` · ${extra.join(" · ")}`}
      </span>
      <button type="button" className="link-btn" onClick={onListen}>
        See mentions in Listen →
      </button>
      {errors.map(([name, msg]) => (
        <span key={name} className="neg">
          <SourceIcon source={lookup(name)} size={16} />
          {lookup(name).label}: {msg}
        </span>
      ))}
      {errors.length > 0 && (
        <button type="button" className="link-btn" onClick={onSetup}>
          Fix in Setup
        </button>
      )}
      {warnings.map((w) => (
        <span key={w} className="neg small">
          {w}
        </span>
      ))}
      {result.threshold_events?.map((ev, i) => (
        <span key={i} className="muted small">
          {ev}
        </span>
      ))}
    </div>
  );
}

function Spinner() {
  return (
    <svg className="spin" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
      <circle cx="12" cy="12" r="9" strokeOpacity="0.3" />
      <path d="M21 12a9 9 0 0 0-9-9" />
    </svg>
  );
}

function RefreshIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M21 12a9 9 0 1 1-2.64-6.36" />
      <path d="M21 4v5h-5" />
    </svg>
  );
}

function AlertIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7.5v5.5" />
      <path d="M12 16.5v.01" />
    </svg>
  );
}
