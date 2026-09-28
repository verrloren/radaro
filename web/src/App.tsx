import { useCallback, useEffect, useMemo, useState } from "react";
import { api, ApiError } from "./api";
import { useAsync } from "./hooks";
import { makeSourceLookup } from "./sources";
import type { Scope, TrackResult } from "./types";
import { Header } from "./components/Header";
import { ScanPanel } from "./components/ScanPanel";
import { ProjectsPanel } from "./components/ProjectsPanel";
import { Overview } from "./components/Overview";
import { VolumeChart } from "./components/VolumeChart";
import { StatsRow } from "./components/StatsRow";
import { Themes } from "./components/Themes";
import { MentionsFeed } from "./components/MentionsFeed";
import { EmptyState } from "./components/EmptyState";
import { ErrorLine, Loading } from "./components/Status";

export interface Selection {
  q: string | null;
  p: number | null;
}

function readUrl(): Selection {
  const sp = new URLSearchParams(window.location.search);
  const q = sp.get("q")?.trim() || null;
  const pRaw = sp.get("p");
  const pNum = pRaw ? Number.parseInt(pRaw, 10) : NaN;
  return { q, p: Number.isFinite(pNum) && pNum > 0 ? pNum : null };
}

function writeUrl(sel: Selection) {
  const sp = new URLSearchParams(window.location.search);
  sp.delete("q");
  sp.delete("p");
  if (sel.p !== null) sp.set("p", String(sel.p));
  if (sel.q) sp.set("q", sel.q);
  const qs = sp.toString();
  const url = `${window.location.pathname}${qs ? `?${qs}` : ""}${window.location.hash}`;
  if (url !== `${window.location.pathname}${window.location.search}${window.location.hash}`) {
    window.history.pushState(null, "", url);
  }
}

export default function App() {
  const [sel, setSel] = useState<Selection>(readUrl);
  const [rev, setRev] = useState(0);
  const refresh = useCallback(() => setRev((r) => r + 1), []);

  useEffect(() => {
    const onPop = () => setSel(readUrl());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const select = useCallback((next: Selection) => {
    writeUrl(next);
    setSel(next);
  }, []);

  // A keyword narrows the view; otherwise the project; otherwise everything.
  const scope: Scope = useMemo(
    () => (sel.q ? { kind: "query", q: sel.q } : sel.p !== null ? { kind: "project", p: sel.p } : { kind: "all" }),
    [sel.q, sel.p],
  );
  const scopeKey = scope.kind === "query" ? `q:${scope.q}` : scope.kind === "project" ? `p:${scope.p}` : "all";

  const meta = useAsync((s) => api.meta(s), []);
  const projects = useAsync((s) => api.projects(s), [rev]);
  const allQueries = useAsync((s) => api.queries(undefined, s), [rev]);
  const projectQueries = useAsync((s) => api.queries(sel.p ?? undefined, s), [sel.p, rev], sel.p !== null);
  const project = useAsync((s) => api.project(sel.p as number, s), [sel.p, rev], sel.p !== null);
  const tracking = useAsync(
    (s) =>
      api.tracking(sel.q as string, s).catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }),
    [sel.q, rev],
    sel.q !== null,
  );
  const summary = useAsync((s) => api.summary(scope, s), [scopeKey, rev]);

  const lookup = useMemo(() => makeSourceLookup(meta.data?.sources), [meta.data]);

  const projectName =
    sel.p === null
      ? null
      : (project.data?.name ?? projects.data?.find((p) => p.id === sel.p)?.name ?? `Project #${sel.p}`);

  const keywordOptions = sel.p !== null ? projectQueries.data : allQueries.data;

  const noKeywords = allQueries.data !== undefined && allQueries.data.length === 0;

  const onScanned = useCallback(
    (res: TrackResult) => {
      select({ p: sel.p, q: res.query });
      refresh();
    },
    [select, refresh, sel.p],
  );

  const title = sel.q ?? projectName ?? "All keywords";
  const subtitle = sel.q ? (projectName ? `keyword · ${projectName}` : "keyword") : sel.p !== null ? "project" : "every tracked keyword";

  return (
    <div className="app">
      <Header
        projects={projects}
        keywords={keywordOptions}
        keywordsLoading={sel.p !== null ? projectQueries.loading : allQueries.loading}
        keywordsError={sel.p !== null ? projectQueries.error : allQueries.error}
        sel={sel}
        onSelect={select}
      />

      <main className="layout">
        <aside className="side" aria-label="Controls">
          <ScanPanel
            meta={meta}
            selectedQuery={sel.q}
            tracking={tracking}
            projectId={sel.p}
            projectName={projectName}
            lookup={lookup}
            onScanned={onScanned}
          />
          <ProjectsPanel
            projects={projects}
            project={project}
            selectedId={sel.p}
            allQueries={allQueries.data ?? []}
            onSelect={(p) => select({ p, q: null })}
            onChanged={refresh}
          />
        </aside>

        <section className="board" aria-label="Dashboard">
          {noKeywords && (summary.data?.summary.total ?? 0) === 0 ? (
            <EmptyState />
          ) : (
            <>
              {summary.error && (
                <div className="panel">
                  <ErrorLine error={summary.error} onRetry={refresh} />
                </div>
              )}
              {!summary.data && summary.loading && (
                <div className="panel">
                  <Loading label="Loading summary" />
                </div>
              )}
              {summary.data && (
                <>
                  <div className="row-top">
                    <Overview
                      title={title}
                      subtitle={subtitle}
                      data={summary.data}
                      loading={summary.loading}
                    />
                    <VolumeChart points={summary.data.timeseries} />
                  </div>
                  <StatsRow data={summary.data} lookup={lookup} />
                  <Themes themes={summary.data.themes} />
                </>
              )}
              <MentionsFeed
                key={scopeKey}
                scope={scope}
                rev={rev}
                sourcesPresent={Object.keys(summary.data?.summary.by_source ?? {})}
                lookup={lookup}
              />
            </>
          )}
        </section>
      </main>

      <footer className="footer">
        <span>Radaro keeps its database on your machine — no telemetry.</span>
        <span className="mono">{meta.data ? `v${meta.data.version.replace(/^v/, "")}` : meta.error ? "version unavailable" : ""}</span>
      </footer>
    </div>
  );
}
