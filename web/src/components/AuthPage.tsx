import { useEffect, useId, useState, type FormEvent, type MouseEvent } from "react";
import { api, ApiError, errorMessage } from "../api";
import type { User } from "../types";
import { navigate } from "../router";
import { ErrorLine } from "./Status";

interface Props {
  mode: "login" | "register";
  serverError?: string;
  onSignedIn: (user: User) => void;
}

export function AuthPage({ mode, serverError, onSignedIn }: Props) {
  const id = useId();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(serverError ?? null);
  const [open, setOpen] = useState<boolean | null>(null);
  const register = mode === "register";

  useEffect(() => {
    setError(serverError ?? null);
    setRepeat("");
    const ctrl = new AbortController();
    api.authConfig(ctrl.signal).then(
      (c) => setOpen(c.registration_open),
      () => setOpen(null),
    );
    return () => ctrl.abort();
  }, [mode, serverError]);

  const mismatch = register && repeat !== "" && repeat !== password;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (register && password !== repeat) {
      setError("The passwords do not match.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = register ? await api.register(email, password) : await api.login(email, password);
      onSignedIn(res.user);
    } catch (err) {
      setError(err instanceof ApiError && err.status === 429 ? "Too many attempts. Wait a minute and try again." : errorMessage(err));
      setBusy(false);
    }
  };

  const switchTo = (to: "/login" | "/register") => (e: MouseEvent) => {
    e.preventDefault();
    navigate(`${to}${window.location.search}`);
  };

  return (
    <main className="auth-shell">
      <section className="auth-card card" aria-labelledby={`${id}-h`}>
        <div className="brand">
          <span className="wordmark">Radaro</span>
          <span className="tri" aria-hidden="true">
            <i />
            <i />
            <i />
          </span>
        </div>
        <header className="auth-head">
          <h1 className="card-title lg" id={`${id}-h`}>
            {register ? "Create your account" : "Sign in"}
          </h1>
          <p className="muted small">
            {register
              ? open === false
                ? "Registration is closed on this server. Ask its admin for an account."
                : "Your projects, keywords and connected accounts are visible only to you."
              : "Hear what the internet says about your projects."}
          </p>
        </header>

        {!(register && open === false) && (
          <form className="setup-form" onSubmit={(e) => void submit(e)} aria-label={register ? "Create account" : "Sign in"}>
            <div className="field">
              <label htmlFor={`${id}-email`} className="small strong">
                Email
              </label>
              <input
                id={`${id}-email`}
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                disabled={busy}
                autoComplete={register ? "email" : "username"}
                autoFocus
                required
              />
            </div>
            <div className="field">
              <label htmlFor={`${id}-pw`} className="small strong">
                Password {register && <span className="muted">· at least 8 characters</span>}
              </label>
              <input
                id={`${id}-pw`}
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={busy}
                autoComplete={register ? "new-password" : "current-password"}
                minLength={register ? 8 : undefined}
                required
              />
            </div>
            {register && (
              <div className="field">
                <label htmlFor={`${id}-pw2`} className="small strong">
                  Repeat password
                </label>
                <input
                  id={`${id}-pw2`}
                  type="password"
                  value={repeat}
                  onChange={(e) => setRepeat(e.target.value)}
                  disabled={busy}
                  autoComplete="new-password"
                  aria-invalid={mismatch || undefined}
                  required
                />
                {mismatch && <span className="small neg-ink">The passwords do not match.</span>}
              </div>
            )}
            <ErrorLine error={error} />
            <button type="submit" className="btn primary lg" disabled={busy || !email.trim() || !password || mismatch}>
              {busy ? (register ? "Creating account…" : "Signing in…") : register ? "Create account" : "Sign in"}
            </button>
          </form>
        )}

        <p className="muted small auth-switch">
          {register ? (
            <>
              Already have an account?{" "}
              <a className="link-btn" href="/login" onClick={switchTo("/login")}>
                Sign in
              </a>
            </>
          ) : open === false ? (
            "New accounts are created by this server's admin."
          ) : (
            <>
              No account yet?{" "}
              <a className="link-btn" href="/register" onClick={switchTo("/register")}>
                Create one
              </a>
            </>
          )}
        </p>
      </section>
    </main>
  );
}
