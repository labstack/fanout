import type { TextSpan } from "../api-error";

// FanQL spans and cursors count Unicode code points; the editor counts UTF-16 units.
function unitOffset(text: string, points: number): number {
  let units = 0;
  for (const char of text) {
    if (points-- <= 0) break;
    units += char.length;
  }
  return units;
}

export function pointOffset(text: string, units: number): number {
  return Array.from(text.slice(0, units)).length;
}

export function unitSpan(text: string, span: TextSpan): { from: number; to: number } {
  const from = unitOffset(text, span.start);
  return { from, to: Math.max(from, unitOffset(text, span.end)) };
}
