import { useCallback, useEffect, useState } from "react";
import { api, ApiError, onSignedOut } from "./api";
import type { User } from "./types";
import App from "./App";
import { AuthPage } from "./components/AuthPage";
import { Loading } from "./components/Status";
import { loginPath, navigate, nextPath, usePath } from "./router";

type Auth = { state: "checking" } | { state: "out"; error?: string } | { state: "in"; user: User };

const PUBLIC = new Set(["/login", "/register"]);

/** Everything but /login and /register needs a signed-in user. */
export function Root() {
  const path = usePath();
  const [auth, setAuth] = useState<Auth>({ state: "checking" });

  useEffect(() => {
    const ctrl = new AbortController();
    api.me(ctrl.signal).then(
      (user) => setAuth({ state: "in", user }),
      (e: unknown) => {
        if (ctrl.signal.aborted) return;
        // 401: not signed in. Anything else: the server is unreachable.
        setAuth(e instanceof ApiError && e.status === 401 ? { state: "out" } : { state: "out", error: String((e as Error).message ?? e) });
      },
    );
    return () => ctrl.abort();
  }, []);

  useEffect(() => onSignedOut(() => setAuth({ state: "out" })), []);

  const isPublic = PUBLIC.has(path);
  useEffect(() => {
    if (auth.state === "out" && !isPublic) navigate(loginPath(), true);
    if (auth.state === "in" && isPublic) navigate(nextPath(), true);
  }, [auth.state, isPublic]);

  const signedIn = useCallback((user: User) => {
    setAuth({ state: "in", user });
    navigate(nextPath(), true);
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } finally {
      setAuth({ state: "out" });
      navigate("/login", true);
    }
  }, []);

  if (auth.state === "checking" || (auth.state === "in" && isPublic) || (auth.state === "out" && !isPublic)) {
    return (
      <div className="auth-shell">
        <Loading label="Loading Radaro" />
      </div>
    );
  }
  if (auth.state === "out") {
    return <AuthPage mode={path === "/register" ? "register" : "login"} serverError={auth.error} onSignedIn={signedIn} />;
  }
  return <App user={auth.user} onSignOut={signOut} />;
}
