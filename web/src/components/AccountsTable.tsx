import { useState } from "react";
import { accountsApi, api, errorMessage } from "../api";
import type { Account, AccountStatus, AccountView, Platform } from "../types";
import { ago, fmtDateTime, relTime } from "../format";
import { SourceIcon, type BadgeSource } from "./SourceBadge";
import { ErrorLine } from "./Status";
import { AccountsLimitsForm } from "./AccountsLimitsForm";
import { Modal } from "./ui/Modal";

/** The icon for a publishing platform; Dev.to has no bundled logo. */
export function platformSource(name: string, label: string): BadgeSource {
  if (name === "devto") return { name, label, glyph: "D", color: "#3b49df" };
  return { name, label, glyph: label.slice(0, 1).toUpperCase(), color: "#8a949c" };
}

const STATUS: Record<AccountStatus, { label: string; cls: string; help: string }> = {
  live: { label: "live", cls: "b-ok", help: "Credentials work and the account is not suspended." },
  unknown: { label: "not checked", cls: "acc-b-unknown", help: "Not checked yet, or the last check could not reach the platform." },
  limited: { label: "rate-limited", cls: "b-mari", help: "The platform is rate-limiting this account." },
  invalid: { label: "invalid", cls: "acc-b-dead", help: "The platform rejects the credentials. Reconnect the account in Setup." },
  suspended: { label: "suspended", cls: "acc-b-dead", help: "The platform suspended or removed this account." },
};

function statusHelp(a: Account): string {
  const s = STATUS[a.status] ?? STATUS.unknown;
  const parts = [a.status_detail || s.help];
  if (a.status === "limited" && a.limited_until) parts.push(`Until ${fmtDateTime(a.limited_until)}.`);
  if (a.checked_at) parts.push(`Checked ${fmtDateTime(a.checked_at)}.`);
  return parts.join(" ");
}

/** An account's health badge, plus "paused" when it is paused. */
export function AccountBadges({ a }: Readonly<{ a: Account }>) {
  const s = STATUS[a.status] ?? STATUS.unknown;
  const help = statusHelp(a);
  return (
    <span className="acc-badges">
      <span className={`badge ${s.cls}`} title={help}>
        {s.label}
        <span className="sr-only">: {help}</span>
      </span>
      {a.paused && <span className="badge acc-b-paused">paused</span>}
    </span>
  );
}

function share(part: number, whole: number): string {
  return whole > 0 ? `${Math.round((part / whole) * 100)}%` : "—";
}

/** "in 2h" for a future time, "now" once it has passed. */
function until(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t) || t <= Date.now()) return "now";
  return `in ${relTime(new Date().toISOString(), t)}`;
}

function QuotaCell({ a }: Readonly<{ a: AccountView }>) {
  const q = a.quota;
  if (!q) return <span className="muted">—</span>;
  const next = q.next_at && Date.parse(q.next_at) > Date.now() ? q.next_at : null;
  // Not ready and no time that lifts it: only a person can (resume, reconnect).
  const held = !q.ready && !next;
  return (
    <span className="acc-cell-stack" title={q.reason || undefined}>
      <span className="num">
        <span className={q.remaining === 0 ? "neg" : undefined}>{q.remaining}</span>
        <span className="muted"> / {q.daily} left</span>
      </span>
      {next && <span className="muted small">next {until(next)}</span>}
      {held && <span className="muted small">on hold</span>}
    </span>
  );
}

interface Props {
  accounts: AccountView[];
  platforms: Platform[];
  onChanged: () => void;
}

const COLUMNS = 7;

type Busy = { id: number; action: "check" | "pause" | "delete" } | null;

