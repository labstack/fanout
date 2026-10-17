/** Merge concurrent tool/model waits so no source frame is compressed twice. Frames before from are cut. */
export function clipSegments(waits, duration, from = 0) {
  if (!Number.isFinite(duration) || duration <= 0) throw new Error("Video duration must be positive");
  if (!Number.isFinite(from) || from < 0 || from >= duration) throw new Error("Clip start must be within the raw video");
  if (!Array.isArray(waits)) throw new Error("Timeline waits must be an array");
  const intervals = waits.map(({ start, end }) => {
    if (!Number.isFinite(start) || !Number.isFinite(end) || start < 0 || end <= start) {
      throw new Error("Wait intervals must have finite seconds with 0 <= start < end");
    }
    if (end > duration + 0.25) throw new Error("Wait interval extends past the raw video");
    return { start: Math.max(from, Math.min(start, duration)), end: Math.min(end, duration) };
  }).filter(({ start, end }) => end > start).sort((a, b) => a.start - b.start);
  const merged = [];
  for (const interval of intervals) {
    const previous = merged.at(-1);
    if (previous && interval.start <= previous.end) previous.end = Math.max(previous.end, interval.end);
    else merged.push({ ...interval });
  }
  const segments = [];
  let cursor = from;
  for (const { start, end } of merged) {
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
