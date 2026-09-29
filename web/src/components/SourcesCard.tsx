import { useId, useState, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import type { AsyncState } from "../hooks";
import type { SourceLookup } from "../sources";
import type { Meta, SourceSettings, Tracking } from "../types";
import { ago, fmtDateTime } from "../format";
import { SourceIcon } from "./SourceBadge";
import { ErrorLine, Loading } from "./Status";

// What each source does once ready, and where its keys come from.
const HOW: Record<string, { ready: string; setup?: string }> = {
  hackernews: { ready: "Public API — works out of the box, no key needed." },
  reddit: { ready: "Searches Reddit with your app credentials.", setup: "Create a “script” app at reddit.com/prefs/apps and paste its client ID and secret." },
  mastodon: { ready: "Searches through your instance.", setup: "Preferences → Development → New application (scope read), then paste the access token." },
  bluesky: { ready: "Public search — no login needed to listen." },
  rss: { ready: "Watches the feeds you listed.", setup: "Add the feed URLs to watch." },
  stackoverflow: { ready: "Public API — anonymous quota, no key needed." },
  x: { ready: "Searches recent posts with your bearer token.", setup: "Paste a bearer token from an X developer account (paid API)." },
  youtube: { ready: "Searches videos with your API key.", setup: "Paste a Google Cloud API key with the YouTube Data API v3 enabled." },
};

interface Props {
  meta: AsyncState<Meta>;
  settings: AsyncState<SourceSettings[]>;
  tracking: AsyncState<Tracking | null> | undefined;
  lookup: SourceLookup;
  onChanged: () => void;
}

export function SourcesCard({ meta, settings, tracking, lookup, onChanged }: Props) {
  const [open, setOpen] = useState<string | null>(null);
  const states = new Map((tracking?.data?.source_states ?? []).map((st) => [st.source, st]));
  const q = tracking?.data?.query;
  const used = new Set(tracking?.data?.sources ?? []);
  const scannedAt = tracking?.data?.last_scanned_at ?? null;
  const bySource = new Map((settings.data ?? []).map((s) => [s.name, s]));

  return (
    <section className="card pad" aria-labelledby="sources-h">
      <div className="card-head">
        <h2 id="sources-h" className="card-title lg">
          Sources
        </h2>
        <span className="muted small">{q ? `last scans for “${q}”` : "Scans run on this machine"}</span>
      </div>
      <p className="muted small">Keys you save here stay in the local database and apply right away. Values in .env still work as a fallback.</p>
      {meta.loading && !meta.data && <Loading label="Loading sources" />}
      <ErrorLine error={meta.error ?? settings.error} />
      <ul className="source-list">
        {(meta.data?.sources ?? []).map((s) => {
          const ready = !s.needs_config || s.configured;
          const st = states.get(s.name);
          const how = HOW[s.name];
          const cfg = bySource.get(s.name);
          const isOpen = open === s.name && cfg !== undefined;
          return (
            <li key={s.name} className={`source-row${ready ? "" : " is-off"}`}>
              <SourceIcon source={lookup(s.name)} size={28} />
              <span className="source-text">
                <span className="strong">
                  {s.label}
                  {cfg?.saved && <span className="muted small"> · saved in Radaro</span>}
                  {cfg && !cfg.saved && cfg.configured && <span className="muted small"> · from .env</span>}
                </span>
                <span className="muted small">{how ? (ready ? how.ready : (how.setup ?? how.ready)) : ready ? "Ready to scan." : "Needs credentials."}</span>
              </span>
              <span className="source-actions">
                <span className={`badge ${ready ? "b-ok" : "b-mari"}`}>{ready ? "configured" : "needs setup"}</span>
                {cfg && (
                  <button type="button" className="btn sm" aria-expanded={isOpen} onClick={() => setOpen(isOpen ? null : s.name)}>
                    {isOpen ? "Close" : ready ? "Edit" : "Set up"}
                  </button>
                )}
              </span>
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
              {isOpen && (
                <SourceForm
                  key={cfg.name}
                  source={cfg}
                  onDone={() => {
                    setOpen(null);
                    onChanged();
                  }}
                />
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function SourceForm({ source, onDone }: { source: SourceSettings; onDone: () => void }) {
  const id = useId();
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(source.fields.map((f) => [f.key, f.secret ? "" : (f.value ?? "")])),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      onDone();
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    void run(() => api.saveSourceSettings(source.name, values));
  };

  return (
    <form className="setup-form" onSubmit={submit} aria-label={`${source.label} settings`}>
      {source.fields.map((f) => {
        const fid = `${id}-${f.key}`;
        const note = f.origin === "ui" ? "saved" : f.origin === "env" ? `from ${f.env}` : "";
        const placeholder = f.secret && f.set ? "•••••••• — leave blank to keep" : f.placeholder;
        return (
          <div className="field" key={f.key}>
            <label htmlFor={fid} className="small strong">
              {f.label}
              {note && <span className="muted"> · {note}</span>}
            </label>
            {f.multiline ? (
              <textarea
                id={fid}
                rows={3}
                value={values[f.key] ?? ""}
                placeholder={placeholder}
                onChange={(e) => setValues({ ...values, [f.key]: e.target.value })}
                disabled={busy}
                spellCheck={false}
              />
            ) : (
              <input
                id={fid}
                type={f.secret ? "password" : "text"}
                value={values[f.key] ?? ""}
                placeholder={placeholder}
                onChange={(e) => setValues({ ...values, [f.key]: e.target.value })}
                disabled={busy}
                autoComplete="off"
                spellCheck={false}
              />
            )}
          </div>
        );
      })}
      <ErrorLine error={error} />
      <div className="btn-row">
        <button type="submit" className="btn primary sm" disabled={busy}>
          Save
        </button>
        {source.saved && (
          <button type="button" className="btn ghost danger sm" disabled={busy} onClick={() => void run(() => api.resetSourceSettings(source.name))}>
            Remove saved keys
          </button>
        )}
      </div>
    </form>
  );
}
