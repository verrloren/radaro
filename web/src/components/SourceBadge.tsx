import type { CSSProperties } from "react";
import bluesky from "../icons/bluesky.svg";
import hackernews from "../icons/hackernews.svg";
import mastodon from "../icons/mastodon.svg";
import reddit from "../icons/reddit.svg";
import rss from "../icons/rss.svg";
import stackoverflow from "../icons/stackoverflow.svg";
import x from "../icons/x.svg";
import youtube from "../icons/youtube.svg";

export interface BadgeSource {
  name?: string;
  label: string;
  glyph: string;
  color: string;
}

const ICONS: Record<string, string> = { bluesky, hackernews, mastodon, reddit, rss, stackoverflow, x, youtube };

/** The platform's logo tile; sources without a bundled logo fall back to their glyph on their color. */
export function SourceIcon({ source, size = 16 }: { source: BadgeSource; size?: number }) {
  const src = source.name ? ICONS[source.name] : undefined;
  if (src) return <img className="src-icon" src={src} alt="" width={size} height={size} />;
  const style = { "--src": source.color, width: size, height: size, fontSize: Math.round(size * 0.5) } as CSSProperties;
  return (
    <span className="src-icon src-glyph" style={style} aria-hidden="true">
      {source.glyph}
    </span>
  );
}

export function SourceBadge({
  source,
  showLabel = false,
  count,
  size,
}: {
  source: BadgeSource;
  showLabel?: boolean;
  count?: number;
  size?: number;
}) {
  return (
    <span className="src-badge" title={source.label}>
      <SourceIcon source={source} size={size} />
      {showLabel ? <span className="src-label">{source.label}</span> : <span className="sr-only">{source.label}</span>}
      {count !== undefined && <span className="src-count">{count.toLocaleString("en-US")}</span>}
    </span>
  );
}
