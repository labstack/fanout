// Full-frame mean difference: tolerate cursor/caret flicker and small streamed
// text updates, but retain larger visual changes. Require 1.2 s of near-stasis.
export const freezeDetectFilter = "freezedetect=n=0.003:d=1.2";

/** Parse ffmpeg stderr; an open freeze at EOF extends to the probed duration. */
export function parseFreezeDetect(stderr, duration) {
  if (!Number.isFinite(duration) || duration <= 0) throw new Error("Video duration must be positive");
  const intervals = [];
  let start;
  for (const match of stderr.matchAll(/lavfi\.freezedetect\.freeze_(start|end):\s*([^\r\n]*)/g)) {
    const value = match[2].trim();
    const at = Number(value);
    if (!value || !Number.isFinite(at) || at < 0 || at > duration + 0.25) {
      throw new Error("Invalid freezedetect timestamp");
    }
    if (match[1] === "start") {
      if (start !== undefined || at < (intervals.at(-1)?.end ?? 0)) throw new Error("Unordered freezedetect start");
      start = Math.min(at, duration);
    } else {
      if (start === undefined || at <= start) throw new Error("Unpaired or unordered freezedetect end");
      const end = Math.min(at, duration);
      if (end > start) intervals.push({ start, end });
      start = undefined;
    }
  }
  if (start !== undefined && start < duration) intervals.push({ start, end: duration });
  return intervals;
}

function mergeIntervals(intervals) {
  const merged = [];
  for (const { start, end } of intervals.sort((a, b) => a.start - b.start)) {
    const previous = merged.at(-1);
    if (previous && start <= previous.end) previous.end = Math.max(previous.end, end);
    else merged.push({ start, end });
  }
  return merged;
}

/** Merge waits and freezes, then subtract holds/typing before compressing. */
export function clipSegments(waits, duration, from = 0, freezes = []) {
  if (!Number.isFinite(duration) || duration <= 0) throw new Error("Video duration must be positive");
  if (!Number.isFinite(from) || from < 0 || from >= duration) throw new Error("Clip start must be within the raw video");
  if (!Array.isArray(waits)) throw new Error("Timeline waits must be an array");
  if (!Array.isArray(freezes)) throw new Error("Frozen intervals must be an array");
  const intervals = [...waits, ...freezes.map(({ start, end }) => ({ kind: "freeze", start, end }))].map(({ kind, start, end }) => {
    if (!Number.isFinite(start) || !Number.isFinite(end) || start < 0 || end <= start) {
      throw new Error("Wait intervals must have finite seconds with 0 <= start < end");
    }
    if (end > duration + 0.25) throw new Error("Wait interval extends past the raw video");
    return { kind, start: Math.max(from, Math.min(start, duration)), end: Math.min(end, duration) };
  }).filter(({ start, end }) => end > start);
  // Version 1 recordings start before typing and first mark a wait after Send.
  // Protect that whole opening even if caret flicker/typing looks frozen. With
  // no wait boundary, conservatively keep it all; explicit typing overrides this.
  if (freezes.length && !waits.some(interval => interval.kind === "typing")) {
    const end = waits.filter(interval => interval.kind !== "hold")
      .reduce((first, interval) => Math.min(first, interval.start), duration);
    if (end > from) intervals.push({ kind: "typing", start: from, end: Math.min(end, duration) });
  }
  const protectedInterval = interval => interval.kind === "hold" || interval.kind === "typing";
  const holds = mergeIntervals(intervals.filter(protectedInterval));
  const waitsToCompress = [];
  for (const { start, end } of mergeIntervals(intervals.filter(interval => !protectedInterval(interval)))) {
    let cursor = start;
    for (const hold of holds) {
      if (hold.end <= cursor) continue;
      if (hold.start >= end) break;
      if (hold.start > cursor) waitsToCompress.push({ start: cursor, end: hold.start });
      cursor = Math.max(cursor, hold.end);
      if (cursor >= end) break;
    }
    if (cursor < end) waitsToCompress.push({ start: cursor, end });
  }
  const segments = [];
  let cursor = from;
  for (const { start, end } of waitsToCompress) {
    if (start > cursor) segments.push({ start: cursor, end: start, seconds: start - cursor });
    if (end > start) segments.push({ start, end, seconds: Math.min(0.6, end - start) });
    cursor = end;
  }
  if (cursor < duration) segments.push({ start: cursor, end: duration, seconds: duration - cursor });
  return segments;
}

export function clipFilter(segments) {
  const filters = segments.map(({ start, end, seconds }, index) =>
    `[0:v]trim=start=${start}:end=${end},setpts=(PTS-STARTPTS)*${seconds / (end - start)}[v${index}]`);
  filters.push(`${segments.map((_, index) => `[v${index}]`).join("")}concat=n=${segments.length}:v=1:a=0,scale=1280:800,fps=30,format=yuv420p[out]`);
  return filters.join(";\n");
}
