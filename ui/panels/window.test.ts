import { analysisOption } from "./analysis";
import { chartThemeFor, timeseriesOption } from "./compile";
import { withAnnotations } from "./annotations";
import type { Panel, PanelResult } from "./types";

it.each(["timeseries", "heatmap", "state_timeline"] as const)("bounds %s by its shifted observed window, including anomalies after the final sample", viz => {
  const panel: Panel = { id: "p", title: "Window", viz, time: { shift: "1h" } };
  const result: PanelResult = { id: "p", status: "ok", elapsed_ms: 1, from_ms: -3600000, to_ms: 0, interval: "1m", frame: {
    columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure" }],
    values: [[-3500000, -1800000], [1, 2]], rows: 2,
  } };
  const theme = chartThemeFor(false);
  const base = viz === "timeseries" ? timeseriesOption(panel, result, theme) : analysisOption(panel, result, theme);
  const option = withAnnotations(base, panel, result, { deploys: [], anomalies: [{ namespace: "shop", service: "cart", kind: "volume", title: "Stopped", severity: "warn", from: new Date(-900000).toISOString(), to: new Date(-600000).toISOString() }] }, {}, theme) as any;
  expect(base.xAxis).toMatchObject({ type: "time", min: -3600000, max: 0 });
  expect(option.xAxis).toMatchObject({ min: result.from_ms, max: result.to_ms });
  const area = option.series[0].markArea.data[0];
  expect(area[0].xAxis).toBeGreaterThan(-1800000);
  expect(area[0].xAxis).toBeGreaterThanOrEqual(option.xAxis.min);
  expect(area[1].xAxis).toBeLessThanOrEqual(option.xAxis.max);
  expect(result.frame!.rows).toBe(2);
  expect(option.series[0].data).toHaveLength(base.series instanceof Array ? (base.series[0] as any).data.length : 0);
});
