import { verticalCompactor, type Layout } from "react-grid-layout";
import { expect, it } from "vitest";

it("preserves server-snapped heights and the next full-width band during vertical compaction", () => {
  const wall: Layout = [
    { i: "stat-a", x: 0, y: 0, w: 4, h: 4 }, { i: "stat-b", x: 4, y: 0, w: 4, h: 4 }, { i: "stat-c", x: 8, y: 0, w: 4, h: 4 },
    { i: "requests", x: 0, y: 4, w: 6, h: 6 }, { i: "latency", x: 6, y: 4, w: 6, h: 6 },
    { i: "map", x: 0, y: 10, w: 6, h: 10 }, { i: "health", x: 6, y: 10, w: 6, h: 10 },
    { i: "heatmap", x: 0, y: 20, w: 12, h: 6 }, { i: "traces", x: 0, y: 26, w: 12, h: 6 }, { i: "patterns", x: 0, y: 32, w: 12, h: 6 },
  ];
  const compacted = verticalCompactor.compact(wall.map(item => ({...item})), 12);
  for (const item of compacted) expect(item).toMatchObject(wall.find(original => original.i === item.i)!);
});
