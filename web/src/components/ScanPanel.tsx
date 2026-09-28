import { useEffect, useId, useState, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import type { AsyncState } from "../hooks";
import type { SourceLookup } from "../sources";
import type { Meta, TrackMode, TrackResult, Tracking } from "../types";
import { ago, fmtDateTime, fmtNum } from "../format";
import { SourceBadge } from "./SourceBadge";
import { ErrorLine, Loading } from "./Status";

interface Props {
  meta: AsyncState<Meta>;
  selectedQuery: string | null;
  tracking: AsyncState<Tracking | null>;
  projectId: number | null;
  projectName: string | null;
  lookup: SourceLookup;
  onScanned: (res: TrackResult) => void;
}

const PAGES = 3;

export function ScanPanel({ meta, selectedQuery, tracking, projectId, projectName, lookup, onScanned }: Props) {
  const inputId = useId();
  const [keyword, setKeyword] = useState(selectedQuery ?? "");
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState<TrackMode | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<TrackResult | null>(null);

  // Follow the selected keyword.
  useEffect(() => {
    setKeyword(selectedQuery ?? "");
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

  const states = saved !== null && tracking.data ? tracking.data.source_states : [];

  return (
    <section className="panel" aria-labelledby={`${inputId}-h`}>
      <h2 className="panel-title" id={`${inputId}-h`}>
        Scan
      </h2>
      <form onSubmit={onSubmit} className="scan-form" aria-busy={busy !== null}>
        <label className="field" htmlFor={inputId}>
          <span className="field-label">Keyword</span>
          <input
            id={inputId}
            type="text"
            value={keyword}
            placeholder='e.g. "arch linux"'
            onChange={(e) => setKeyword(e.target.value)}
            autoComplete="off"
            spellCheck={false}
            disabled={busy !== null}
          />
        </label>

        <fieldset className="sources" disabled={busy !== null}>
          <legend className="field-label">Sources</legend>
          {meta.loading && !meta.data && <Loading label="Loading sources" />}
          <ErrorLine error={meta.error} />
          <div className="source-grid">
            {(meta.data?.sources ?? []).map((s) => {
              const setup = s.needs_config && !s.configured;
              return (
                <label key={s.name} className={`source-check${setup ? " needs-setup" : ""}`}>
                  <input type="checkbox" checked={checked.has(s.name)} onChange={() => toggle(s.name)} />
                  <SourceBadge source={s} />
                  <span className="source-name">{s.label}</span>
                  {setup && (
                    <span className="setup-hint" title="Credentials or feeds are not configured for this source">
                      setup needed
                    </span>
                  )}
                </label>
              );
            })}
          </div>
        </fieldset>

        {projectName && (
          <p className="hint">
            Scans are added to project <strong>{projectName}</strong>.
          </p>
        )}

        <div className="btn-row">
          <button type="submit" className="btn primary" disabled={busy !== null || !meta.data}>
            {busy === "incremental" ? "Scanning…" : "Scan latest"}
          </button>
          <button
            type="button"
            className="btn"
            disabled={busy !== null || !meta.data}
            onClick={() => void run("backfill")}
          >
            {busy === "backfill" ? "Backfilling…" : `Backfill ${PAGES} pages`}
          </button>
        </div>
      </form>

      {busy && (
        <p className="loading-line" role="status">
          <span className="pulse" aria-hidden="true" /> Fetching from {checked.size} source{checked.size === 1 ? "" : "s"}
          {" "}— this can take a few seconds…
        </p>
      )}
      <ErrorLine error={error} />
      {result && <ScanResult result={result} lookup={lookup} />}

      {selectedQuery && (
        <div className="source-states">
          <h3 className="sub-title">
            Last scans
            {tracking.data?.last_scanned_at && (
              <span className="muted mono" title={fmtDateTime(tracking.data.last_scanned_at)}>
                {" "}
                · {ago(tracking.data.last_scanned_at)}
              </span>
            )}
          </h3>
          {tracking.loading && saved === null && <Loading label="Loading scan state" />}
          <ErrorLine error={tracking.error} />
          {!tracking.loading && tracking.data === null && <p className="muted small">Not tracked yet.</p>}
          {states.length > 0 && (
            <ul className="state-list">
              {states.map((st) => (
                <li key={st.source} className={st.last_error ? "has-error" : ""}>
                  <SourceBadge source={lookup(st.source)} />
                  <span className="state-text mono">
                    {st.last_error ? (
                      <span className="neg" title={st.last_error}>
                        error
                      </span>
                    ) : st.last_success_at ? (
                      <span title={fmtDateTime(st.last_success_at)}>ok {ago(st.last_success_at)}</span>
                    ) : (
                      <span className="muted">never</span>
                    )}
                    {st.backfill_complete && <span className="pill">full history</span>}
                  </span>
                  {st.last_error && <span className="state-err">{st.last_error}</span>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  );
}

function ScanResult({ result, lookup }: { result: TrackResult; lookup: SourceLookup }) {
  const errors = Object.entries(result.errors ?? {});
  const bySource = Object.entries(result.by_source ?? {});
  const alerts: string[] = [];
  if (result.alerted > 0) alerts.push(`${result.alerted} alert${result.alerted === 1 ? "" : "s"} sent`);
  if (result.alert_pending > 0) alerts.push(`${result.alert_pending} pending`);
  if (result.threshold_alerted > 0) alerts.push(`${result.threshold_alerted} threshold alert${result.threshold_alerted === 1 ? "" : "s"}`);
  if (result.threshold_pending > 0) alerts.push(`${result.threshold_pending} threshold pending`);

  return (
    <div className="scan-result" role="status">
      <p>
        <span className="mono">{result.mode}</span> · fetched <strong>{fmtNum(result.fetched)}</strong> ·{" "}
        <strong className={result.new > 0 ? "pos" : ""}>{fmtNum(result.new)}</strong> new
        {alerts.length > 0 && <> · {alerts.join(", ")}</>}
      </p>
      {bySource.length > 0 && (
        <div className="badge-row">
          {bySource.map(([name, n]) => (
            <SourceBadge key={name} source={lookup(name)} count={n} />
          ))}
        </div>
      )}
      {errors.length > 0 && (
        <ul className="result-errors">
          {errors.map(([name, msg]) => (
            <li key={name}>
              <SourceBadge source={lookup(name)} showLabel /> <span className="neg">{msg}</span>
            </li>
          ))}
        </ul>
      )}
      {result.sentiment_error && <p className="neg small">Sentiment: {result.sentiment_error}</p>}
      {result.analysis_error && <p className="neg small">Themes: {result.analysis_error}</p>}
      {result.alert_error && <p className="neg small">Alerts: {result.alert_error}</p>}
      {result.threshold_events?.length > 0 && (
        <ul className="small muted">
          {result.threshold_events.map((ev, i) => (
            <li key={i}>{ev}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
