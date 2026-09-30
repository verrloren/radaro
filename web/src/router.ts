import { useEffect, useState } from "react";

/** Changes the address without a reload; usePath listeners follow. */
export function navigate(to: string, replace = false) {
  if (replace) window.history.replaceState(null, "", to);
  else window.history.pushState(null, "", to);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

export function usePath(): string {
  const [path, setPath] = useState(window.location.pathname);
  useEffect(() => {
    const on = () => setPath(window.location.pathname);
    window.addEventListener("popstate", on);
    return () => window.removeEventListener("popstate", on);
  }, []);
  return path;
}

/** Where to go after signing in: a same-site path only, never another origin. */
export function nextPath(): string {
  const next = new URLSearchParams(window.location.search).get("next") ?? "/";
  return next.startsWith("/") && !next.startsWith("//") && !next.startsWith("/\\") ? next : "/";
}

export function loginPath(): string {
  const here = `${window.location.pathname}${window.location.search}`;
  return here === "/" ? "/login" : `/login?next=${encodeURIComponent(here)}`;
}
