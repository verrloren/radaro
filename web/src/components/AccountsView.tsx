import { useState } from "react";
import { accountsApi, api, errorMessage } from "../api";
import { useAsync } from "../hooks";
import type { AccountStats, AccountTally, Platform } from "../types";
import { fmtNum } from "../format";
import { AccountsBanChart } from "./AccountsBanChart";
import { AccountsTable, platformSource } from "./AccountsTable";
import { SourceIcon } from "./SourceBadge";
import { ErrorLine, Loading } from "./Status";

interface Props {
  rev: number;
  onChanged: () => void;
  onSetup: () => void;
}

const pct = (r: number) => `${Math.round(r * 1000) / 10}%`;

function Breakdown({ stats, platforms }: Readonly<{ stats: AccountStats; platforms: Platform[] }>) {
  const label = (name: string) => platforms.find((p) => p.name === name)?.label ?? name;
  const rows: AccountTally[] = stats.platforms;
  return (
    <section className="card pad" aria-labelledby="acc-by-h">
      <div className="card-head">
        <h2 className="card-title" id="acc-by-h">
          By platform
        </h2>
        <span className="muted small">now</span>
      </div>
      <ul className="acc-by">
        {rows.map((t) => {
          const name = t.platform ?? "";
          return (
            <li key={name} className="acc-by-row">
              <SourceIcon source={platformSource(name, label(name))} size={22} />
              <span className="acc-cell-stack">
                <span className="strong">{label(name)}</span>
                <span className="muted small">
                  {fmtNum(t.live)} live · {fmtNum(t.dead)} dead · {fmtNum(t.limited)} limited
                  {t.paused > 0 && ` · ${fmtNum(t.paused)} paused`}
                  {t.unknown > 0 && ` · ${fmtNum(t.unknown)} not checked`}
                </span>
              </span>
              <span className="acc-by-rate num">
                <span className={t.dead > 0 ? "neg" : undefined}>{pct(t.ban_rate)}</span>
                <span className="muted small">
                  {fmtNum(t.dead)} of {fmtNum(t.total)}
                </span>
              </span>
              <span className="acc-meter" aria-hidden="true">
                <span style={{ width: `${Math.min(100, t.ban_rate * 100)}%` }} />
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/** Accounts: health, limits and ban rate of every publishing account. */
export function AccountsView({ rev, onChanged, onSetup }: Readonly<Props>) {
  const list = useAsync((s) => api.accounts(s), [rev]);
  const stats = useAsync((s) => accountsApi.stats(30, s), [rev]);
  const [checking, setChecking] = useState(false);
  const [checkError, setCheckError] = useState<string | null>(null);

  const accounts = list.data?.accounts ?? [];
  const platforms = list.data?.platforms ?? [];
  const total = stats.data?.total;
  const heldBack = accounts.filter((a) => a.paused || a.status === "limited").length;

  const checkAll = async () => {
    setChecking(true);
    setCheckError(null);
    try {
      const res = await accountsApi.checkAll();
      if (res.error) setCheckError(`Some checks could not run: ${res.error}`);
      onChanged();
    } catch (e) {
      setCheckError(errorMessage(e));
    } finally {
      setChecking(false);
    }
  };

  if (!list.data && list.loading) {
    return (
      <div className="card pad">
        <Loading label="Loading accounts" />
      </div>
    );
  }
  if (!list.data) {
    return (
      <div className="card pad">
        <ErrorLine error={list.error} onRetry={onChanged} />
      </div>
    );
  }

  if (accounts.length === 0) {
    return (
      <section className="card pad empty" aria-labelledby="acc-empty-h">
        <span className="tri" aria-hidden="true">
          <i />
          <i />
          <i />
        </span>
        <h2 id="acc-empty-h" className="empty-title">
          no publishing accounts yet
        </h2>
        <p className="muted">
          Connect a Reddit, Bluesky, Mastodon or Dev.to account in{" "}
          <button type="button" className="link-btn" onClick={onSetup}>
            Setup
          </button>
          {". Radaro then checks its health, keeps it within its limits and tracks how often accounts get banned."}
        </p>
      </section>
    );
  }

  const n = accounts.length;
  const live = total?.live ?? accounts.filter((a) => a.status === "live").length;
  const dead = total?.dead ?? accounts.filter((a) => a.status === "invalid" || a.status === "suspended").length;
  const tiles = [
    { key: "live", label: "Live", value: fmtNum(live), note: `of ${fmtNum(n)} account${n === 1 ? "" : "s"}` },
    { key: "dead", label: "Dead", value: fmtNum(dead), note: "invalid or suspended" },
    { key: "held", label: "Limited or paused", value: fmtNum(heldBack), note: "not publishing right now" },
    { key: "rate", label: "Ban rate", value: total ? pct(total.ban_rate) : "—", note: "dead of all accounts" },
  ];

  return (
    <>
      <header className="view-head">
        <p className="eyebrow">
          <span className="eyebrow-mark" aria-hidden="true" />
          {"Accounts · Health, limits and ban rate"}
        </p>
        <h1 className="headline">
          {live === n ? (
            <>
              All {n === 1 ? "" : `${fmtNum(n)} `}account{n === 1 ? " is" : "s are"} <span className="hl">live</span>.
            </>
          ) : (
            <>
              {fmtNum(live)} of {fmtNum(n)} accounts {live === 1 ? "is" : "are"} live
              {dead > 0 && (
                <>
                  {" "}
                  — <span className="neg">{fmtNum(dead)} dead</span>
                </>
              )}
              .
            </>
          )}
        </h1>
        <dl className="stats acc-stats">
          {tiles.map((t) => (
            <div key={t.key} className="stat">
              <dt>{t.label}</dt>
              <dd className={`stat-value${t.key === "dead" && dead > 0 ? " neg" : ""}`}>{t.value}</dd>
              <dd className="stat-note">{t.note}</dd>
            </div>
          ))}
        </dl>
      </header>

      <ErrorLine error={stats.error} onRetry={onChanged} />
      {stats.data && (
        <div className="acc-grid">
          <AccountsBanChart series={stats.data.series} platforms={platforms} />
          <Breakdown stats={stats.data} platforms={platforms} />
        </div>
      )}

      <section className="card pad" aria-labelledby="acc-list-h">
        <div className="card-head">
          <h2 className="card-title lg" id="acc-list-h">
            Accounts
          </h2>
          <span className="acc-head-actions">
            <button type="button" className="btn ghost sm" onClick={onSetup}>
              Add account
            </button>
            <button type="button" className="btn sm" disabled={checking} onClick={() => void checkAll()}>
              {checking ? "Checking…" : "Check all"}
            </button>
          </span>
        </div>
        <p className="muted small">
          Counts cover the last 30 days. Quota is what the account may still publish in the next 24 hours under its limits.
        </p>
        <ErrorLine error={checkError ?? list.error} />
        <AccountsTable accounts={accounts} platforms={platforms} onChanged={onChanged} />
      </section>
    </>
  );
}
