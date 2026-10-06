import { beforeEach, describe, expect, it, vi } from "vitest";
const mock = vi.hoisted(() => ({ panels: vi.fn(), annotations: vi.fn() }));
vi.mock("./api", () => ({ queryPanels: mock.panels, queryAnnotations: mock.annotations }));
import { refreshDashboard } from "./refresh";
import type { QueryBody } from "./api";
const body: QueryBody = { dashboard: { version: 1, name: "D", time: { range: "1h" }, panels: [{ id: "a", title: "A", viz: "timeseries", query: { from: "spans", measures: ["count()"] } }, { id: "b", title: "B", viz: "timeseries", time: { shift: "1d" }, query: { from: "spans", measures: ["count()"] } }] }, time: { range: "1h" }, vars: { service: "checkout" } };
beforeEach(() => vi.resetAllMocks());
describe("S8 annotations", () => {
  it("skips annotations for a batch with only stat, table and bar panels", async () => {
    const panels = (["stat", "table", "bar"] as const).map(viz => ({ ...body.dashboard.panels[0], id: viz, viz }));
    const results = panels.map(p => ({ id: p.id, status: "ok", elapsed_ms: 1, from_ms: 0, to_ms: 10000 }));
    mock.panels.mockResolvedValue(results);
    await expect(refreshDashboard({ ...body, dashboard: { ...body.dashboard, panels } })).resolves.toEqual({ results });
    expect(mock.annotations).not.toHaveBeenCalled();
  });
  it.each(["timeseries", "heatmap", "state_timeline"] as const)("uses only returned time-panel windows for %s", async viz => {
    const panels = [{ ...body.dashboard.panels[0], viz }, { ...body.dashboard.panels[1], viz: "stat" as const }];
    mock.panels.mockResolvedValue([
      { id: "a", status: "ok", elapsed_ms: 1, from_ms: 1000, to_ms: 2000 },
      { id: "b", status: "ok", elapsed_ms: 1, from_ms: 0, to_ms: 10000 },
      { id: "unknown", status: "ok", elapsed_ms: 1, from_ms: -10000, to_ms: 20000 },
    ]);
    mock.annotations.mockResolvedValue({ deploys: [], anomalies: [] });
    await refreshDashboard({ ...body, dashboard: { ...body.dashboard, panels } });
    expect(mock.annotations).toHaveBeenCalledExactlyOnceWith({ from: new Date(1000).toISOString(), to: new Date(2000).toISOString() }, undefined);
  });
  it("makes one panel batch plus one annotation request for a refresh", async () => {
    mock.panels.mockResolvedValue([{ id: "a", status: "ok", elapsed_ms: 1, from_ms: 100000, to_ms: 200000 }, { id: "b", status: "ok", elapsed_ms: 1, from_ms: 0, to_ms: 100000 }]); mock.annotations.mockResolvedValue({ deploys: [], anomalies: [] });
    await refreshDashboard(body); expect(mock.panels).toHaveBeenCalledTimes(1); expect(mock.annotations).toHaveBeenCalledTimes(1);
    expect(mock.annotations.mock.calls[0][0].from).toBe(new Date(0).toISOString()); expect(mock.annotations.mock.calls[0][0].to).toBe(new Date(200000).toISOString());
    expect(mock.annotations.mock.calls[0][0]).not.toHaveProperty("services");
    expect(mock.annotations.mock.calls[0][0]).not.toHaveProperty("namespace");
  });
  it("keeps successful panels if annotations fail", async () => {
    mock.panels.mockResolvedValue([{ id: "a", status: "ok", elapsed_ms: 1, from_ms: 1, to_ms: 2 }]); mock.annotations.mockRejectedValue(new Error("Unavailable"));
    const got = await refreshDashboard(body); expect(got.results[0].status).toBe("ok"); expect(got.annotation_error).toBe("Unavailable");
  });
  it("skips annotations without valid windows or with both flags disabled", async () => {
    for (const results of [[], [{ id: "a", status: "empty", elapsed_ms: 1 }], [{ id: "a", status: "ok", elapsed_ms: 1, from_ms: 2, to_ms: 1 }]]) {
      mock.panels.mockResolvedValue(results);
      await expect(refreshDashboard(body)).resolves.toEqual({ results });
    }
    const results = [{ id: "a", status: "ok", elapsed_ms: 1, from_ms: 1, to_ms: 2 }]; mock.panels.mockResolvedValue(results);
    await expect(refreshDashboard({ ...body, dashboard: { ...body.dashboard, annotations: { deploys: false, anomalies: false } } })).resolves.toEqual({ results });
    expect(mock.annotations).not.toHaveBeenCalled();
  });
  it("still requests history when only one annotation kind is disabled", async () => {
    mock.panels.mockResolvedValue([{ id: "a", status: "ok", elapsed_ms: 1, from_ms: 1, to_ms: 2 }]);
    const annotations = { deploys: [], anomalies: [], truncated: true }; mock.annotations.mockResolvedValue(annotations);
    for (const flags of [{ deploys: false }, { anomalies: false }]) expect((await refreshDashboard({ ...body, dashboard: { ...body.dashboard, annotations: flags } })).annotations).toBe(annotations);
    expect(mock.annotations).toHaveBeenCalledTimes(2);
  });
  it("passes cancellation through both requests and propagates aborts", async () => {
    const controller = new AbortController(); const error = new DOMException("Aborted", "AbortError");
    mock.panels.mockResolvedValue([{ id: "a", status: "ok", elapsed_ms: 1, from_ms: 1, to_ms: 2 }]);
    mock.annotations.mockImplementation(() => { controller.abort(); throw error; });
    await expect(refreshDashboard(body, controller.signal)).rejects.toBe(error);
    expect(mock.panels.mock.calls[0][1]).toBe(controller.signal); expect(mock.annotations.mock.calls[0][1]).toBe(controller.signal);
  });
  it("does not request annotations when the panel batch fails", async () => {
    const error = new Error("Panels unavailable"); mock.panels.mockRejectedValue(error);
    await expect(refreshDashboard(body)).rejects.toBe(error); expect(mock.annotations).not.toHaveBeenCalled();
  });
});
