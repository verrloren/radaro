import { useId, useState, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import { useAsync } from "../hooks";
import type { Platform, SourceSettings } from "../types";
import { ErrorLine } from "./Status";
import { RedditBrowserForm } from "./RedditBrowserForm";

interface FieldSpec {
  key: "handle" | "instance" | "secret";
  label: string;
  secret?: boolean;
  placeholder?: string;
}

// How each platform connects; Reddit signs in through the browser instead.
const FORMS: Record<string, { help: string; fields: FieldSpec[] }> = {
  bluesky: {
    help: "Use an app password: Settings → Privacy and security → App passwords.",
    fields: [
      { key: "handle", label: "Handle or email", placeholder: "you.bsky.social" },
      { key: "secret", label: "App password", secret: true },
    ],
  },
  mastodon: {
    help: "Preferences → Development → New application with scopes read and write:statuses, then copy its access token.",
    fields: [
      { key: "instance", label: "Instance", placeholder: "mastodon.social" },
      { key: "secret", label: "Access token", secret: true },
    ],
  },
  devto: {
    help: "Settings → Extensions → DEV Community API Keys.",
    fields: [{ key: "secret", label: "API key", secret: true }],
  },
};

/** Keys for a source that only listens (RSS, X, YouTube). */
export function SourceForm({ source, onDone }: { source: SourceSettings; onDone: () => void }) {
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

/** Connects a Bluesky, Mastodon or Dev.to account; the key is checked with the platform. */
export function ConnectForm({ platform, projectId, onDone }: { platform: Platform; projectId?: number; onDone: () => void }) {
  const id = useId();
  const spec = FORMS[platform.name];
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (!spec) return null;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.connectAccount({
        platform: platform.name,
        handle: values.handle,
        instance: values.instance,
        secret: values.secret ?? "",
        project_id: projectId,
      });
      onDone();
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  return (
    <form className="setup-form" onSubmit={(e) => void submit(e)} aria-label={`Connect ${platform.label}`}>
      <p className="muted small">{spec.help}</p>
      {spec.fields.map((f) => (
        <div className="field" key={f.key}>
          <label htmlFor={`${id}-${f.key}`} className="small strong">
            {f.label}
          </label>
          <input
            id={`${id}-${f.key}`}
            type={f.secret ? "password" : "text"}
            value={values[f.key] ?? ""}
            placeholder={f.placeholder}
            onChange={(e) => setValues({ ...values, [f.key]: e.target.value })}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
          />
        </div>
      ))}
      <ErrorLine error={error} />
      <div className="btn-row">
        <button type="submit" className="btn primary sm" disabled={busy || !values.secret?.trim()}>
          {busy ? "Checking…" : "Connect"}
        </button>
      </div>
    </form>
  );
}

/** Starts the Reddit OAuth sign-in; Reddit sends the browser back to Setup. */
export function RedditForm({projectId,onDone}:{projectId?:number;onDone?:()=>void}) {
  const [mode,setMode]=useState<"browser"|"oauth">("browser");
  return <div><div className="btn-row"><button type="button" className={`btn sm ${mode==="browser"?"primary":"ghost"}`} onClick={()=>setMode("browser")}>Login and password</button><button type="button" className={`btn sm ${mode==="oauth"?"primary":"ghost"}`} onClick={()=>setMode("oauth")}>OAuth app</button></div>{mode==="browser"?<RedditBrowserForm projectId={projectId} onDone={onDone}/>:<RedditOAuthForm projectId={projectId}/>}</div>;
}
function RedditOAuthForm({ projectId }: { projectId?: number }) {
  const id = useId();
  const redirect = `${window.location.origin}/oauth/reddit/callback`;
  const app = useAsync((signal) => api.redditApp(signal), []);
  const [clientId, setClientId] = useState("");
  const [secret, setSecret] = useState("");
  const [authorizeUrl, setAuthorizeUrl] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const authorize = async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await api.redditAuthorize(app.data?.configured ? "" : clientId.trim(), app.data?.configured ? "" : secret.trim(), projectId);
      if (app.data?.configured) setAuthorizeUrl(res.authorize_url);
      else window.location.assign(res.authorize_url);
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    void authorize();
  };

  const copy = () => {
    void navigator.clipboard?.writeText(redirect).then(() => setCopied(true));
  };

  if (app.loading && !app.data) return <p className="muted small">Loading Reddit app…</p>;
  if (app.error && !app.data) return <ErrorLine error={app.error} />;

  if (app.data?.configured) {
    return (
      <div className="setup-form">
        <p className="muted small">The Reddit app is saved. To connect a different Reddit account, open the authorization link in a private window and sign in to that account there.</p>
        <ErrorLine error={error} />
        {authorizeUrl ? (
          <div className="field">
            <a className="link-btn" href={authorizeUrl} target="_blank" rel="noopener noreferrer">Open Reddit authorization</a>
            <p className="muted small">For a different account, copy this link and open it in a private window:</p>
            <input type="text" readOnly aria-label="Reddit authorization link" value={authorizeUrl} onFocus={(e) => e.currentTarget.select()} />
          </div>
        ) : (
          <button type="button" className="btn primary sm" disabled={busy} onClick={() => void authorize()}>
            {busy ? "Preparing link…" : "Add another Reddit account"}
          </button>
        )}
      </div>
    );
  }

  return (
    <form className="setup-form" onSubmit={(e) => void submit(e)} aria-label="Connect Reddit">
      <ol className="setup-steps small">
        <li>
          Create an app at{" "}
          <a className="link-btn" href="https://www.reddit.com/prefs/apps" target="_blank" rel="noreferrer">
            reddit.com/prefs/apps
          </a>{" "}
          — type “web app”.
        </li>
        <li>
          Set its redirect URI to <code className="code-chip">{redirect}</code>{" "}
          <button type="button" className="link-btn quiet" onClick={copy}>
            {copied ? "copied" : "copy"}
          </button>
        </li>
        <li>Paste the app’s client ID and secret, then approve access on Reddit.</li>
      </ol>
      <div className="field">
        <label htmlFor={`${id}-cid`} className="small strong">
          Client ID
        </label>
        <input id={`${id}-cid`} type="text" value={clientId} onChange={(e) => setClientId(e.target.value)} disabled={busy} autoComplete="off" spellCheck={false} />
      </div>
      <div className="field">
        <label htmlFor={`${id}-sec`} className="small strong">
          Client secret <span className="muted">· empty for an “installed app”</span>
        </label>
        <input id={`${id}-sec`} type="password" value={secret} onChange={(e) => setSecret(e.target.value)} disabled={busy} autoComplete="off" />
      </div>
      <ErrorLine error={error} />
      <div className="btn-row">
        <button type="submit" className="btn primary sm" disabled={busy || !clientId.trim()}>
          {busy ? "Opening Reddit…" : "Continue to Reddit"}
        </button>
      </div>
    </form>
  );
}