export function AccountsTable({ accounts, platforms, onChanged }: Readonly<Props>) {
  const [busy, setBusy] = useState<Busy>(null);
  const [confirm, setConfirm] = useState<number | null>(null);
  const [limits, setLimits] = useState<AccountView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const label = (name: string) => platforms.find((p) => p.name === name)?.label ?? name;

  const run = async (id: number, action: NonNullable<Busy>["action"], fn: () => Promise<unknown>) => {
    setBusy({ id, action });
    setError(null);
    try {
      await fn();
      setConfirm(null);
      onChanged();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <>
      <ErrorLine error={error} />
      <div className="acc-table-wrap">
        <table className="acc-table">
          <thead>
            <tr>
              <th scope="col">Account</th>
              <th scope="col">Status</th>
              <th scope="col" className="acc-num">
                24h / 7d
              </th>
              <th scope="col">Quota today</th>
              <th scope="col" className="acc-num" title="Share of the last 30 days' publications removed by the platform or moderators">
                Removed
              </th>
              <th scope="col" className="acc-num" title="Share of the last 30 days' publish attempts that failed">
                Failed
              </th>
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          {accounts.map((a) => {
            const act = a.activity;
            const rowBusy = busy?.id === a.id;
            return (
              // One tbody per account: its row plus, when there is one, its last error.
              <tbody key={a.id} className={a.status === "invalid" || a.status === "suspended" ? "is-dead" : undefined}>
                <tr>
                  <th scope="row">
                    <span className="acc-who">
                      <SourceIcon source={platformSource(a.platform, label(a.platform))} size={24} />
                      <span className="acc-cell-stack">
                        <span className="strong acc-handle">{a.handle}</span>
                        <span className="muted small">{label(a.platform)}</span>
                      </span>
                    </span>
                  </th>
                  <td>
                    <span className="acc-cell-stack">
                      <AccountBadges a={a} />
                      <span className="muted small" title={a.checked_at ? fmtDateTime(a.checked_at) : undefined}>
                        {a.checked_at ? `checked ${ago(a.checked_at)}` : "never checked"}
                      </span>
                    </span>
                  </td>
                  <td className="acc-num num">
                    {act.published_24h} <span className="muted">/ {act.published_7d}</span>
                  </td>
                  <td>
                    <QuotaCell a={a} />
                  </td>
                  <td className="acc-num num" title={`${act.removed_30d} of ${act.published_30d} publications`}>
                    {share(act.removed_30d, act.published_30d)}
                  </td>
                  <td className="acc-num num" title={`${act.failed_30d} of ${act.published_30d + act.failed_30d} attempts`}>
                    {share(act.failed_30d, act.published_30d + act.failed_30d)}
                  </td>
                  <td>
                    {confirm === a.id ? (
                      <span className="acc-actions">
                        <button type="button" className="link-btn danger" disabled={rowBusy} onClick={() => void run(a.id, "delete", () => api.deleteAccount(a.id))}>
                          Disconnect {a.handle}?
                        </button>
                        <button type="button" className="link-btn quiet" disabled={rowBusy} onClick={() => setConfirm(null)}>
                          Keep
                        </button>
                      </span>
                    ) : (
                      <span className="acc-actions">
                        <button type="button" className="btn sm" disabled={rowBusy} onClick={() => void run(a.id, "check", () => accountsApi.check(a.id))}>
                          {rowBusy && busy?.action === "check" ? "Checking…" : "Check now"}
                        </button>
                        <button
                          type="button"
                          className="btn ghost sm"
                          disabled={rowBusy}
                          onClick={() => void run(a.id, "pause", () => accountsApi.update(a.id, { paused: !a.paused }))}
                        >
                          {a.paused ? "Resume" : "Pause"}
                        </button>
                        <button type="button" className="btn ghost sm" disabled={rowBusy} onClick={() => setLimits(a)}>
                          Limits
                        </button>
                        <button type="button" className="btn ghost danger sm" disabled={rowBusy} onClick={() => setConfirm(a.id)}>
                          Disconnect
                        </button>
                      </span>
                    )}
                  </td>
                </tr>
                {act.last_error && (
                  <tr className="acc-sub">
                    <td colSpan={COLUMNS}>
                      <span className="acc-error small" title={act.last_error}>
                        <span className="muted">Last error · </span>
                        {act.last_error}
                      </span>
                    </td>
                  </tr>
                )}
              </tbody>
            );
          })}
        </table>
      </div>

      {limits && (
        <Modal title={`Limits · ${limits.handle}`} onClose={() => setLimits(null)}>
          <AccountsLimitsForm
            account={limits}
            onDone={() => {
              setLimits(null);
              onChanged();
            }}
          />
        </Modal>
      )}
    </>
  );
}
