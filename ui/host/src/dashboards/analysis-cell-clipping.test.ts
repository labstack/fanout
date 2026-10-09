import { init } from "echarts";
import { expect, it } from "vitest";
import { analysisOption } from "../../../panels/analysis";
import { chartThemeFor } from "../../../panels/compile";

it.each(["heatmap", "state_timeline"] as const)("clips %s cells to an unaligned window in the rendered chart", viz => {
  const chart = init(null, undefined, { renderer: "svg", ssr: true, width: 560, height: 248 });
  try {
    chart.setOption(analysisOption({ id: "p", title: "Cells", viz, thresholds: [{ value: 1.5, status: "warn" }] }, { id: "p", status: "ok", elapsed_ms: 0, from_ms: 37000, to_ms: 97000, interval: "1m", frame: {
      columns: [{ name: "time", type: "time", role: "time" }, { name: "service", type: "string", role: "dimension" }, { name: "count", type: "number", role: "measure" }],
      values: [[0, 60000], ["checkout", "checkout"], [1, 2]], rows: 2,
    } }, chartThemeFor(false), { width: 560, height: 248 }));
    chart.renderToSVGString();
    const grid = (chart as any).getModel().getComponent("grid").coordinateSystem.getRect();
    const rects: any[] = [];
    const model = (chart as any).getModel().getSeriesByIndex(0);
    (chart as any).getViewOfSeriesModel(model).group.traverse((el: any) => { if (el.type === "rect") rects.push(el); });
    expect(rects).toHaveLength(2);
    expect(rects.some((el: any) => el.shape.x < grid.x)).toBe(true);
    expect(rects.some((el: any) => el.shape.x + el.shape.width > grid.x + grid.width)).toBe(true);
    for (const cell of rects) {
      expect(cell.__clipPaths?.length ?? 0).toBeGreaterThan(0);
      let box = cell.getBoundingRect().clone();
      for (const clip of cell.__clipPaths) {
        const bounds = clip.getBoundingRect();
        const right = Math.min(box.x + box.width, bounds.x + bounds.width), bottom = Math.min(box.y + box.height, bounds.y + bounds.height);
        box.x = Math.max(box.x, bounds.x); box.y = Math.max(box.y, bounds.y);
        box.width = Math.max(0, right - box.x); box.height = Math.max(0, bottom - box.y);
      }
      expect(box.x).toBeGreaterThanOrEqual(grid.x);
      expect(box.x + box.width).toBeLessThanOrEqual(grid.x + grid.width);
      expect(box.y).toBeGreaterThanOrEqual(grid.y);
      expect(box.y + box.height).toBeLessThanOrEqual(grid.y + grid.height);
    }
  } finally { chart.dispose(); }
});
