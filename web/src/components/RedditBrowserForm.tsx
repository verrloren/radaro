import { useCallback, useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent, type MouseEvent } from "react";
import { api, ApiError, errorMessage } from "../api";
import { useAsync } from "../hooks";
import type { BrowserInput, BrowserScreen } from "../types";
import { ErrorLine, Loading } from "./Status";

export function RedditBrowserForm({ projectId, onDone }: Readonly<{ projectId?: number; onDone?: () => void }>) {
  const id = useId();
  const available = useAsync((s) => api.redditBrowser(s), []);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [proxy, setProxy] = useState("");
  const [text, setText] = useState("");
  const [screen, setScreen] = useState<BrowserScreen | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const session = useRef<string | null>(null);
  const mounted = useRef(true);
  const pending = useRef(0);
  const queue = useRef(Promise.resolve());
  const finishing = useRef(false);
  const refreshFailed = useRef(false);
  const startController = useRef<AbortController | null>(null);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; startController.current?.abort(); if (session.current) void api.cancelRedditBrowser(session.current).catch(() => {}); };
  }, []);
  const finish = async (showError = true) => {
    if (!session.current) return;
    finishing.current = true;
    try {
      await api.finishRedditBrowser(session.current);
      session.current = null;
      if (mounted.current) { setScreen(null); onDone?.(); }
    } catch (err) {
      if (mounted.current && showError) setError(errorMessage(err));
      if (!(err instanceof ApiError && err.status === 409)) throw err;
    } finally { finishing.current = false; }
  };
  const start = async (e: FormEvent) => {
    e.preventDefault(); setBusy(true); setError(null);
    const ctrl = new AbortController(); startController.current = ctrl;
    try {
      const res = await api.startRedditBrowser({ username: username.trim(), password, proxy_url: proxy.trim(), project_id: projectId }, ctrl.signal);
      if (!mounted.current) { void api.cancelRedditBrowser(res.session_id).catch(() => {}); return; }
      session.current = res.session_id; refreshFailed.current = false; setPassword(""); setProxy(""); setScreen(res.screen);
      await finish(false);
    } catch (err) { if (mounted.current) setError(errorMessage(err)); }
    finally { if (mounted.current) setBusy(false); }
  };
  const input = useCallback((value: BrowserInput, background = false) => {
    const current = session.current;
    if (!mounted.current || !current || finishing.current) return;
    pending.current++;
    if (!background) { refreshFailed.current = false; setBusy(true); setError(null); }
    // Preserve every user action while keeping screenshots and inputs in order.
    queue.current = queue.current.then(async () => {
      if (!mounted.current || session.current !== current) return;
      try {
        const res = await api.redditBrowserInput(current, value);
        if (mounted.current && session.current === current) setScreen(res);
      } catch (err) {
        if (mounted.current && session.current === current) { refreshFailed.current = true; setError(errorMessage(err)); }
      }
    }).finally(() => {
      pending.current--;
      if (mounted.current && pending.current === 0 && !finishing.current) setBusy(false);
    });
    return queue.current;
  }, []);
  const hasScreen = screen !== null;
  useEffect(() => {
    if (!hasScreen || busy) return;
    const timer = window.setInterval(() => {
      if (!pending.current && !finishing.current && !refreshFailed.current) void input({ kind: "refresh" }, true);
    }, 1000);
    return () => window.clearInterval(timer);
  }, [hasScreen, busy, input]);
  const click = (e: MouseEvent<HTMLImageElement>) => {
    if (!screen) return;
    e.currentTarget.focus(); const rect = e.currentTarget.getBoundingClientRect();
    void input({ kind: "click", x: (e.clientX - rect.left) * screen.width / rect.width, y: (e.clientY - rect.top) * screen.height / rect.height });
  };
  const key = (e: KeyboardEvent<HTMLImageElement>) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    if (["Enter", "Tab", "Backspace", "Escape", "Delete", "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(e.key)) { e.preventDefault(); void input({ kind: "key", key: e.key }); }
  };
  if (available.loading && !available.data) return <Loading label="Checking browser connection" />;
  if (available.error) return <ErrorLine error={available.error} />;
  if (!available.data?.available) return <p className="muted small">Browser sign-in needs Chromium installed on the Radaro server.</p>;
  if (screen) return <div className="setup-form">
    <p className="muted small">Complete Reddit sign-in below, including CAPTCHA or two-factor verification if requested. Click inside the view to select a field or checkbox. This sign-in expires after 10 minutes.</p>
    <img className="reddit-browser-screen" src={`data:image/png;base64,${screen.image}`} width={screen.width} height={screen.height} alt="Reddit sign-in browser" tabIndex={0} onClick={click} onKeyDown={key} aria-label="Interactive Reddit sign-in" />
    <div className="field"><label htmlFor={`${id}-input`} className="small strong">Text or verification code for the selected Reddit field</label><div className="btn-row"><input id={`${id}-input`} type="password" value={text} onChange={(e) => setText(e.target.value)} autoComplete="off" disabled={busy} /><button type="button" className="btn sm" disabled={busy || !text} onClick={() => { void input({ kind: "text", text }); setText(""); }}>Enter text</button></div></div>
    <ErrorLine error={error} />
    <div className="btn-row">
      <button type="button" className="btn primary sm" disabled={busy} onClick={() => { setBusy(true); setError(null); void finish().catch((err) => setError(errorMessage(err))).finally(() => { if (mounted.current) setBusy(false); }); }}>Complete connection</button>
      <button type="button" className="btn ghost sm" disabled={busy} onClick={() => void input({ kind: "refresh" })}>Refresh view</button>
      <button type="button" className="btn ghost sm" disabled={busy} onClick={() => void input({ kind: "key", key: "Enter" })}>Press Enter</button>
      <button type="button" className="btn ghost sm" disabled={busy} onClick={() => void input({ kind: "scroll", delta: 400 })}>Scroll down</button>
      <button type="button" className="btn ghost sm" disabled={busy} onClick={() => void input({ kind: "scroll", delta: -400 })}>Scroll up</button>
      <button type="button" className="btn ghost sm" disabled={busy} onClick={() => { const old = session.current; session.current = null; if (old) void api.cancelRedditBrowser(old).catch(() => {}); setScreen(null); }}>Cancel sign-in</button>
    </div>
  </div>;
  return <form className="setup-form" onSubmit={(e) => void start(e)} aria-label="Reddit browser sign-in">
    <p className="muted small">Sign in with your Reddit account. Each account keeps its own browser session and optional proxy.</p>
    <div className="field"><label htmlFor={`${id}-user`} className="small strong">Reddit login or email</label><input id={`${id}-user`} type="text" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" disabled={busy} /></div>
    <div className="field"><label htmlFor={`${id}-pass`} className="small strong">Reddit password</label><input id={`${id}-pass`} type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" disabled={busy} /></div>
    <div className="field"><label htmlFor={`${id}-proxy`} className="small strong">Account proxy · optional</label><input id={`${id}-proxy`} type="password" value={proxy} onChange={(e) => setProxy(e.target.value)} placeholder="http://user:password@host:port or socks5://host:port" autoComplete="off" disabled={busy} /><p className="muted small">HTTP, HTTPS or SOCKS5, with optional username and password. Leave blank for a direct connection.</p></div>
    <ErrorLine error={error} />
    <button type="submit" className="btn primary sm" disabled={busy || !username.trim() || !password}>{busy ? "Signing in…" : "Connect Reddit"}</button>
  </form>;
}
