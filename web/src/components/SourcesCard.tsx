import { useState } from "react";
import { api, errorMessage } from "../api";
import type { AsyncState } from "../hooks";
import type { SourceLookup } from "../sources";
import type { Account, AccountsResponse, Meta, Platform, SourceInfo, SourceSettings, Tracking } from "../types";
import { ago, fmtDateTime } from "../format";
import { SourceIcon } from "./SourceBadge";
import { ErrorLine, Loading } from "./Status";
import { ConnectForm, RedditForm, SourceForm } from "./ConnectForms";
import { Modal } from "./ui/Modal";

// What each listen-only source does once ready, and where its keys come from.
const HOW: Record<string, { ready: string; setup?: string }> = {
  hackernews: { ready: "Public API — works out of the box, no key needed." },
  rss: { ready: "Watches the feeds you listed.", setup: "Add the feed URLs to watch." },
  stackoverflow: { ready: "Public API — anonymous quota, no key needed." },
  x: { ready: "Searches recent posts with your bearer token.", setup: "Paste a bearer token from an X developer account (paid API)." },
  youtube: { ready: "Searches videos with your API key.", setup: "Paste a Google Cloud API key with the YouTube Data API v3 enabled." },
};

/** One platform: a scan source, a publishing platform, or both. */
interface Row {
  name: string;
  label: string;
  source?: SourceInfo;
  platform?: Platform;
  settings?: SourceSettings;
  accounts: Account[];
}

type Dialog = { kind: "source"; settings: SourceSettings } | { kind: "account"; platform: Platform };

function describe(r: Row): string {
  const connected = r.accounts.length > 0;
  if (!r.source) return connected ? "Publishes as your connected account." : "Posting only: connect an account to publish.";
  const ready = !r.source.needs_config || r.source.configured;
  if (r.platform && r.source.needs_config) {
    // Reddit, Mastodon: the account both scans and posts.
    if (connected) return "Scans and posts as your connected account.";
    if (ready) return "Scanning is configured from .env. Connect an account to post, and to scan with it instead.";
    return "Connect your account — the same connection scans and posts.";
  }
  if (r.platform) return connected ? "Public search to listen; posts as your connected account." : "Public search, no login needed to listen. Connect an account to post.";
  const how = HOW[r.name];
  if (how) return ready ? how.ready : (how.setup ?? how.ready);
  return ready ? "Ready to scan." : "Needs credentials.";
}

interface Props {
  /** Source keys apply to the whole server, so only the admin edits them. */
  isAdmin: boolean;
  meta: AsyncState<Meta>;
  settings: AsyncState<SourceSettings[]>;
  accounts: AsyncState<AccountsResponse>;
  notice: { ok: boolean; text: string } | null;
  tracking: AsyncState<Tracking | null> | undefined;
  lookup: SourceLookup;
  onChanged: () => void;
}

