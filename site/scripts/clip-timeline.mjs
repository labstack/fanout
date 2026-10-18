function mergeIntervals(intervals) {
  const merged = [];
  for (const { start, end } of intervals.sort((a, b) => a.start - b.start)) {
    const previous = merged.at(-1);
    if (previous && start <= previous.end) previous.end = Math.max(previous.end, end);
    else merged.push({ start, end });
  }
  return merged;
}

/** Merge concurrent waits, preserving holds at real speed even inside a wait. Frames before from are cut. */
export function clipSegments(waits, duration, from = 0) {
  if (!Number.isFinite(duration) || duration <= 0) throw new Error("Video duration must be positive");
  if (!Number.isFinite(from) || from < 0 || from >= duration) throw new Error("Clip start must be within the raw video");
  if (!Array.isArray(waits)) throw new Error("Timeline waits must be an array");
  const intervals = waits.map(({ kind, start, end }) => {
    if (!Number.isFinite(start) || !Number.isFinite(end) || start < 0 || end <= start) {
      throw new Error("Wait intervals must have finite seconds with 0 <= start < end");
    }
    if (end > duration + 0.25) throw new Error("Wait interval extends past the raw video");
    return { kind, start: Math.max(from, Math.min(start, duration)), end: Math.min(end, duration) };
  }).filter(({ start, end }) => end > start);
  const holds = mergeIntervals(intervals.filter(interval => interval.kind === "hold"));
  const waitsToCompress = [];
  for (const { start, end } of mergeIntervals(intervals.filter(interval => interval.kind !== "hold"))) {
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
