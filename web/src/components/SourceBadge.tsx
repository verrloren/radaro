import type { CSSProperties } from "react";

export interface BadgeSource {
  label: string;
  glyph: string;
  color: string;
}

export function SourceBadge({
  source,
  showLabel = false,
  count,
}: {
  source: BadgeSource;
  showLabel?: boolean;
  count?: number;
}) {
  const style = { "--src": source.color } as CSSProperties;
  return (
    <span className="src-badge" style={style} title={source.label}>
      <span className="src-glyph" aria-hidden="true">
        {source.glyph}
      </span>
      {showLabel ? <span className="src-label">{source.label}</span> : <span className="sr-only">{source.label}</span>}
      {count !== undefined && <span className="src-count">{count.toLocaleString("en-US")}</span>}
    </span>
  );
}
