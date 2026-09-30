import type { ReactNode } from "react";
import type { Selection } from "../App";
import type { AsyncState } from "../hooks";
import type { Meta, Project, User } from "../types";
import { fmtNum } from "../format";
import { Select } from "./ui/Select";

export type View = "listen" | "scan" | "drafts" | "accounts" | "setup";

interface Props {
  projects: AsyncState<Project[]>;
  keywords: string[] | undefined;
  keywordsLoading: boolean;
  keywordsError: string | null;
  sel: Selection;
  onSelect: (next: Selection) => void;
  view: View;
  onView: (v: View) => void;
  total: number | undefined;
  needSetup: number;
  meta: AsyncState<Meta>;
  user: User;
  onSignOut: () => void;
}

function Icon({ children }: Readonly<{ children: ReactNode }>) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      {children}
    </svg>
  );
}

const NAV: readonly { view: View; name: string; sub: string; icon: ReactNode }[] = [
  { view: "listen", name: "Listen", sub: "Overview and mentions", icon: <path d="M3 12h4l3-8 4 16 3-8h4" /> },
  {
    view: "scan",
    name: "Scan",
    sub: "Pull new mentions",
    icon: (
      <>
        <path d="M19.07 4.93A10 10 0 1 0 22 12" />
        <path d="M16.24 7.76A6 6 0 1 0 18 12" />
        <path d="M12 12l6.5-6.5" />
      </>
    ),
  },
  {
    view: "drafts",
    name: "Drafts",
    sub: "Review and publish",
    icon: (
      <>
        <path d="M4 20h4L19 9l-4-4L4 16v4z" />
        <path d="M13 7l4 4" />
      </>
    ),
  },
  {
    view: "accounts",
    name: "Accounts",
    sub: "Health, limits, ban rate",
    icon: (
      <>
        <circle cx="9" cy="8" r="3" />
        <path d="M3 20c0-3.3 2.7-6 6-6s6 2.7 6 6" />
        <path d="M16 11l2 2 4-4" />
      </>
    ),
  },
  {
    view: "setup",
    name: "Setup",
    sub: "Sources, accounts, projects",
    icon: (
      <>
        <path d="M4 7h10M18 7h2M4 17h4M12 17h8" />
        <circle cx="16" cy="7" r="2" />
        <circle cx="10" cy="17" r="2" />
      </>
    ),
  },
];

/** What a section shows next to its name: the mention total, or how many sources need setup. */
function navExtra(view: View, total: number | undefined, needSetup: number): ReactNode {
  if (view === "listen" && total !== undefined) return <span className="nav-count">{fmtNum(total)}</span>;
  if (view === "setup" && needSetup > 0) return <span className="badge b-mari">{needSetup} need setup</span>;
  return null;
}

export function Rail({ projects, keywords, keywordsLoading, keywordsError, sel, onSelect, view, onView, total, needSetup, meta, user, onSignOut }: Readonly<Props>) {
  const kw = keywords ?? [];
  const kwOptions = sel.q && !kw.includes(sel.q) ? [sel.q, ...kw] : kw;
  const projectMissing = sel.p !== null && projects.data && !projects.data.some((p) => p.id === sel.p);
  const serverDown = meta.error !== null && !meta.data;

  return (
    <aside className="rail" aria-label="Radaro navigation">
      <div className="rail-top">
        <div className="brand">
          <span className="wordmark">Radaro</span>
          <span className="tri" aria-hidden="true">
            <i />
            <i />
            <i />
          </span>
        </div>
        <span className="rail-top-end">
          <span className={`server-pill${serverDown ? " is-down" : ""}`}>
            <span className="dot" aria-hidden="true" />
            {serverDown ? "offline" : "online"}
          </span>
          <button type="button" className="link-btn quiet rail-signout" onClick={onSignOut} title={`Signed in as ${user.email}`}>
            Sign out
          </button>
        </span>
      </div>

      <div className="selectors">
        <div className="field">
          <span className="lbl">Project</span>
          <Select
            label="Project"
            value={sel.p === null ? "" : String(sel.p)}
            onChange={(v) => onSelect({ p: v ? Number(v) : null, q: null, v: sel.v })}
            disabled={!projects.data && projects.loading}
            options={[
              { value: "", label: "All projects" },
              ...(projectMissing ? [{ value: String(sel.p), label: `Project #${sel.p} (missing)` }] : []),
              ...(projects.data ?? []).map((p) => ({ value: String(p.id), label: p.name })),
            ]}
          />
        </div>

        <div className="field">
          <span className="lbl">Keyword</span>
          <Select
            label="Keyword"
            value={sel.q ?? ""}
            onChange={(v) => onSelect({ p: sel.p, q: v || null, v: sel.v })}
            disabled={keywords === undefined && keywordsLoading}
            options={[{ value: "", label: sel.p !== null ? "Whole project" : "Every keyword" }, ...kwOptions.map((q) => ({ value: q, label: q }))]}
          />
        </div>
        {(projects.error || keywordsError) && (
          <p className="error-line" role="alert">
            {projects.error ?? keywordsError}
          </p>
        )}
      </div>

      <nav className="nav" aria-label="Sections">
        {NAV.map((n) => {
          const on = view === n.view;
          return (
            <button
              key={n.view}
              type="button"
              className={`nav-item nav-${n.view}${on ? " is-on" : ""}`}
              aria-current={on ? "page" : undefined}
              onClick={() => onView(n.view)}
            >
              <span className="nav-ico" aria-hidden="true">
                <Icon>{n.icon}</Icon>
              </span>
              <span className="nav-text">
                <span className="nav-name">
                  {n.name}
                  {navExtra(n.view, total, needSetup)}
                </span>
                <span className="nav-sub">{n.sub}</span>
              </span>
            </button>
          );
        })}
      </nav>

      <div className="rail-foot">
        <span className="rail-status">
          <span className={`dot${serverDown ? " is-down" : ""}`} aria-hidden="true" />
          {serverDown ? "Can't reach the server" : "Connected"}
          {meta.data && <span className="muted small"> · v{meta.data.version.replace(/^v/, "")}</span>}
        </span>
        <span className="rail-user small">
          <span className="email muted" title={user.email}>
            {user.email}
            {user.is_admin && " · admin"}
          </span>
          <button type="button" className="link-btn quiet" onClick={onSignOut}>
            Sign out
          </button>
        </span>
      </div>
    </aside>
  );
}
