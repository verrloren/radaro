import { useEffect, useId, useState, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import { useAsync, type AsyncState } from "../hooks";
import type { AccountsResponse, Platform, Project } from "../types";
import { fmtNum } from "../format";
import { ErrorLine, Loading } from "./Status";
import { ConnectForm, RedditForm } from "./ConnectForms";
import { ProjectAccounts } from "./ProjectAccounts";
import { Modal } from "./ui/Modal";

interface Props {
  projects: AsyncState<Project[]>;
  selectedId: number | null;
  accounts: AsyncState<AccountsResponse>;
  rev: number;
  onSelect: (id: number | null) => void;
  onChanged: () => void;
}

/** Keywords arrive one per line or comma-separated. */
function splitKeywords(raw: string): string[] {
  return raw
    .split(/[\n,]/)
    .map((k) => k.trim())
    .filter(Boolean);
}

export function ProjectsPanel({ projects, selectedId, accounts, rev, onSelect, onChanged }: Readonly<Props>) {
  const id = useId();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const create = (e: FormEvent) => {
    e.preventDefault();
    const n = name.trim();
    if (!n) return;
    setBusy(true);
    setError(null);
    api
      .createProject(n)
      .then((p) => {
        setName("");
        onSelect(p.id);
        onChanged();
      })
      .catch((err: unknown) => setError(errorMessage(err)))
      .finally(() => setBusy(false));
  };

  const current = projects.data?.find((p) => p.id === selectedId);

  return (
    <section className="card pad" aria-labelledby={`${id}-h`}>
      <h2 className="card-title lg" id={`${id}-h`}>
        Projects
      </h2>

      <form className="inline-form" onSubmit={create}>
        <label className="sr-only" htmlFor={`${id}-name`}>
          New project name
        </label>
        <input
          id={`${id}-name`}
          type="text"
          value={name}
          placeholder="New project name"
          onChange={(e) => setName(e.target.value)}
          disabled={busy}
          autoComplete="off"
          maxLength={100}
        />
        <button type="submit" className="btn" disabled={busy || !name.trim()}>
          Create
        </button>
      </form>
      <ErrorLine error={error} />

      <ErrorLine error={projects.error} />
      {projects.loading && !projects.data && <Loading label="Loading projects" />}

      {(projects.data ?? []).length > 0 && (
        <ul className="project-list" aria-label="Projects">
          {(projects.data ?? []).map((p) => (
            <li key={p.id}>
              <button type="button" className={`project-item${p.id === selectedId ? " is-on" : ""}`} aria-pressed={p.id === selectedId} onClick={() => onSelect(p.id === selectedId ? null : p.id)}>
                <span className="strong">
                  {p.name}
                  {p.is_default && <span className="muted small"> · default</span>}
                </span>
                <span className="muted small">
                  {fmtNum(p.query_count)} keyword{p.query_count === 1 ? "" : "s"} · {fmtNum(p.mention_count)} mentions
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      {selectedId === null ? (
        <p className="muted small">Pick a project to manage its keywords and accounts.</p>
      ) : current ? (
        <ProjectDetail key={current.id} project={current} accounts={accounts} rev={rev} onSelect={onSelect} onChanged={onChanged} />
      ) : (
        projects.data && <p className="muted small">This project does not exist.</p>
      )}
    </section>
  );
}

function ProjectDetail({
  project,
  accounts,
  rev,
  onSelect,
  onChanged,
}: Readonly<{
  project: Project;
  accounts: AsyncState<AccountsResponse>;
  rev: number;
  onSelect: (id: number | null) => void;
  onChanged: () => void;
}>) {
  const id = useId();
  const keywords = useAsync((s) => api.keywords(project.id, s), [project.id, rev]);
  const [draft, setDraft] = useState("");
  const [renaming, setRenaming] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [connect, setConnect] = useState<Platform | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  useEffect(() => setNote(null), [project.id]);

  const act = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      await fn();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const addKeywords = (e: FormEvent) => {
    e.preventDefault();
    const list = splitKeywords(draft);
    if (list.length === 0) return;
    void act(async () => {
      const res = await api.addKeywords(project.id, list);
      setDraft("");
      const skipped = list.length - res.added;
      setNote(`Added ${res.added} keyword${res.added === 1 ? "" : "s"}${skipped > 0 ? ` · ${skipped} already in the project` : ""}.`);
      onChanged();
    });
  };

  const rename = (e: FormEvent) => {
    e.preventDefault();
    if (renaming === null) return;
    void act(async () => {
      await api.renameProject(project.id, renaming);
      setRenaming(null);
      onChanged();
    });
  };

  const pending = draft ? splitKeywords(draft).length : 0;

  return (
    <div className="project-detail">
      {renaming === null ? (
        <h3 className="sub-title">
          {project.name}{" "}
          <button type="button" className="link-btn quiet small" onClick={() => setRenaming(project.name)}>
            Rename
          </button>
        </h3>
      ) : (
        <form className="inline-form" onSubmit={rename} aria-label="Rename project">
          <label className="sr-only" htmlFor={`${id}-rename`}>
            Project name
          </label>
          <input id={`${id}-rename`} type="text" value={renaming} onChange={(e) => setRenaming(e.target.value)} disabled={busy} autoFocus maxLength={100} />
          <button type="submit" className="btn sm" disabled={busy || !renaming.trim()}>
            Save
          </button>
          <button type="button" className="btn ghost sm" disabled={busy} onClick={() => setRenaming(null)}>
            Cancel
          </button>
        </form>
      )}

      <div className="project-block">
        <h4 className="block-title">Keywords</h4>
        <ErrorLine error={keywords.error} />
        {keywords.loading && !keywords.data && <Loading label="Loading keywords" />}
        {keywords.data && keywords.data.length === 0 && <p className="muted small">No keywords yet. Add what people would write about this project.</p>}
        {keywords.data && keywords.data.length > 0 && (
          <ul className="kw-list">
            {keywords.data.map((k) => (
              <li key={k.id} title={`${fmtNum(k.mention_count)} mentions · ${k.sources.join(", ")}`}>
                <span>{k.query}</span>
                <span className="muted small kw-count">{fmtNum(k.mention_count)}</span>
                <button
                  type="button"
                  className="icon-btn"
                  aria-label={`Remove “${k.query}” from ${project.name}`}
                  title="Remove from project (mentions are kept)"
                  disabled={busy}
                  onClick={() =>
                    void act(async () => {
                      await api.removeKeyword(project.id, k.id);
                      onChanged();
                    })
                  }
                >
                  ×
                </button>
              </li>
            ))}
          </ul>
        )}
        <form className="kw-add" onSubmit={addKeywords}>
          <label className="sr-only" htmlFor={`${id}-kw`}>
            Add keywords, one per line or separated by commas
          </label>
          <textarea
            id={`${id}-kw`}
            rows={2}
            value={draft}
            placeholder="Add keywords — one per line or comma-separated"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) addKeywords(e);
            }}
            disabled={busy}
            spellCheck={false}
          />
          <button type="submit" className="btn sm" disabled={busy || pending === 0}>
            {pending > 1 ? `Add ${pending}` : "Add"}
          </button>
        </form>
        {note && (
          <p className="ok-line" role="status">
            ✓ {note} Scan them from Scan.
          </p>
        )}
      </div>

      <ProjectAccounts
        projectId={project.id}
        projectName={project.name}
        own={accounts.data?.accounts ?? []}
        ownError={accounts.error}
        rev={rev}
        onConnect={setConnect}
        onChanged={onChanged}
      />

      <ErrorLine error={error} />

      {!project.is_default && (
        <div className="danger-zone">
          {!confirmDelete ? (
            <button type="button" className="btn ghost danger" disabled={busy} onClick={() => setConfirmDelete(true)}>
              Delete project
            </button>
          ) : (
            <div className="confirm" role="group" aria-label="Confirm project deletion">
              <span className="small">
                Delete <strong>{project.name}</strong>? Mentions of its keywords are kept.
              </span>
              <div className="btn-row">
                <button
                  type="button"
                  className="btn danger"
                  disabled={busy}
                  autoFocus
                  onClick={() =>
                    void act(async () => {
                      await api.deleteProject(project.id);
                      onSelect(null);
                      onChanged();
                    })
                  }
                >
                  Yes, delete
                </button>
                <button type="button" className="btn ghost" disabled={busy} onClick={() => setConfirmDelete(false)}>
                  Cancel
                </button>
              </div>
            </div>
          )}
        </div>
      )}

      {connect && (
        <Modal title={`Connect ${connect.label} to ${project.name}`} onClose={() => setConnect(null)}>
          {connect.name === "reddit" ? (
            <RedditForm projectId={project.id} onDone={()=>{setConnect(null);onChanged();}} />
          ) : (
            <ConnectForm
              platform={connect}
              projectId={project.id}
              onDone={() => {
                setConnect(null);
                onChanged();
              }}
            />
          )}
        </Modal>
      )}
    </div>
  );
}
