import { verticalCompactor, type Layout } from "react-grid-layout";
import { expect, it } from "vitest";

it("preserves server-snapped heights and the next full-width band during vertical compaction", () => {
  const wall: Layout = [
    { i: "stat-a", x: 0, y: 0, w: 4, h: 3 }, { i: "stat-b", x: 4, y: 0, w: 4, h: 3 }, { i: "stat-c", x: 8, y: 0, w: 4, h: 3 },
    { i: "requests", x: 0, y: 3, w: 6, h: 6 }, { i: "latency", x: 6, y: 3, w: 6, h: 6 },
    { i: "map", x: 0, y: 9, w: 6, h: 10 }, { i: "health", x: 6, y: 9, w: 6, h: 10 },
    { i: "heatmap", x: 0, y: 19, w: 12, h: 6 }, { i: "traces", x: 0, y: 25, w: 12, h: 6 }, { i: "patterns", x: 0, y: 31, w: 12, h: 6 },
  ];
  for (const statsMinHeight of [3, 4]) {
    // grid.tsx reserves four presentation rows for stats without persisting it.
    const visible = wall.map(item => item.i.startsWith("stat") ? {...item, h: statsMinHeight} : {...item});
    const compacted = verticalCompactor.compact(visible, 12);
    const map = compacted.find(item => item.i === "map")!, health = compacted.find(item => item.i === "health")!;
    expect(map.h).toBe(10); expect(health.h).toBe(10); expect(health.y).toBe(map.y);
    expect(compacted.find(item => item.i === "heatmap")!.y).toBe(map.y + map.h);
    for (const item of compacted) expect(item.h).toBe(visible.find(original => original.i === item.i)!.h);
  }
});
