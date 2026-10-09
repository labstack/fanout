import { expect, it } from "vitest";
import { chartThemeFor, timeseriesOption } from "../../../panels/compile";
import type { Frame } from "../../../panels/types";
it("single series has neither legend nor direct labels; grouped labels name the service", () => {
  const panel = { id: "p", title: "Latency", viz: "timeseries" as const, unit: "ms" as const };
  const single: Frame = { columns: [{ name: "time", type: "time", role: "time" }, { name: "p95", type: "number", role: "measure", unit: "ms" }], values: [[0], [42]], rows: 1 };
  const one = timeseriesOption(panel, { id: "p", status: "ok", frame: single, elapsed_ms: 1 }, chartThemeFor(false));
  expect((one.series as { endLabel?: { show: boolean } }[])[0].endLabel?.show).not.toBe(true);
  expect((one.legend as { show: boolean }).show).toBe(false);
  const grouped: Frame = { columns: [single.columns[0], { name: "service", type: "string", role: "dimension" }, single.columns[1]], values: [[0, 0], ["checkout", "payment"], [42, 30]], rows: 2 };
  const many = timeseriesOption(panel, { id: "p", status: "ok", frame: grouped, elapsed_ms: 1 }, chartThemeFor(false));
  expect((many.series as { name: string; endLabel?: { formatter: string } }[]).map(s => s.name)).toEqual(["checkout", "payment"]);
  expect((many.series as { endLabel?: { show: boolean } }[]).every(s => s.endLabel?.show)).toBe(true);
});
it("gives ungrouped measures reader-facing series names too", () => {
  const option = timeseriesOption({ id: "p", title: "Performance", viz: "timeseries" }, { id: "p", status: "ok", elapsed_ms: 0, frame: { rows: 1, columns: [{ name: "time", role: "time", type: "time" }, { name: "p95", role: "measure", type: "number", unit: "ms" }, { name: "error_rate", role: "measure", type: "number", unit: "percent" }], values: [[0], [42], [5]] } }, chartThemeFor(false));
  expect((option.series as { name: string }[]).map(series => series.name)).toEqual(["p95 latency", "Error rate"]);
});