export function SourcesCard({ isAdmin, meta, settings, accounts, notice, tracking, lookup, onChanged }: Props) {
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [confirm, setConfirm] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const states = new Map((tracking?.data?.source_states ?? []).map((st) => [st.source, st]));
  const q = tracking?.data?.query;
  const used = new Set(tracking?.data?.sources ?? []);
  const scannedAt = tracking?.data?.last_scanned_at ?? null;

  const bySettings = new Map((settings.data ?? []).map((s) => [s.name, s]));
  const platforms = accounts.data?.platforms ?? [];
  const accountList = accounts.data?.accounts ?? [];
  const sources = meta.data?.sources ?? [];
  const rowFor = (name: string, label: string, source?: SourceInfo): Row => ({
    name,
    label,
    source,
    platform: platforms.find((p) => p.name === name),
    settings: bySettings.get(name),
    accounts: accountList.filter((a) => a.platform === name),
  });
  const rows: Row[] = [
    ...sources.map((s) => rowFor(s.name, s.label, s)),
    ...platforms.filter((p) => !sources.some((s) => s.name === p.name)).map((p) => rowFor(p.name, p.label)),
  ];
  const icon = (name: string) => (name === "devto" ? { name, label: "Dev.to", glyph: "D", color: "#3b49df" } : lookup(name));

  const disconnect = async (id: number) => {
    setBusy(true);
    setError(null);
    try {
      await api.deleteAccount(id);
      setConfirm(null);
      onChanged();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const done = () => {
    setDialog(null);
    onChanged();
  };

  return (
    <section className="card pad" aria-labelledby="sources-h">
      <div className="card-head">
        <h2 id="sources-h" className="card-title lg">
          Sources and accounts
        </h2>
        <span className="muted small">{q ? `last scans for “${q}”` : "Scans run on the server"}</span>
      </div>
      <p className="muted small">
        Your accounts are yours alone; a Reddit or Mastodon account both scans and posts, and each project picks which account it uses. Source keys (RSS, X, YouTube)
        apply to the whole server{isAdmin ? "" : " and are set by its admin"}. Nothing is posted without your approval.
      </p>
      {notice && (notice.ok ? <p className="ok-line" role="status">✓ {notice.text}</p> : <ErrorLine error={notice.text} />)}
      {meta.loading && !meta.data && <Loading label="Loading sources" />}
      <ErrorLine error={meta.error ?? settings.error ?? accounts.error ?? error} />
      <ul className="source-list">
        {rows.map((r) => {
          const ready = r.source ? !r.source.needs_config || r.source.configured : r.accounts.length > 0;
          const st = states.get(r.name);
          const cfg = r.settings;
          return (
            <li key={r.name} className={`source-row${ready ? "" : " is-off"}`}>
              <SourceIcon source={icon(r.name)} size={28} />
              <span className="source-text">
                <span className="strong">
                  {r.label}
                  {cfg?.saved && <span className="muted small"> · saved in Radaro</span>}
                  {cfg && !cfg.saved && cfg.configured && <span className="muted small"> · from .env</span>}
                </span>
                <span className="muted small">{describe(r)}</span>
                {r.accounts.map((a) => (
                  <span key={a.id} className="account-line small">
                    <span>
                      Account: <span className="strong">{a.handle}</span>
                    </span>
                    {confirm === a.id ? (
                      <>
                        <button type="button" className="link-btn danger" disabled={busy} onClick={() => void disconnect(a.id)}>
                          Disconnect {a.handle}?
                        </button>
                        <button type="button" className="link-btn quiet" disabled={busy} onClick={() => setConfirm(null)}>
                          Keep
                        </button>
                      </>
                    ) : (
                      <button type="button" className="link-btn quiet" onClick={() => setConfirm(a.id)}>
                        Disconnect
                      </button>
                    )}
                  </span>
                ))}
              </span>
              <span className="source-actions">
                {r.source ? (
                  <span className={`badge ${ready ? "b-ok" : "b-mari"}`}>{ready ? "configured" : "needs setup"}</span>
                ) : (
                  r.accounts.length > 0 && <span className="badge b-ok">connected</span>
                )}
                {cfg ? (
                  isAdmin && (
                    <button type="button" className="btn sm" onClick={() => setDialog({ kind: "source", settings: cfg })}>
                      {ready ? "Edit" : "Set up"}
                    </button>
                  )
                ) : (
                  r.platform && (
                    <button type="button" className="btn sm" onClick={() => setDialog({ kind: "account", platform: r.platform as Platform })}>
                      {r.accounts.length ? "Add account" : "Connect account"}
                    </button>
                  )
                )}
              </span>
              {q && used.has(r.name) && (
                <span className="source-last small" title={st?.last_error ?? fmtDateTime(st?.last_success_at ?? scannedAt)}>
                  <span className={`dot ${st?.last_error ? "tone-error" : st?.last_success_at ? "tone-ok" : "tone-none"}`} aria-hidden="true" />
                  {st?.last_error
                    ? `last scan error: ${st.last_error}`
                    : st?.last_success_at
                      ? `ok · ${ago(st.last_success_at)}`
                      : scannedAt
                        ? `scanned ${ago(scannedAt)}`
                        : "not scanned yet"}
                </span>
              )}
            </li>
          );
        })}
      </ul>

      {dialog?.kind === "source" && (
        <Modal title={`${dialog.settings.label} keys`} onClose={() => setDialog(null)}>
          <SourceForm source={dialog.settings} onDone={done} />
        </Modal>
      )}
      {dialog?.kind === "account" && (
        <Modal title={`Connect ${dialog.platform.label}`} onClose={() => setDialog(null)}>
          {dialog.platform.name === "reddit" ? <RedditForm /> : <ConnectForm platform={dialog.platform} onDone={done} />}
        </Modal>
      )}
    </section>
  );
}
