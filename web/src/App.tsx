import { useCallback, useEffect, useMemo, useState } from "react";
import { api, ApiError } from "./api";
import { useAsync } from "./hooks";
import { makeSourceLookup } from "./sources";
import type { Scope, TrackResult } from "./types";
import { Rail, type View } from "./components/Rail";
import { ScanCard } from "./components/ScanCard";
import { Report } from "./components/Report";
import { SentimentCard } from "./components/SentimentCard";
import { VolumeChart } from "./components/VolumeChart";
import { Themes } from "./components/Themes";
import { MentionsFeed } from "./components/MentionsFeed";
import { EmptyState } from "./components/EmptyState";
import { SourcesCard } from "./components/SourcesCard";
import { ProjectsPanel } from "./components/ProjectsPanel";
import { ErrorLine, Loading } from "./components/Status";

export interface Selection {
  q: string | null;
  p: number | null;
  v: View;
}

const VIEWS: readonly View[] = ["listen", "scan", "setup"];

function readUrl(): Selection {
  const sp = new URLSearchParams(window.location.search);
  const q = sp.get("q")?.trim() || null;
  const pRaw = sp.get("p");
  const pNum = pRaw ? Number.parseInt(pRaw, 10) : NaN;
  return { q, p: Number.isFinite(pNum) && pNum > 0 ? pNum : null, v: VIEWS.includes(sp.get("v") as View) ? (sp.get("v") as View) : "listen" };
}

function writeUrl(sel: Selection) {
  const sp = new URLSearchParams(window.location.search);
  sp.delete("q");
  sp.delete("p");
  sp.delete("v");
  if (sel.p !== null) sp.set("p", String(sel.p));
  if (sel.q) sp.set("q", sel.q);
  if (sel.v !== "listen") sp.set("v", sel.v);
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
  const go = useCallback((v: View) => select({ ...sel, v }), [select, sel]);

  // A keyword narrows the view; otherwise the project; otherwise everything.
  const scope: Scope = useMemo(
    () => (sel.q ? { kind: "query", q: sel.q } : sel.p !== null ? { kind: "project", p: sel.p } : { kind: "all" }),
    [sel.q, sel.p],
  );
  const scopeKey = scope.kind === "query" ? `q:${scope.q}` : scope.kind === "project" ? `p:${scope.p}` : "all";

  // The by-source rows and the feed share one source filter; a new scope clears it.
  const [source, setSource] = useState<string | null>(null);
  useEffect(() => setSource(null), [scopeKey]);

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
  const needSetup = (meta.data?.sources ?? []).filter((s) => s.needs_config && !s.configured).length;

  const onScanned = useCallback(
    (res: TrackResult) => {
      select({ p: sel.p, q: res.query, v: "scan" });
      refresh();
    },
    [select, refresh, sel.p],
  );

  const subject = sel.q ?? projectName ?? "Your keywords";
  const crumb = `${projectName ?? "All projects"} / ${sel.q ?? (sel.p !== null ? "Whole project" : "Every keyword")}`;
  const empty = noKeywords && (summary.data?.summary.total ?? 0) === 0;

  return (
    <div className="app">
      <Rail
        projects={projects}
        keywords={keywordOptions}
        keywordsLoading={sel.p !== null ? projectQueries.loading : allQueries.loading}
        keywordsError={sel.p !== null ? projectQueries.error : allQueries.error}
        sel={sel}
        onSelect={select}
        view={sel.v}
        onView={go}
        total={summary.data?.summary.total}
        needSetup={needSetup}
        meta={meta}
      />

      <main className="main">
        <p className="crumb">{crumb}</p>

        {sel.v === "listen" ? (
          <section className="view" aria-label="Listen" key="listen">
            {empty ? (
              <EmptyState onScan={() => go("scan")} />
            ) : (
              <>
                {summary.error && (
                  <div className="card pad">
                    <ErrorLine error={summary.error} onRetry={refresh} />
                  </div>
                )}
                {!summary.data && summary.loading && (
                  <div className="card pad">
                    <Loading label="Loading summary" />
                  </div>
                )}
                {summary.data && (
                  <>
                    <Report subject={subject} data={summary.data} loading={summary.loading} />
                    <div className="grid-3">
                      <SentimentCard data={summary.data} lookup={lookup} source={source} onSource={setSource} />
                      <VolumeChart points={summary.data.timeseries} />
                      <Themes themes={summary.data.themes} />
                    </div>
                  </>
                )}
                <MentionsFeed
                  key={scopeKey}
                  scope={scope}
                  rev={rev}
                  summary={summary.data?.summary}
                  lookup={lookup}
                  source={source}
                  onSource={setSource}
                />
              </>
            )}
          </section>
        ) : sel.v === "scan" ? (
          <section className="view" aria-label="Scan" key="scan">
            <ScanCard
              meta={meta}
              selectedQuery={sel.q}
              tracking={tracking}
              projectId={sel.p}
              projectName={projectName}
              lookup={lookup}
              onScanned={onScanned}
              onSetup={() => go("setup")}
              onListen={() => go("listen")}
            />
          </section>
        ) : (
          <section className="view" aria-label="Setup" key="setup">
            <header className="view-head">
              <p className="eyebrow">
                <span className="eyebrow-mark" aria-hidden="true" />
                Setup · Sources and projects
              </p>
              <h1 className="headline">
                {meta.data
                  ? `${meta.data.sources.length - needSetup} of ${meta.data.sources.length} sources are ready${needSetup ? ` — ${needSetup} need setup.` : "."}`
                  : "Sources and projects"}
              </h1>
            </header>
            <div className="grid-setup">
              <SourcesCard meta={meta} tracking={sel.q ? tracking : undefined} lookup={lookup} />
              <ProjectsPanel
                projects={projects}
                project={project}
                selectedId={sel.p}
                allQueries={allQueries.data ?? []}
                onSelect={(p) => select({ p, q: null, v: sel.v })}
                onChanged={refresh}
              />
            </div>
          </section>
        )}
      </main>
    </div>
  );
}
