import type { BadgeSource } from "./components/SourceBadge";
import type { SourceInfo } from "./types";

export type SourceLookup = (name: string) => BadgeSource;

export function makeSourceLookup(sources: SourceInfo[] | undefined): SourceLookup {
  const map = new Map((sources ?? []).map((s) => [s.name, s]));
  return (name) => {
    const s = map.get(name);
    if (s) return s;
    return { label: name, glyph: name.slice(0, 1).toUpperCase(), color: "#8a949c" };
  };
}
