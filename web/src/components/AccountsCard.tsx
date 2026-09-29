import { useId, useState, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import type { AsyncState } from "../hooks";
import type { SourceLookup } from "../sources";
import type { AccountsResponse, Platform } from "../types";
import { SourceIcon } from "./SourceBadge";
import { ErrorLine, Loading } from "./Status";

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

interface Props {
  accounts: AsyncState<AccountsResponse>;
  lookup: SourceLookup;
  notice: { ok: boolean; text: string } | null;
  onChanged: () => void;
}

export function AccountsCard({ accounts, lookup, notice, onChanged }: Props) {
  const [open, setOpen] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const platforms = accounts.data?.platforms ?? [];
  const list = accounts.data?.accounts ?? [];
  const icon = (name: string) => (name === "devto" ? { name, label: "Dev.to", glyph: "D", color: "#3b49df" } : lookup(name));

  const disconnect = async (id: number) => {
    setBusy(true);
    setError(null);
    try {
      await api.deleteAccount(id);
      setConfirm(null);
      onChanged();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="card pad" aria-labelledby="accounts-h">
      <div className="card-head">
        <h2 id="accounts-h" className="card-title lg">
          Publishing accounts
        </h2>
        <span className="muted small">Where approved drafts are posted</span>
      </div>
      <p className="muted small">Credentials are checked with the platform, then stored in the local database. Nothing is posted without your approval.</p>
      {notice && (notice.ok ? <p className="ok-line" role="status">✓ {notice.text}</p> : <ErrorLine error={notice.text} />)}
      {accounts.loading && !accounts.data && <Loading label="Loading accounts" />}
      <ErrorLine error={accounts.error ?? error} />
      <ul className="source-list">
        {platforms.map((p) => {
          const connected = list.filter((a) => a.platform === p.name);
          const isOpen = open === p.name;
          return (
            <li key={p.name} className={`source-row${connected.length ? "" : " is-off"}`}>
              <SourceIcon source={icon(p.name)} size={28} />
              <span className="source-text">
                <span className="strong">{p.label}</span>
                {connected.length === 0 ? (
                  <span className="muted small">Not connected.</span>
                ) : (
                  connected.map((a) => (
                    <span key={a.id} className="account-line small">
                      <span>{a.handle}</span>
                      {confirm === a.id ? (
                        <>
                          <button type="button" className="link-btn danger" disabled={busy} onClick={() => void disconnect(a.id)}>
                            Disconnect {a.handle}?
                          </button>
                          <button type="button" className="link-btn quiet" disabled={busy} onClick={() => setConfirm(null)}>
                            Keep
                          </button>
                        </>
                      ) : (
                        <button type="button" className="link-btn quiet" onClick={() => setConfirm(a.id)}>
                          Disconnect
                        </button>
                      )}
                    </span>
                  ))
                )}
              </span>
              <span className="source-actions">
                {connected.length > 0 && <span className="badge b-ok">connected</span>}
                <button type="button" className="btn sm" aria-expanded={isOpen} onClick={() => setOpen(isOpen ? null : p.name)}>
                  {isOpen ? "Close" : connected.length ? "Add account" : "Connect"}
                </button>
              </span>
              {isOpen &&
                (p.name === "reddit" ? (
                  <RedditForm />
                ) : (
                  <ConnectForm
                    platform={p}
                    onDone={() => {
                      setOpen(null);
                      onChanged();
                    }}
                  />
                ))}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function ConnectForm({ platform, onDone }: { platform: Platform; onDone: () => void }) {
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
      await api.connectAccount({ platform: platform.name, handle: values.handle, instance: values.instance, secret: values.secret ?? "" });
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

function RedditForm() {
  const id = useId();
  const redirect = `${window.location.origin}/oauth/reddit/callback`;
  const [clientId, setClientId] = useState("");
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await api.redditAuthorize(clientId.trim(), secret.trim());
      window.location.assign(res.authorize_url);
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  const copy = () => {
    void navigator.clipboard?.writeText(redirect).then(() => setCopied(true));
  };

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
