import { useEffect, useState } from "react";
import { api, draftsApi } from "../api";
import { useAsync } from "../hooks";
import { fmtNum } from "../format";
import type { DraftCounts, DraftStatus } from "../types";
import { DraftsEditor } from "./DraftsEditor";
import { DraftsList } from "./DraftsList";
import { ErrorLine, Loading } from "./Status";

interface Props {
  rev: number;
  onChanged: () => void;
}

type Tab = DraftStatus | "all";

type Notice = { text: string; url?: string };

const TABS: { tab: Tab; label: string }[] = [
  { tab: "draft", label: "To review" },
  { tab: "approved", label: "Approved" },
  { tab: "published", label: "Published" },
  { tab: "failed", label: "Failed" },
  { tab: "skipped", label: "Skipped" },
  { tab: "all", label: "All" },
];

function headline(total: number, toReview: number, approved: number): string {
  if (total === 0) return "No drafts yet.";
  if (toReview > 0) {
    const waits = toReview === 1 ? "draft waits" : "drafts wait";
    const ready = approved ? `, ${fmtNum(approved)} approved to publish.` : ".";
    return `${fmtNum(toReview)} ${waits} for your review${ready}`;
  }
  if (approved > 0) return `${fmtNum(approved)} approved ${approved === 1 ? "draft is" : "drafts are"} ready to publish.`;
  return "Nothing waits for review.";
}

/** Drafts: the publishing queue — review, approve, skip and publish. */
export function DraftsView({ rev, onChanged }: Readonly<Props>) {
  const [tab, setTab] = useState<Tab>("draft");
  const [selected, setSelected] = useState<number | null>(null);
  // A published draft leaves the Approved tab; this keeps its link in view.
  const [notice, setNotice] = useState<Notice | null>(null);

  // One state for both, so the list and the tab counts never disagree.
  const drafts = useAsync(
    (s) => Promise.all([draftsApi.list(tab === "all" ? undefined : tab, s), draftsApi.counts(s)]).then(([list, counts]) => ({ drafts: list, counts })),
    [tab, rev],
  );
  const accounts = useAsync((s) => api.accounts(s), [rev]);

  const counts = drafts.data?.counts ?? {};
  const total = Object.values(counts).reduce((a, n) => a + (n ?? 0), 0);

  // Keep a selection that is still in the list; otherwise open the first draft.
  useEffect(() => {
    const ds = drafts.data?.drafts;
    if (!ds) return;
    setSelected((cur) => (cur !== null && ds.some((d) => d.id === cur) ? cur : (ds[0]?.id ?? null)));
  }, [drafts.data]);

  const pickTab = (t: Tab) => {
    setTab(t);
    setNotice(null);
  };

  return (
    <>
      <header className="view-head">
        <p className="eyebrow">
          <span className="eyebrow-mark" aria-hidden="true" />
          {"Drafts · Nothing is published without your approval"}
        </p>
        <h1 className="headline">{drafts.data ? headline(total, counts.draft ?? 0, counts.approved ?? 0) : "Drafts"}</h1>
      </header>

      {drafts.error && (
        <div className="card pad">
          <ErrorLine error={drafts.error} onRetry={onChanged} />
        </div>
      )}
      {!drafts.data && drafts.loading && (
        <div className="card pad">
          <Loading label="Loading drafts" />
        </div>
      )}
      {drafts.data && total === 0 && <DraftsEmpty />}
      {drafts.data && total > 0 && (
        <>
          <DraftsTabs tab={tab} counts={counts} total={total} onTab={pickTab} />
          {notice && <DraftsNotice notice={notice} onDismiss={() => setNotice(null)} />}
          <div className="drafts-grid">
            <DraftsList
              drafts={drafts.data.drafts}
              platforms={accounts.data?.platforms ?? []}
              selected={selected}
              onSelect={setSelected}
              stale={drafts.loading}
            />
            {selected === null ? (
              <div className="card pad drafts-none">
                <p className="muted">No drafts here. Pick another tab.</p>
              </div>
            ) : (
              <DraftsEditor key={selected} id={selected} rev={rev} accounts={accounts.data} onChanged={onChanged} onNotice={setNotice} />
            )}
          </div>
        </>
      )}
    </>
  );
}

interface TabsProps {
  tab: Tab;
  counts: DraftCounts;
  total: number;
  onTab: (tab: Tab) => void;
}

function DraftsTabs({ tab, counts, total, onTab }: Readonly<TabsProps>) {
  return (
    <div className="chips" role="tablist" aria-label="Filter drafts by status">
      {TABS.map(({ tab: t, label }) => (
        <button
          key={t}
          type="button"
          role="tab"
          aria-selected={tab === t}
          className={`chip${tab === t ? " is-on" : ""}`}
          onClick={() => onTab(t)}
        >
          {label}
          <span className="n">{fmtNum(t === "all" ? total : (counts[t] ?? 0))}</span>
        </button>
      ))}
    </div>
  );
}

function DraftsNotice({ notice, onDismiss }: Readonly<{ notice: Notice; onDismiss: () => void }>) {
  return (
    <output className="ok-line drafts-notice">
      ✓ {notice.text}
      {notice.url && (
        <>
          {" "}
          <a className="link" href={notice.url} target="_blank" rel="noopener noreferrer">
            {"View it"}
            <span className="sr-only"> (opens in a new tab)</span>
          </a>
        </>
      )}
      <button type="button" className="icon-btn" aria-label="Dismiss" onClick={onDismiss}>
        ×
      </button>
    </output>
  );
}

function DraftsEmpty() {
  return (
    <section className="card pad empty" aria-labelledby="drafts-empty-h">
      <span className="tri" aria-hidden="true">
        <i />
        <i />
        <i />
      </span>
      <h2 id="drafts-empty-h" className="empty-title">
        no drafts to review
      </h2>
      <p className="muted">
        Your coding agent writes drafts with the Radaro skill; you review, approve and publish them here. Nothing goes
        out until you approve it.
      </p>
      <ul className="empty-steps">
        <li>
          <span className="step">
            <b>1</b>
          </span>
          <span>
            Find threads worth answering: <code>radaro opportunities</code>
          </span>
        </li>
        <li>
          <span className="step">
            <b>2</b>
          </span>
          <span>
            Draft a reply or a post: <code>radaro draft add --platform reddit --mention &lt;id&gt; --body-file reply.md</code>
          </span>
        </li>
        <li>
          <span className="step">
            <b>3</b>
          </span>
          <span>Come back here to edit, approve and publish it.</span>
        </li>
      </ul>
    </section>
  );
}
