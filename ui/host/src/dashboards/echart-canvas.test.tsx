import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ init: vi.fn(), connect: vi.fn(), disconnect: vi.fn(), instance: null as null | Record<string, unknown> }));

vi.mock("echarts/core", () => ({ init: mocks.init, use: () => undefined, connect: mocks.connect, disconnect: mocks.disconnect }));
vi.mock("echarts/charts", () => ({ BarChart: {}, GaugeChart: {}, GraphChart: {}, LineChart: {}, CustomChart: {}, HeatmapChart: {}, ScatterChart: {} }));
vi.mock("echarts/components", () => ({ AriaComponent: {}, GridComponent: {}, LegendComponent: {}, MarkLineComponent: {}, TooltipComponent: {}, VisualMapComponent: {} }));
vi.mock("echarts/renderers", () => ({ CanvasRenderer: {} }));

import { EChartCanvas } from "./echart-canvas";

function fresh() {
  const instance = { setOption: vi.fn(), dispose: vi.fn(), on: vi.fn(), resize: vi.fn(), group: "" };
  mocks.init.mockReturnValueOnce(instance);
  return instance;
}

describe("EChartCanvas", () => {
  afterEach(() => { document.body.innerHTML = ""; vi.clearAllMocks(); });

  it("applies the option with the label as the aria description and disposes on unmount", async () => {
    const instance = fresh();
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const option = { series: [], aria: { label: { enabled: true } } };
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="Latency" />));
    const [applied, opts] = instance.setOption.mock.calls[0];
    expect(applied.series).toEqual([]);
    expect(applied.aria).toEqual({ label: { enabled: true }, enabled: true, description: "Latency" });
    expect(opts).toEqual({ notMerge: true });
    expect(instance.dispose).not.toHaveBeenCalled();
    await act(async () => root.unmount());
    expect(instance.dispose).toHaveBeenCalledTimes(1);
  });

  it("forwards datum selections to the latest click handler without replacing the chart", async () => {
    const instance = fresh();
    const container = document.createElement("div"); document.body.append(container);
    const root = createRoot(container);
    const option = { series: [] };
    const first = vi.fn(); const next = vi.fn();
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="Points" onClick={first} />));
    const click = instance.on.mock.calls.find(([name]) => name === "click")![1];
    const event = { data: { selection: { dimensions: { service: "cart" } } }, dataType: "node" };
    click(event);
    expect(first).toHaveBeenCalledWith(event);
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="Points" onClick={next} />));
    click(event);
    expect(next).toHaveBeenCalledWith(event);
    expect(first).toHaveBeenCalledOnce();
    expect(instance.setOption).toHaveBeenCalledOnce();
    await act(async () => root.unmount());
  });

  it("disconnects a group only when its last chart leaves", async () => {
    const a = fresh();
    const b = fresh();
    const ca = document.createElement("div");
    const cb = document.createElement("div");
    document.body.append(ca, cb);
    const ra = createRoot(ca);
    const rb = createRoot(cb);
    await act(async () => ra.render(<EChartCanvas option={{}} height={1} label="a" group="g" />));
    await act(async () => rb.render(<EChartCanvas option={{}} height={1} label="b" group="g" />));
    expect(a.group).toBe("g");
    expect(b.group).toBe("g");
    await act(async () => ra.unmount());
    expect(a.group).toBe("");
    expect(mocks.disconnect).not.toHaveBeenCalled();
    await act(async () => rb.unmount());
    expect(mocks.disconnect).toHaveBeenCalledWith("g");
  });
});
