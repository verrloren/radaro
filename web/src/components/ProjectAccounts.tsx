import { useState } from "react";
import { api, errorMessage } from "../api";
import { useAsync } from "../hooks";
import type { AccountView, Platform, ProjectPool } from "../types";
import { AccountBadges } from "./AccountsTable";
import { ErrorLine, Loading } from "./Status";
import { Select } from "./ui/Select";

interface Props {
  projectId: number;
  projectName: string;
  /** Every account the user has connected. */
  own: AccountView[];
  ownError: string | null;
  rev: number;
  onConnect: (platform: Platform) => void;
  onChanged: () => void;
}

/**
 * The project's account pool: several accounts per platform. Publishing picks
 * the best one of the pool; with an empty pool, any of the user's accounts.
 */
export function ProjectAccounts({ projectId, projectName, own, ownError, rev, onConnect, onChanged }: Readonly<Props>) {
  const pools = useAsync((s) => api.projectAccounts(projectId, s), [projectId, rev]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      onChanged();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="project-block">
      <h4 className="block-title">Accounts</h4>
      <p className="muted small">
        The accounts this project publishes with — and, for Reddit and Mastodon, scans with. Publishing picks the healthiest account with quota left; an empty
        pool uses any of your accounts on that platform.
      </p>
      <ErrorLine error={pools.error ?? ownError} />
      {pools.loading && !pools.data && <Loading label="Loading accounts" />}
      <ul className="binding-list">
        {(pools.data ?? []).map((pool) => (
          <PoolRow
            key={pool.platform.name}
            pool={pool}
            projectName={projectName}
            own={own}
            busy={busy}
            onAdd={(id) => void act(() => api.bindAccount(projectId, pool.platform.name, id))}
            onRemove={(id) => void act(() => api.unbindProjectAccount(projectId, pool.platform.name, id))}
            onClear={() => void act(() => api.unbindAccount(projectId, pool.platform.name))}
            onConnect={() => onConnect(pool.platform)}
          />
        ))}
      </ul>
      <ErrorLine error={error} />
    </div>
  );
}

interface RowProps {
  pool: ProjectPool;
  projectName: string;
  own: AccountView[];
  busy: boolean;
  onAdd: (accountId: number) => void;
  onRemove: (accountId: number) => void;
  onClear: () => void;
  onConnect: () => void;
}

function PoolRow({ pool, projectName, own, busy, onAdd, onRemove, onClear, onConnect }: Readonly<RowProps>) {
  const { platform, accounts } = pool;
  const pooled = new Set(accounts.map((a) => a.id));
  const candidates = own.filter((a) => a.platform === platform.name && !pooled.has(a.id));
  const hasOwn = own.some((a) => a.platform === platform.name);
  const emptyNote = hasOwn ? `Any of your ${platform.label} accounts` : `No ${platform.label} account yet`;

  return (
    <li className="binding-row pool-row">
      <span className="strong small binding-name">{platform.label}</span>
      <span className="pool-body">
        {accounts.length > 0 ? (
          <ul className="pool-list" aria-label={`${platform.label} accounts in ${projectName}`}>
            {accounts.map((a) => (
              <li key={a.id} className="pool-item">
                <span className="pool-handle">{a.handle}</span>
                <AccountBadges a={a} />
                <button
                  type="button"
                  className="icon-btn"
                  aria-label={`Remove ${a.handle} from ${projectName}`}
                  title="Remove from this project (the account stays connected)"
                  disabled={busy}
                  onClick={() => onRemove(a.id)}
                >
                  ×
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <span className="muted small">{emptyNote}</span>
        )}
        {candidates.length > 0 && (
          <Select
            label={`Add a ${platform.label} account to ${projectName}`}
            value=""
            disabled={busy}
            onChange={(v) => {
              if (v) onAdd(Number(v));
            }}
            options={[{ value: "", label: "Add an account…" }, ...candidates.map((a) => ({ value: String(a.id), label: a.handle }))]}
          />
        )}
      </span>
      <span className="pool-actions">
        {accounts.length > 1 && (
          <button type="button" className="btn ghost sm" disabled={busy} onClick={onClear} aria-label={`Clear ${platform.label} accounts of ${projectName}`}>
            Clear
          </button>
        )}
        <button type="button" className="btn ghost sm" disabled={busy} onClick={onConnect}>
          Connect new
        </button>
      </span>
    </li>
  );
}
