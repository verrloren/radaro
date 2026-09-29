import { useEffect, useId, useState, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import type { AsyncState } from "../hooks";
import type { Project } from "../types";
import { fmtNum } from "../format";
import { ErrorLine, Loading } from "./Status";
import { Select } from "./ui/Select";

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
        />
        <button type="submit" className="btn" disabled={busy || !name.trim()}>
          Create
        </button>
      </form>

      <ErrorLine error={projects.error} />
      {projects.loading && !projects.data && <Loading label="Loading projects" />}

      {(projects.data ?? []).length > 0 && (
        <ul className="project-list" aria-label="Projects">
          {(projects.data ?? []).map((p) => (
            <li key={p.id}>
              <button type="button" className={`project-item${p.id === selectedId ? " is-on" : ""}`} aria-pressed={p.id === selectedId} onClick={() => onSelect(p.id === selectedId ? null : p.id)}>
                <span className="strong">{p.name}</span>
                <span className="muted small">
                  {fmtNum(p.query_count)} keyword{p.query_count === 1 ? "" : "s"} · {fmtNum(p.mention_count)} mentions
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      {selectedId === null ? (
        <p className="muted small">Pick a project to manage its keywords.</p>
      ) : (
        <div className="project-detail">
          {project.loading && !current && <Loading label="Loading project" />}
          <ErrorLine error={project.error} />
          {current && (
            <>
              <h3 className="sub-title">
                {current.name}{" "}
                <span className="muted small">
                  · {fmtNum(current.query_count)} keyword{current.query_count === 1 ? "" : "s"} · {fmtNum(current.mention_count)} mentions
                </span>
              </h3>
              {(current.queries ?? []).length === 0 ? (
                <p className="muted small">No keywords in this project yet.</p>
              ) : (
                <ul className="kw-list">
                  {(current.queries ?? []).map((q) => (
                    <li key={q}>
                      <span>{q}</span>
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
                <Select
                  id={`${id}-add`}
                  label="Add a tracked keyword to this project"
                  value={addQuery}
                  onChange={setAddQuery}
                  disabled={busy || addable.length === 0}
                  options={[{ value: "", label: addable.length === 0 ? "No other keywords" : "Add keyword…" }, ...addable.map((q) => ({ value: q, label: q }))]}
                />
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
