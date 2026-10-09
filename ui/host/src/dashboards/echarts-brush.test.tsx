import {visualizations} from "../../tests/visualizations";
import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setPlatformAPI, use, type EChartsType } from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import {  type Panel, type PanelResult } from "../../../panels/types";
import { PanelCard } from "./panel-card";

const native = vi.hoisted(() => ({ charts: [] as EChartsType[], options: [] as Record<string, unknown>[] }));
// Use the real engine and component registration. SVG avoids requiring a
// native canvas library in happy-dom; brush preprocessing/actions are unchanged.
vi.mock("echarts/core", async importOriginal => {
  const core = await importOriginal<typeof import("echarts/core")>();
  return { ...core, init: (element: HTMLElement) => {
    const chart = core.init(element, undefined, { renderer: "svg", width: 600, height: 240 });
    native.charts.push(chart);
    const setOption = chart.setOption.bind(chart);
    vi.spyOn(chart, "setOption").mockImplementation((option, ...args) => {
      native.options.push(option as Record<string, unknown>);
      return setOption({ ...option, animation: false }, ...args);
    });
    return chart;
  } };
});
use([SVGRenderer]);
setPlatformAPI({ measureText: text => ({ width: String(text ?? "").length * 7 }) });
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
  window.matchMedia = vi.fn().mockImplementation(query => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} }));
  native.charts.length = 0; native.options.length = 0;
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); document.body.innerHTML = ""; });
const col=(name:string,type:"time"|"number"|"string"|"json",role:"time"|"dimension"|"measure",unit?:string)=>({name,type,role,unit});
const fixtures:Record<string,PanelResult["frame"]>={
 heatmap:{columns:[col("time","time","time"),col("bucket_lower","number","dimension","ms"),col("bucket_upper","number","dimension","ms"),col("count","number","measure","count")],values:[[1000],[1],[2],[3]],rows:1},
 histogram:{columns:[col("bucket_lower","number","dimension","ms"),col("bucket_upper","number","dimension","ms"),col("count","number","measure","count")],values:[[1],[2],[3]],rows:1},
 scatter:{columns:[col("service","string","dimension"),col("count","number","measure","count"),col("p95","number","measure","ms")],values:[["checkout"],[2],[50]],rows:1},
 state_timeline:{columns:[col("time","time","time"),col("service","string","dimension"),col("p95","number","measure","ms")],values:[[1000],["checkout"],[50]],rows:1},
 logs:{columns:[col("time","time","time"),col("severity","string","dimension"),col("service","string","dimension"),col("body","string","dimension"),col("trace_id","string","dimension"),col("namespace","string","dimension")],values:[[1000],["ERROR"],["checkout"],["failed"],["abc"],["shop"]],rows:1},
 log_patterns:{columns:[col("body_template","string","dimension"),col("count","number","measure","count"),col("trend","json","dimension","count")],values:[["failed <*>"],[2],["[1,1]"]],rows:1},
 traces:{columns:[col("trace_id","string","dimension"),col("namespace","string","dimension"),col("service","string","dimension"),col("operation","string","dimension"),col("duration_ms","number","measure","ms"),col("status","string","dimension"),col("start","time","time")],values:[["abc"],["shop"],["checkout"],["cart"],[50],["STATUS_CODE_ERROR"],[1000]],rows:1},
 service_map:{columns:[col("kind","string","dimension"),col("service","string","dimension"),col("caller","string","dimension"),col("callee","string","dimension"),col("edge_type","string","dimension"),col("calls","number","measure","count"),col("average_ms","number","measure","ms"),col("error_rate","number","measure","percent"),col("health","string","dimension"),col("p95_ms","number","measure","ms"),col("spans","number","measure","count")],values:[["node","edge"],["checkout",""],["","checkout"],["","payment"],["","call"],[null,3],[null,20],[10,1],["unhealthy",""],[50,null],[2,null]],rows:2},
 health:{columns:[col("service","string","dimension"),col("health","string","dimension"),col("spans","number","measure","count"),col("error_rate","number","measure","percent"),col("p50_ms","number","measure","ms"),col("p95_ms","number","measure","ms"),col("log_count","number","measure","count"),col("metric_count","number","measure","count")],values:[["checkout"],["unhealthy"],[2],[10],[20],[50],[1],[1]],rows:1,health:{health:"unhealthy",counts:{healthy:0,degraded:0,unhealthy:1},service_count:1,total_spans:2,error_rate:10,error_trend:[1,10]}},
};

