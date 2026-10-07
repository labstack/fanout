import { useComputedColorScheme } from "@mantine/core";
import type { ReactNode } from "react";
import { brand, chart, fonts } from "../../../../tokens";

export function RowText({ text, template = false, highlight = "" }: { text: string; template?: boolean; highlight?: string }) {
  const dark = useComputedColorScheme("light") === "dark";
  const accent = brand[dark ? 5 : 7];
  const rgb = [1,3,5].map(i => parseInt(accent.slice(i,i+2),16)).join(", ");
  const matches: number[] = [];
  if (highlight) {
    const lower = text.toLowerCase(), needle = highlight.toLowerCase();
    for (let at = lower.indexOf(needle); at !== -1; at = lower.indexOf(needle,at+needle.length)) matches.push(at);
  }
  const decorate = (start: number, end: number): ReactNode[] => {
    const parts: ReactNode[] = [];
    let cursor = start;
    for (const at of matches) {
      const lo = Math.max(start,at), hi = Math.min(end,at+highlight.length);
      if (lo >= hi) continue;
      parts.push(text.slice(cursor,lo), <mark key={lo} data-highlight-match style={{background:`rgba(${rgb}, ${dark ? .22 : .30})`,color:"inherit",borderRadius:3,padding:"0 2px"}}>{text.slice(lo,hi)}</mark>);
      cursor = hi;
    }
    parts.push(text.slice(cursor,end)); return parts;
  };
  const parts: ReactNode[] = [];
  let cursor = 0;
  if (template) for (const match of text.matchAll(/<\*>|<str>|<num>|<ip>|<time>/g)) {
    parts.push(...decorate(cursor,match.index));
    parts.push(<span key={`chip-${match.index}`} data-template-chip style={{background:`rgba(${rgb}, .15)`,color:brand[dark ? 3 : 8],borderRadius:3,padding:"0 3px",fontSize:12}}>{decorate(match.index,match.index+match[0].length)}</span>);
    cursor = match.index+match[0].length;
  }
  parts.push(...decorate(cursor,text.length));
  return <span title={text} style={{fontFamily:template ? fonts.display : undefined,color:chart[dark ? "dark" : "light"].text,fontSize:12}}>{parts}</span>;
}
