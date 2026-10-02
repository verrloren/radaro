import { useState } from "react";
import { llmApi, errorMessage } from "../api";
import { useAsync } from "../hooks";
import type { LLMStatus } from "../types";
import { fmtDateTime } from "../format";
import { ErrorLine, Loading } from "./Status";

const labels = { not_configured: "Not configured", unchecked: "Configured · unchecked", connected: "Connected", error: "Connection error" };

export function LLMConnectionBadge({ status }: Readonly<{status: LLMStatus}>) {
  return <span className={`tag llm-${status.status}`}>LLM · {labels[status.status]}</span>;
}

export function LLMStatusCard({ isAdmin }: Readonly<{isAdmin: boolean}>) {
  const [rev, setRev] = useState(0);
  const status = useAsync((s) => llmApi.status(s), [rev]);
  const [checked, setChecked] = useState<LLMStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const current = checked ?? status.data;
  const check = async () => {
    setBusy(true); setError(null);
    try { setChecked(await llmApi.check()); }
    catch (e) { setError(errorMessage(e)); }
    finally { setBusy(false); }
  };
  return <section className="card pad llm-status-card" aria-label="LLM connection">
    <div className="section-head"><h2 className="card-title">LLM connection</h2>{current && <LLMConnectionBadge status={current} />}</div>
    {status.loading && !current && <Loading label="Checking LLM settings" />}
    <ErrorLine error={error ?? status.error} />
    {current && <>
      <p>{current.detail}</p>
      <p className="muted small">Provider: {current.provider} · Model: {current.model || "—"}{current.reasoning_effort && ` · Reasoning: ${current.reasoning_effort}`}</p>
      <p className="muted small">{current.checked_at ? `Last check: ${fmtDateTime(current.checked_at)}` : "No connection check yet."}</p>
      <div className="btn-row">
        {isAdmin && <button type="button" className="btn sm" disabled={busy || !current.configured} onClick={() => void check()}>{busy ? "Checking…" : "Check connection"}</button>}
        <button type="button" className="btn ghost sm" disabled={busy} onClick={() => { setChecked(null); setRev((v) => v + 1); }}>Refresh status</button>
      </div>
    </>}
  </section>;
}
