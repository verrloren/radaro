const DAY = 86_400_000;

export const nf = new Intl.NumberFormat("en-US");

export function fmtNum(n: number): string {
  return nf.format(n);
}

export function fmtCompact(n: number): string {
  return new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 }).format(n);
}

/** Net sentiment in [-1, 1] as a signed percent, e.g. "+23%", "−8%", "0%". */
export function fmtNet(net: number): string {
  const pct = Math.round(net * 100);
  if (pct > 0) return `+${pct}%`;
  if (pct < 0) return `−${Math.abs(pct)}%`;
  return "0%";
}

/** Compact relative time: "now", "12m", "5h", "3d", "4mo", "2y". */
export function relTime(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "—";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return "now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  const d = Math.floor(h / 24);
  if (d < 45) return `${d}d`;
  const mo = Math.floor(d / 30.44);
  if (mo < 12) return `${mo}mo`;
  return `${Math.floor(d / 365.25)}y`;
}

/** "just now" / "3h ago". */
export function ago(iso: string | null | undefined): string {
  const r = relTime(iso);
  return r === "now" ? "just now" : r === "—" ? r : `${r} ago`;
}

export function fmtDateTime(iso: string | null | undefined): string {
  if (!iso) return "";
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return iso;
  return t.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** "YYYY-MM-DD" -> UTC epoch ms at midnight. */
export function parseDay(d: string): number {
  const [y, m, day] = d.split("-").map(Number);
  return Date.UTC(y, (m ?? 1) - 1, day ?? 1);
}

export function dayKey(ms: number): string {
  return new Date(ms).toISOString().slice(0, 10);
}

export function addDays(ms: number, n: number): number {
  return ms + n * DAY;
}

export function daysBetween(a: number, b: number): number {
  return Math.round((b - a) / DAY);
}

const shortFmt = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
const longFmt = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric", timeZone: "UTC" });

export function fmtDay(ms: number, withYear = false): string {
  return (withYear ? longFmt : shortFmt).format(ms);
}

export function fmtDayRange(a: number, b: number): string {
  if (a === b) return fmtDay(a, true);
  const sameYear = new Date(a).getUTCFullYear() === new Date(b).getUTCFullYear();
  return `${fmtDay(a, !sameYear)} – ${fmtDay(b, true)}`;
}
