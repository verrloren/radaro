import { useEffect, useId, useRef, useState, type FormEvent, type MouseEvent, type ReactNode } from "react";
import { api, ApiError, errorMessage } from "../api";
import type { User } from "../types";
import { navigate } from "../router";
// Paul Signac, Capo di Noli (1898), public domain (Wikimedia Commons),
// recoloured with a gradient map in the logo's marigold, magenta and violet.
import artwork from "../assets/auth-art.jpg";

interface Props {
  mode: "login" | "register";
  serverError?: string;
  onSignedIn: (user: User) => void;
}

type FieldErrors = Partial<Record<"email" | "password" | "repeat", string>>;

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function AuthPage({ mode, serverError, onSignedIn }: Props) {
  const id = useId();
  const register = mode === "register";
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(serverError ?? null);
  const [fields, setFields] = useState<FieldErrors>({});
  const [open, setOpen] = useState<boolean | null>(null);
  const formRef = useRef<HTMLFormElement>(null);

  useEffect(() => {
    setError(serverError ?? null);
    setFields({});
    setRepeat("");
    const ctrl = new AbortController();
    api.authConfig(ctrl.signal).then(
      (c) => setOpen(c.registration_open),
      () => setOpen(null),
    );
    return () => ctrl.abort();
  }, [mode, serverError]);

  const validate = (): FieldErrors => {
    const e: FieldErrors = {};
    if (!EMAIL.test(email.trim())) e.email = "Enter an email like you@example.com.";
    if (!password) e.password = "Enter your password.";
    else if (register && password.length < 8) e.password = "Use at least 8 characters.";
    if (register && repeat !== password) e.repeat = "The passwords do not match.";
    return e;
  };

  // A message disappears as soon as its field becomes valid.
  const clear = (key: keyof FieldErrors) => {
    if (!fields[key]) return;
    setFields((f) => {
      const next = { ...f };
      delete next[key];
      return next;
    });
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const found = validate();
    setFields(found);
    setError(null);
    if (Object.keys(found).length > 0) {
      formRef.current?.querySelector<HTMLInputElement>('[aria-invalid="true"]')?.focus();
      return;
    }
    setBusy(true);
    try {
      const res = register ? await api.register(email, password) : await api.login(email, password);
      onSignedIn(res.user);
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) setError("Too many attempts. Wait a minute and try again.");
      else if (err instanceof ApiError && err.status === 401) setError("Invalid email or password. Check both and try again.");
      else if (err instanceof ApiError && err.status === 409) setError("An account with this email already exists. Sign in instead, or use another email.");
      else setError(errorMessage(err));
      setBusy(false);
    }
  };

  const go = (to: "/login" | "/register") => (e: MouseEvent) => {
    e.preventDefault();
    navigate(`${to}${window.location.search}`);
  };

  const closed = register && open === false;
  const mismatch = register && repeat !== "" && repeat !== password;
  const repeatError = fields.repeat ?? (mismatch ? "The passwords do not match." : undefined);

  const input = (
    key: keyof FieldErrors,
    label: ReactNode,
    value: string,
    set: (v: string) => void,
    type: "email" | "password",
    autoComplete: string,
    message: string | undefined,
  ) => {
    const fid = `${id}-${key}`;
    const control = (
      <input
        id={fid}
        className="auth-input"
        type={type === "password" && show ? "text" : type}
        value={value}
        onChange={(e) => {
          set(e.target.value);
          clear(key);
        }}
        disabled={busy}
        autoComplete={autoComplete}
        autoFocus={key === "email"}
        spellCheck={false}
        inputMode={type === "email" ? "email" : undefined}
        aria-invalid={message ? true : undefined}
        aria-describedby={message ? `${fid}-msg` : undefined}
      />
    );
    return (
      <div className="auth-field">
        <label htmlFor={fid}>{label}</label>
        {type === "password" ? (
          <div className="auth-pw">
            {control}
            <button type="button" className="auth-peek" onClick={() => setShow((s) => !s)} aria-label={show ? "Hide password" : "Show password"}>
              {show ? "Hide" : "Show"}
            </button>
          </div>
        ) : (
          control
        )}
        {message && (
          <p className="auth-msg" id={`${fid}-msg`}>
            {message}
          </p>
        )}
      </div>
    );
  };

  return (
    <main className="auth-shell">
      <div className="auth-card">
        <figure className="auth-art">
          <img src={artwork} alt="Paul Signac’s painting Capo di Noli, a coastal path under trees, recoloured in Radaro’s marigold, magenta and violet" />
          <figcaption>Paul Signac · Capo di Noli, 1898</figcaption>
        </figure>

        <section className="auth-side" aria-labelledby={`${id}-h`}>
          {register && (
            <a className="auth-back" href="/login" onClick={go("/login")}>
              <span aria-hidden="true">←</span> Back
            </a>
          )}

          <header className="auth-head" key={mode}>
            <p className="auth-eyebrow">{register ? "Create your account on" : "Sign in to"}</p>
            <h1 id={`${id}-h`}>{register ? "Radaro, the ear on the internet" : "Hear what the internet says"}</h1>
          </header>

          {closed ? (
            <p className="auth-note" key="closed">
              <strong>Sign-up is closed on this server.</strong> New accounts are created by its admin.
            </p>
          ) : (
            <form className="auth-form" ref={formRef} onSubmit={(e) => void submit(e)} noValidate key={`form-${mode}`}>
              {input("email", "Email", email, setEmail, "email", register ? "email" : "username", fields.email)}
              {input(
                "password",
                <>
                  Password {register && <span className="muted">· at least 8 characters</span>}
                </>,
                password,
                setPassword,
                "password",
                register ? "new-password" : "current-password",
                fields.password,
              )}
              {register && input("repeat", "Repeat password", repeat, setRepeat, "password", "new-password", repeatError)}
              {error && (
                <p className="auth-alert" role="alert">
                  <span aria-hidden="true">!</span> {error}
                </p>
              )}
              <button type="submit" className="auth-submit" disabled={busy}>
                {busy && <span className="auth-spin" aria-hidden="true" />}
                {busy ? (register ? "Creating account…" : "Signing in…") : register ? "Create account" : "Sign in"}
              </button>
            </form>
          )}

          <p className="auth-switch">
            {register ? (
              <>
                Already have an account?{" "}
                <a className="link-btn" href="/login" onClick={go("/login")}>
                  Sign in
                </a>
              </>
            ) : open === false ? (
              "New accounts are created by this server’s admin."
            ) : (
              <>
                Don’t have an account?{" "}
                <a className="link-btn" href="/register" onClick={go("/register")}>
                  Sign up
                </a>
              </>
            )}
          </p>

          <footer className="auth-foot">
            <span className="brand">
              <span className="wordmark">Radaro</span>
              <span className="tri" aria-hidden="true">
                <i />
                <i />
                <i />
              </span>
            </span>
            <p>
              Self-hosted social listening
              <br />
              with approval before every post.
            </p>
          </footer>
        </section>
      </div>
    </main>
  );
}
