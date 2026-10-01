import { useId, useState, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import type { AccountView } from "../types";
import { ErrorLine } from "./Status";
export function AccountProxyForm({ account, onDone }: Readonly<{ account: AccountView; onDone: () => void }>) {
  const id = useId();
  const [value, setValue] = useState(""); const [busy, setBusy] = useState(false); const [error, setError] = useState<string | null>(null);
  const save = async (raw: string) => {
    setBusy(true); setError(null);
    try { await api.redditBrowserProxy(account.id, raw); onDone(); }
    catch (err) { setError(errorMessage(err)); setBusy(false); }
  };
  return <form className="setup-form" aria-label={`Proxy for ${account.handle}`} onSubmit={(e: FormEvent) => { e.preventDefault(); void save(value.trim()); }}>
    <p className="muted small">{account.browser?.proxy_configured ? "This account has a proxy." : "This account connects directly."} Changes apply to its next browser operation. Reddit may ask you to sign in again after a proxy change.</p>
    <div className="field"><label htmlFor={id} className="small strong">New account proxy</label><input id={id} type="password" value={value} onChange={(e) => setValue(e.target.value)} autoComplete="off" placeholder="http://user:password@host:port" disabled={busy} /></div>
    <ErrorLine error={error} />
    <div className="btn-row"><button className="btn primary sm" disabled={busy || !value.trim()}>Save proxy</button><button type="button" className="btn ghost sm" disabled={busy} onClick={() => void save("")}>Use direct connection</button></div>
  </form>;
}