const timeFrame: PanelResult["frame"] = { columns: [col("time", "time", "time"), col("service", "string", "dimension"), col("count", "number", "measure")], values: [[1000, 2000], ["checkout", "checkout"], [2, 3]], rows: 2 };

it.each([false, true].flatMap(dark => [300, 600].map(width => ({ dark, width }))))("happy-dom renders and inspects every panel with no missing ECharts component warning (dark=$dark, width=$width)", async ({ dark, width }) => {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(DOMRect.fromRect({ width, height: 240 }));
  const errors = vi.spyOn(console, "error").mockImplementation(() => {});
  const warnings = vi.spyOn(console, "warn").mockImplementation(() => {});
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  const zoom = vi.fn(), noop = () => {};
  try {
    for (const viz of visualizations) {
      const panel: Panel = { id: viz, title: viz, viz, content: "Notes" };
      const result: PanelResult = { id: viz, status: "ok", elapsed_ms: 1, interval: "1m", from_ms: 0, to_ms: 10000, frame: fixtures[viz] ?? timeFrame };
      const props = { panel, title: viz, result, height: 300, group: "warning-test", loading: false, editing: false, agentAvailable: false, onView: noop, onCopyLink: noop, onExplain: noop, onZoom: zoom };
      await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><PanelCard key={viz} {...props} /></MantineProvider>));
      // Inspection must preserve the absence of a native toolbox on restoration.
      const card = container.querySelector<HTMLElement>("[data-panel]")!;
      const folded = width < 360 || viz === "text";
      const choose = async (mode: string) => {
        if (folded) {
          await act(async () => card.querySelector<HTMLButtonElement>(`[aria-label="${viz} menu"]`)!.click());
          await vi.waitFor(() => expect(document.querySelector('[role="menu"]')).not.toBeNull());
        }
        const scope = folded ? document : card;
        const button = Array.from(scope.querySelectorAll<HTMLButtonElement>("[data-panel-view]")).find(button => button.textContent === mode)!;
        expect(button).toBeDefined();
        await act(async () => button.click());
      };
      if (viz !== "text") {
        await choose("Data"); expect(card.querySelector("[data-panel-data]")).not.toBeNull();
        await choose("Chart");
      }
      await choose("Spec"); expect(card.querySelector("[data-panel-spec]")).not.toBeNull();
      await choose(viz === "text" ? "Content" : "Chart");
      expect(card.querySelector("[data-panel-spec]")).toBeNull();
      if (viz === "text") expect(card.textContent).toContain("Notes");
    }
    const messages = [...errors.mock.calls, ...warnings.mock.calls].map(args => args.join(" "));
    expect(messages.filter(message => message.includes("used but not imported"))).toEqual([]);
    expect(native.charts.length).toBeGreaterThan(0);
    for (const option of native.options) {
      const toolbox = option.toolbox as { show?: boolean } | undefined;
      expect(!toolbox || toolbox.show === false).toBe(true);
    }
  } finally { await act(async () => root.unmount()); }
});

it("real BrushComponent activates a global lineX cursor without a visible toolbox", async () => {
  const { EChartCanvas } = await import("./echart-canvas");
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  try {
    await act(async () => root.render(<EChartCanvas option={{ animation: false, xAxis: { type: "time" }, yAxis: {}, series: [{ type: "line", data: [[1000, 2], [2000, 3]] }] }} height={240} label="Brush" onZoom={vi.fn()} />));
    const chart = native.charts[0];
    const model = (chart as unknown as { getModel(): { getComponent(name: string): { brushOption: { brushType: string; brushMode: string } } } }).getModel();
    expect(model.getComponent("brush").brushOption).toMatchObject({ brushType: "lineX", brushMode: "single" });
    const toolbox = (chart.getOption() as { toolbox?: { show: boolean }[] }).toolbox;
    expect(toolbox?.every(item => item.show === false) ?? true).toBe(true);
  } finally { await act(async () => root.unmount()); }
});
