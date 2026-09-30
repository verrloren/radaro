import { useId, useState, type FormEvent } from "react";
import { accountsApi, errorMessage } from "../api";
import type { AccountView } from "../types";
import { ErrorLine } from "./Status";

interface FieldSpec {
  key: "daily" | "interval" | "cooldown";
  label: string;
  unit: string;
  max: number;
  /** The account's own value in the field's unit, or null for the platform default. */
  own: (a: AccountView) => number | null;
  /** The limit in effect, in the field's unit. */
  effective: (a: AccountView) => number;
}

const FIELDS: FieldSpec[] = [
  {
    key: "daily",
    label: "Publications per 24 hours",
    unit: "",
    max: 1000,
    own: (a) => a.daily_limit,
    effective: (a) => a.default_limits?.daily ?? a.limits.daily,
  },
  {
    key: "interval",
    label: "Minimum gap between publications",
    unit: "seconds",
    max: 7 * 24 * 3600,
    own: (a) => a.min_interval_sec,
    effective: (a) => a.default_limits?.min_interval_sec ?? a.limits.min_interval_sec,
  },
  {
    key: "cooldown",
    label: "Cooldown per community",
    unit: "hours",
    max: 90 * 24,
    own: (a) => a.community_cooldown_h,
    effective: (a) => a.default_limits?.community_cooldown_h ?? a.limits.community_cooldown_h,
  },
];

/** A blank field uses the platform default. */
function placeholder(f: FieldSpec, a: AccountView): string {
  const def = f.effective(a);
  return `default: ${def === 0 ? "none" : def}`;
}

/** Per-account publishing limits; a blank field uses the platform default. */
export function AccountsLimitsForm({ account, onDone }: Readonly<{ account: AccountView; onDone: () => void }>) {
  const id = useId();
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(FIELDS.map((f) => [f.key, f.own(account)?.toString() ?? ""])),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const parse = (f: FieldSpec): number | string => {
    const raw = (values[f.key] ?? "").trim();
    if (raw === "") return 0; // 0 = platform default
    if (!/^\d+$/.test(raw)) return `${f.label}: enter a whole number or leave it blank.`;
    const n = Number(raw);
    const unit = f.unit ? " " + f.unit : "";
    if (n > f.max) return `${f.label}: at most ${f.max}${unit}.`;
    return n;
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const parsed = FIELDS.map(parse);
    const bad = parsed.find((p) => typeof p === "string");
    if (typeof bad === "string") {
      setError(bad);
      return;
    }
    const [daily, interval, cooldown] = parsed as number[];
    setBusy(true);
    setError(null);
    try {
      await accountsApi.update(account.id, { daily_limit: daily, min_interval_sec: interval, community_cooldown_h: cooldown });
      onDone();
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  return (
    <form className="setup-form" onSubmit={(e) => void submit(e)} aria-label={`Limits for ${account.handle}`}>
      <p className="muted small">
        Radaro keeps each account under these limits when it publishes. Leave a field blank to use the platform default.
      </p>
      {FIELDS.map((f) => (
        <div className="field" key={f.key}>
          <label htmlFor={`${id}-${f.key}`} className="small strong">
            {f.label}
            {f.unit && <span className="muted"> · {f.unit}</span>}
          </label>
          <input
            id={`${id}-${f.key}`}
            type="text"
            inputMode="numeric"
            value={values[f.key] ?? ""}
            placeholder={placeholder(f, account)}
            onChange={(e) => setValues({ ...values, [f.key]: e.target.value })}
            disabled={busy}
            autoComplete="off"
          />
        </div>
      ))}
      <ErrorLine error={error} />
      <div className="btn-row">
        <button type="submit" className="btn primary sm" disabled={busy}>
          {busy ? "Saving…" : "Save limits"}
        </button>
        <button
          type="button"
          className="btn ghost sm"
          disabled={busy}
          onClick={() => setValues(Object.fromEntries(FIELDS.map((f) => [f.key, ""])))}
        >
          Use platform defaults
        </button>
      </div>
    </form>
  );
}
