import { useId, useState, type ChangeEvent, type FormEvent } from "react";
import { api, errorMessage } from "../api";
import type { AccountImportResult } from "../types";
import { ErrorLine } from "./Status";

/** Counts CSV records without treating a newline inside a quoted field as a row. */
function csvRows(text: string): number {
  let quoted = false;
  let records = 0;
  let first = "";
  let record = "";
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === '"') {
      if (quoted && text[i + 1] === '"') {
        record += '"';
        i++;
      } else {
        quoted = !quoted;
      }
    } else if ((c === "\n" || c === "\r") && !quoted) {
      if (record.trim()) {
        if (!first) first = record;
        records++;
      }
      record = "";
      if (c === "\r" && text[i + 1] === "\n") i++;
    } else {
      record += c;
    }
  }
  if (record.trim()) {
    if (!first) first = record;
    records++;
  }
  const fields = first.split(",").map((field) => field.trim().toLowerCase());
  const header = fields.includes("platform") && fields.every((field) => ["platform", "handle", "secret", "instance"].includes(field));
  return Math.max(0, records - (header ? 1 : 0));
}

export function importRowCount(text: string): number {
  if (!text.trim()) return 0;
  if (text.trimStart().startsWith("[")) {
    try {
      const rows: unknown = JSON.parse(text);
      return Array.isArray(rows) ? rows.length : 0;
    } catch {
      return 0;
    }
  }
  return csvRows(text);
}

/** Imports a CSV or JSON batch; credentials never appear in the results. */
export function AccountImportForm({ onChanged }: Readonly<{ onChanged: () => void }>) {
  const id = useId();
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<AccountImportResult | null>(null);
  const count = importRowCount(value);

  const fileSelected = async (e: ChangeEvent<HTMLInputElement>) => {
    const input = e.currentTarget;
    const file = input.files?.[0];
    if (!file) return;
    setError(null);
    setResult(null);
    try {
      setValue(await file.text());
    } catch (err) {
      setError(`Could not read the file: ${errorMessage(err)}`);
    }
    input.value = "";
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!value.trim()) return;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const imported = await api.importAccounts(value);
      setResult(imported);
      if (imported.errors === 0) setValue("");
      if (imported.added + imported.updated > 0) onChanged();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="setup-form" onSubmit={(e) => void submit(e)} aria-label="Import accounts">
      <p className="muted small">CSV with an optional <code>platform,handle,secret,instance</code> header, or a JSON array with those fields. Up to 500 rows. Use “Add another Reddit account” for Reddit OAuth.</p>
      <div className="field">
        <label htmlFor={`${id}-file`} className="small strong">Choose a CSV or JSON file</label>
        <input id={`${id}-file`} type="file" accept=".csv,.json,text/csv,application/json,text/plain" onChange={(e) => void fileSelected(e)} disabled={busy} />
      </div>
      <div className="field">
        <label htmlFor={`${id}-text`} className="small strong">Or paste account rows</label>
        <textarea id={`${id}-text`} rows={8} value={value} onChange={(e) => { setValue(e.target.value); setResult(null); }} disabled={busy} spellCheck={false} autoComplete="off" />
      </div>
      {value.trim() && <p className="muted small" role="status">{count} row{count === 1 ? "" : "s"} ready to import{count > 500 ? " · maximum 500" : ""}.</p>}
      <ErrorLine error={error} />
      <button type="submit" className="btn primary sm" disabled={busy || !value.trim() || count === 0 || count > 500}>{busy ? "Importing…" : `Import ${count} account${count === 1 ? "" : "s"}`}</button>
      {result && (
        <div className="import-results" role="status">
          <p className="strong small">{result.added} added · {result.updated} updated · {result.errors} errors</p>
          <div className="acc-table-wrap">
            <table className="acc-table">
              <thead><tr><th scope="col">Row</th><th scope="col">Platform</th><th scope="col">Handle</th><th scope="col">Result</th></tr></thead>
              <tbody>{result.results.map((r) => <tr key={r.row}><th scope="row">{r.row}</th><td>{r.platform}</td><td>{r.handle}</td><td>{r.error ?? r.status}</td></tr>)}</tbody>
            </table>
          </div>
        </div>
      )}
    </form>
  );
}
