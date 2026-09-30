import { useId, useState, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import { useAsync } from "../hooks";
import { ErrorLine, Loading } from "./Status";

/** Server-wide outbound proxy; the saved URL and credentials are never read back. */
export function ProxySettingsForm({ onChanged }: Readonly<{ onChanged: () => void }>) {
  const id = useId();
  const [rev, setRev] = useState(0);
  const config = useAsync((signal) => api.proxySettings(signal), [rev]);
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      setUrl("");
      setRev((n) => n + 1);
      onChanged();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (url.trim()) void run(() => api.saveProxy(url.trim()));
  };

  const origin = config.data?.origin;
  const state = origin === "ui" ? "saved in Radaro" : origin === "env" ? "from environment" : "not configured";

  return (
    <section className="card pad" aria-labelledby={`${id}-h`}>
      <div className="card-head">
        <h2 className="card-title lg" id={`${id}-h`}>Outbound proxy</h2>
        {config.data && <span className="muted small">{state}</span>}
      </div>
      <p className="muted small">One proxy for this server’s outgoing requests. Enter a new URL to replace it; saved credentials stay hidden.</p>
      {config.loading && !config.data && <Loading label="Loading proxy settings" />}
      <ErrorLine error={config.error ?? error} onRetry={() => setRev((n) => n + 1)} />
      <form className="setup-form" onSubmit={submit} aria-label="Outbound proxy settings">
        <div className="field">
          <label htmlFor={`${id}-url`} className="small strong">Proxy URL</label>
          <input id={`${id}-url`} type="password" value={url} onChange={(e) => setUrl(e.target.value)} placeholder={config.data?.configured ? "Configured — enter a replacement URL" : "http://user:password@proxy.example:8080"} autoComplete="off" spellCheck={false} disabled={busy} />
        </div>
        <div className="btn-row">
          <button type="submit" className="btn primary sm" disabled={busy || !url.trim()}>{busy ? "Saving…" : "Save proxy"}</button>
          {origin === "ui" && <button type="button" className="btn ghost danger sm" disabled={busy} onClick={() => void run(() => api.resetProxy())}>Remove saved proxy</button>}
        </div>
      </form>
    </section>
  );
}
