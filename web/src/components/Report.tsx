import type { SummaryResponse } from "../types";
import { daysBetween, fmtDayRange, fmtNet, fmtNum, parseDay } from "../format";

interface Props {
  subject: string;
  data: SummaryResponse;
  loading: boolean;
}

function mood(pct: number): string {
  if (pct <= -15) return "clearly negative";
  if (pct < 0) return "slightly negative";
  if (pct === 0) return "balanced";
  if (pct < 15) return "slightly positive";
  return "clearly positive";
}

/** The listening report: one sentence that answers "how is it going?", then the numbers behind it. */
export function Report({ subject, data, loading }: Props) {
  const { summary, net, themes, timeseries } = data;
  const days = timeseries.map((p) => parseDay(p.date)).filter((t) => !Number.isNaN(t));
  const first = days.length ? Math.min(...days) : NaN;
  const last = days.length ? Math.max(...days) : NaN;
  const span = days.length ? daysBetween(first, last) + 1 : 0;
  const range = days.length ? fmtDayRange(first, last) : "";
  const activeDays = Object.values(summary.by_day).filter((n) => n > 0).length;
  const top = [...themes].sort((a, b) => b.count - a.count)[0];
  const pct = Math.round(net * 100);
  const total = summary.total;
  const pctOf = (n: number) => (total > 0 ? `${Math.round((n / total) * 100)}% of mentions` : "—");
  const n = (k: string) => summary.by_sentiment[k] ?? 0;

  const stats = [
    { key: "total", label: "Mentions", value: fmtNum(total), note: range || "—" },
    { key: "net", label: "Net sentiment", value: total > 0 ? fmtNet(net) : "—", note: "positive minus negative" },
    { key: "positive", label: "Positive", value: fmtNum(n("positive")), note: pctOf(n("positive")) },
    { key: "neutral", label: "Neutral", value: fmtNum(n("neutral")), note: pctOf(n("neutral")) },
    { key: "negative", label: "Negative", value: fmtNum(n("negative")), note: pctOf(n("negative")) },
    { key: "days", label: "Active days", value: fmtNum(activeDays), note: activeDays === span && span > 0 ? "every day had a mention" : "days with a mention" },
  ];

  return (
    <section className="report" aria-busy={loading} aria-labelledby="report-h">
      <p className="eyebrow">
        <span className="eyebrow-mark" aria-hidden="true" />
        Listening report{range && ` · ${range}`}
      </p>
      <h1 id="report-h" className="headline">
        {total === 0 ? (
          <>{subject} has no mentions yet — run one from Scan.</>
        ) : (
          <>
            {subject} got <span className="hl">{fmtNum(total)} mention{total === 1 ? "" : "s"}</span>
            {span > 0 && ` in ${span} day${span === 1 ? "" : "s"}`}
            {top ? ` — mostly about ${top.label}, and` : ", and"} sentiment is {mood(pct)} (
            <span className={pct < 0 ? "neg" : pct > 0 ? "pos" : ""}>{fmtNet(net)}</span>).
          </>
        )}
      </h1>
      <dl className="stats">
        {stats.map((s) => (
          <div key={s.key} className="stat">
            <dt>
              {(s.key === "positive" || s.key === "neutral" || s.key === "negative") && <span className={`key key-${s.key}`} aria-hidden="true" />}
              {s.label}
            </dt>
            <dd className="stat-value">{s.value}</dd>
            <dd className="stat-note">{s.note}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
