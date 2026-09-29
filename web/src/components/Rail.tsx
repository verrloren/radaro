import type { Selection } from "../App";
import type { AsyncState } from "../hooks";
import type { Meta, Project } from "../types";
import { fmtNum } from "../format";
import { Select } from "./ui/Select";

export type View = "listen" | "scan" | "setup";

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
}

export function Rail({ projects, keywords, keywordsLoading, keywordsError, sel, onSelect, view, onView, total, needSetup, meta }: Props) {
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
        <span className={`server-pill${serverDown ? " is-down" : ""}`}>
          <span className="dot" aria-hidden="true" />
          {serverDown ? "offline" : "local"}
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
        <button type="button" className={`nav-item nav-listen${view === "listen" ? " is-on" : ""}`} aria-current={view === "listen" ? "page" : undefined} onClick={() => onView("listen")}>
          <span className="nav-ico" aria-hidden="true">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M3 12h4l3-8 4 16 3-8h4" />
            </svg>
          </span>
          <span className="nav-text">
            <span className="nav-name">
              Listen
              {total !== undefined && <span className="nav-count">{fmtNum(total)}</span>}
            </span>
            <span className="nav-sub">Overview and mentions</span>
          </span>
        </button>
        <button type="button" className={`nav-item nav-scan${view === "scan" ? " is-on" : ""}`} aria-current={view === "scan" ? "page" : undefined} onClick={() => onView("scan")}>
          <span className="nav-ico" aria-hidden="true">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <path d="M19.07 4.93A10 10 0 1 0 22 12" />
              <path d="M16.24 7.76A6 6 0 1 0 18 12" />
              <path d="M12 12l6.5-6.5" />
            </svg>
          </span>
          <span className="nav-text">
            <span className="nav-name">Scan</span>
            <span className="nav-sub">Pull new mentions</span>
          </span>
        </button>
        <button type="button" className={`nav-item nav-setup${view === "setup" ? " is-on" : ""}`} aria-current={view === "setup" ? "page" : undefined} onClick={() => onView("setup")}>
          <span className="nav-ico" aria-hidden="true">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <path d="M4 7h10M18 7h2M4 17h4M12 17h8" />
              <circle cx="16" cy="7" r="2" />
              <circle cx="10" cy="17" r="2" />
            </svg>
          </span>
          <span className="nav-text">
            <span className="nav-name">
              Setup
              {needSetup > 0 && <span className="badge b-mari">{needSetup} need setup</span>}
            </span>
            <span className="nav-sub">Sources, accounts, projects</span>
          </span>
        </button>
      </nav>

      <div className="rail-foot">
        <span className="rail-status">
          <span className={`dot${serverDown ? " is-down" : ""}`} aria-hidden="true" />
          {serverDown ? "Can't reach the local server" : "Local server running"}
        </span>
        <span className="muted small">
          127.0.0.1 · your data stays on this machine
          {meta.data && ` · v${meta.data.version.replace(/^v/, "")}`}
        </span>
      </div>
    </aside>
  );
}
