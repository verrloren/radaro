import { fmtDateTime, relTime } from "../format";
import type { Draft, Platform } from "../types";
import { SourceIcon, type BadgeSource } from "./SourceBadge";

const COLORS: Record<string, string> = { reddit: "#ff4500", bluesky: "#1185fe", mastodon: "#6364ff", devto: "#3b49df" };

/** The platform's badge; Dev.to has no bundled logo and shows its initial. */
export function platformBadge(name: string, platforms: Platform[]): BadgeSource {
  const label = platforms.find((p) => p.name === name)?.label ?? name;
  return { name, label, glyph: name === "devto" ? "D" : label.slice(0, 1).toUpperCase(), color: COLORS[name] ?? "#8e8e8e" };
}

export const STATUS_LABEL: Record<Draft["status"], string> = {
  draft: "to review",
  approved: "approved",
  publishing: "publishing",
  published: "published",
  failed: "failed",
  skipped: "skipped",
};

/** Where a draft goes: r/golang, a reply, Dev.to tags. */
export function draftWhere(d: Draft): string {
  const community = d.community?.trim();
  if (d.platform === "reddit" && community) {
    const sub = `r/${community.replace(/^\/?r\//, "")}`;
    return d.kind === "reply" ? `reply in ${sub}` : sub;
  }
  if (d.kind === "reply") return "reply";
  if (d.platform === "devto" && community) return community;
  return "post";
}

export function StatusBadge({ d }: Readonly<{ d: Draft }>) {
  return (
    <span className="draft-badges">
      <span className={`badge draft-status st-${d.status}`}>{STATUS_LABEL[d.status]}</span>
      {d.removed_at && (
        <span className="badge draft-status st-removed" title={`Removed ${fmtDateTime(d.removed_at)}`}>
          removed
        </span>
      )}
    </span>
  );
}

interface Props {
  drafts: Draft[];
  platforms: Platform[];
  selected: number | null;
  onSelect: (id: number) => void;
  stale: boolean;
}

export function DraftsList({ drafts, platforms, selected, onSelect, stale }: Readonly<Props>) {
  return (
    <ul className={`draft-list${stale ? " is-stale" : ""}`} aria-label="Drafts">
      {drafts.map((d) => {
        const badge = platformBadge(d.platform, platforms);
        const text = d.title?.trim() || d.body;
        const at = d.published_at ?? d.updated_at;
        return (
          <li key={d.id}>
            <button
              type="button"
              className={`draft-item${selected === d.id ? " is-on" : ""}`}
              aria-current={selected === d.id ? "true" : undefined}
              onClick={() => onSelect(d.id)}
            >
              <span className="draft-item-head">
                <SourceIcon source={badge} size={20} />
                <span className="strong small">{badge.label}</span>
                <span className="muted small draft-where">{draftWhere(d)}</span>
                <time className="muted small draft-when" dateTime={at} title={fmtDateTime(at)}>
                  {relTime(at)}
                </time>
              </span>
              <span className="draft-item-text">{text}</span>
              <StatusBadge d={d} />
            </button>
          </li>
        );
      })}
    </ul>
  );
}
