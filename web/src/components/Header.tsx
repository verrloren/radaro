import type { Selection } from "../App";
import type { AsyncState } from "../hooks";
import type { Project } from "../types";

interface Props {
  projects: AsyncState<Project[]>;
  keywords: string[] | undefined;
  keywordsLoading: boolean;
  keywordsError: string | null;
  sel: Selection;
  onSelect: (next: Selection) => void;
}

export function Header({ projects, keywords, keywordsLoading, keywordsError, sel, onSelect }: Props) {
  const kw = keywords ?? [];
  const kwOptions = sel.q && !kw.includes(sel.q) ? [sel.q, ...kw] : kw;
  const projectMissing = sel.p !== null && projects.data && !projects.data.some((p) => p.id === sel.p);

  return (
    <header className="header">
      <div className="brand">
        <svg className="brand-mark" viewBox="0 0 32 32" aria-hidden="true">
          <circle cx="16" cy="16" r="13" />
          <circle cx="16" cy="16" r="6.5" />
          <path d="M16 16 L26.5 9.5" />
        </svg>
        <span className="wordmark">Radaro</span>
        <span className="tag">social listening</span>
      </div>

      <div className="selectors">
        <label className="field compact">
          <span className="field-label">Project</span>
          <select
            value={sel.p ?? ""}
            onChange={(e) => onSelect({ p: e.target.value ? Number(e.target.value) : null, q: null })}
            disabled={!projects.data && projects.loading}
          >
            <option value="">All keywords</option>
            {projectMissing && <option value={sel.p ?? ""}>Project #{sel.p} (missing)</option>}
            {(projects.data ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} ({p.query_count})
              </option>
            ))}
          </select>
        </label>

        <label className="field compact">
          <span className="field-label">Keyword</span>
          <select
            value={sel.q ?? ""}
            onChange={(e) => onSelect({ p: sel.p, q: e.target.value || null })}
            disabled={keywords === undefined && keywordsLoading}
          >
            <option value="">{sel.p !== null ? "Whole project" : "Every keyword"}</option>
            {kwOptions.map((q) => (
              <option key={q} value={q}>
                {q}
              </option>
            ))}
          </select>
        </label>
      </div>
      {(projects.error || keywordsError) && (
        <p className="error-line header-error" role="alert">
          {projects.error ?? keywordsError}
        </p>
      )}
    </header>
  );
}
