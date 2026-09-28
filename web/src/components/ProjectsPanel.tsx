import { useEffect, useId, useState, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import type { AsyncState } from "../hooks";
import type { Project } from "../types";
import { fmtNum } from "../format";
import { ErrorLine, Loading } from "./Status";

interface Props {
  projects: AsyncState<Project[]>;
  project: AsyncState<Project>;
  selectedId: number | null;
  allQueries: string[];
  onSelect: (id: number | null) => void;
  onChanged: () => void;
}

const DEFAULT_PROJECT_ID = 1;

export function ProjectsPanel({ projects, project, selectedId, allQueries, onSelect, onChanged }: Props) {
  const id = useId();
  const [name, setName] = useState("");
  const [addQuery, setAddQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  useEffect(() => {
    setConfirmDelete(false);
    setAddQuery("");
    setError(null);
  }, [selectedId]);

  const act = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const create = (e: FormEvent) => {
    e.preventDefault();
    const n = name.trim();
    if (!n) return;
    void act(async () => {
      const p = await api.createProject(n);
      setName("");
      onSelect(p.id);
      onChanged();
    });
  };

  const current = project.data && project.data.id === selectedId ? project.data : undefined;
  const inProject = new Set(current?.queries ?? []);
  const addable = allQueries.filter((q) => !inProject.has(q));

  return (
    <section className="panel" aria-labelledby={`${id}-h`}>
      <h2 className="panel-title" id={`${id}-h`}>
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
        />
        <button type="submit" className="btn" disabled={busy || !name.trim()}>
          Create
        </button>
      </form>

      <ErrorLine error={projects.error} />
      {projects.loading && !projects.data && <Loading label="Loading projects" />}

      {selectedId === null ? (
        <p className="muted small">Select a project in the header to manage its keywords.</p>
      ) : (
        <div className="project-detail">
          {project.loading && !current && <Loading label="Loading project" />}
          <ErrorLine error={project.error} />
          {current && (
            <>
              <h3 className="sub-title">
                {current.name}{" "}
                <span className="muted mono">
                  · {fmtNum(current.query_count)} kw · {fmtNum(current.mention_count)} mentions
                </span>
              </h3>
              {(current.queries ?? []).length === 0 ? (
                <p className="muted small">No keywords in this project yet.</p>
              ) : (
                <ul className="kw-list">
                  {(current.queries ?? []).map((q) => (
                    <li key={q}>
                      <span className="kw">{q}</span>
                      <button
                        type="button"
                        className="icon-btn"
                        aria-label={`Remove “${q}” from ${current.name}`}
                        title="Remove from project"
                        disabled={busy}
                        onClick={() =>
                          void act(async () => {
                            await api.removeProjectQuery(current.id, q);
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

              <form
                className="inline-form"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (!addQuery) return;
                  void act(async () => {
                    await api.addProjectQuery(current.id, addQuery);
                    setAddQuery("");
                    onChanged();
                  });
                }}
              >
                <label className="sr-only" htmlFor={`${id}-add`}>
                  Add a tracked keyword to this project
                </label>
                <select
                  id={`${id}-add`}
                  value={addQuery}
                  onChange={(e) => setAddQuery(e.target.value)}
                  disabled={busy || addable.length === 0}
                >
                  <option value="">{addable.length === 0 ? "No other keywords" : "Add keyword…"}</option>
                  {addable.map((q) => (
                    <option key={q} value={q}>
                      {q}
                    </option>
                  ))}
                </select>
                <button type="submit" className="btn" disabled={busy || !addQuery}>
                  Add
                </button>
              </form>

              {current.id !== DEFAULT_PROJECT_ID && (
                <div className="danger-zone">
                  {!confirmDelete ? (
                    <button type="button" className="btn ghost danger" disabled={busy} onClick={() => setConfirmDelete(true)}>
                      Delete project
                    </button>
                  ) : (
                    <div className="confirm" role="group" aria-label="Confirm project deletion">
                      <span className="small">
                        Delete <strong>{current.name}</strong>?
                      </span>
                      <div className="btn-row">
                        <button
                          type="button"
                          className="btn danger"
                          disabled={busy}
                          autoFocus
                          onClick={() =>
                            void act(async () => {
                              await api.deleteProject(current.id);
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
            </>
          )}
        </div>
      )}
      <ErrorLine error={error} />
    </section>
  );
}
