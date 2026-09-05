import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ init: vi.fn(), setOption: vi.fn(), dispose: vi.fn(), on: vi.fn(), resize: vi.fn() }));

vi.mock("echarts/core", () => ({
  init: mocks.init,
  use: () => undefined,
}));
vi.mock("echarts/components", () => ({ AriaComponent: {}, GridComponent: {}, LegendComponent: {}, TooltipComponent: {}, VisualMapComponent: {} }));
vi.mock("echarts/renderers", () => ({ SVGRenderer: {} }));

import { EChart } from "./echart";

describe("EChart", () => {
  afterEach(() => { document.body.innerHTML = ""; vi.clearAllMocks(); });

  it("keeps one chart instance across option changes and applies options in place", async () => {
    mocks.init.mockReturnValue({ setOption: mocks.setOption, dispose: mocks.dispose, on: mocks.on, resize: mocks.resize });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<EChart option={{ series: [{ type: "line", data: [1, 2] }] }} label="Trend" />));
    await act(async () => root.render(<EChart option={{ series: [{ type: "line", data: [1, 2, 3] }] }} label="Trend" />));
    expect(mocks.init).toHaveBeenCalledTimes(1);
    expect(mocks.setOption).toHaveBeenCalledTimes(2);
    expect(mocks.setOption.mock.calls[1][1]).toEqual({ notMerge: true });
    expect(mocks.dispose).not.toHaveBeenCalled();
    await act(async () => root.unmount());
    expect(mocks.dispose).toHaveBeenCalledTimes(1);
  });

  it("routes clicks to the latest handler without re-registering", async () => {
    mocks.init.mockReturnValue({ setOption: mocks.setOption, dispose: mocks.dispose, on: mocks.on, resize: mocks.resize });
    const first = vi.fn();
    const second = vi.fn();
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<EChart option={{}} label="Map" onClick={first} />));
    await act(async () => root.render(<EChart option={{}} label="Map" onClick={second} />));
    expect(mocks.on).toHaveBeenCalledTimes(1);
    const handler = mocks.on.mock.calls[0][1] as (params: unknown) => void;
    handler({ dataType: "node" });
    expect(second).toHaveBeenCalledWith({ dataType: "node" });
    expect(first).not.toHaveBeenCalled();
    await act(async () => root.unmount());
  });
});
