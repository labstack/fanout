import type { Panel, PanelResult, Selection } from "../../../../panels/types";
import { BarViz } from "./bar";
import { GaugeViz } from "./gauge";
import { StatViz } from "./stat";
import { TableViz } from "./table";
import { TextViz } from "./text";
import { TimeseriesViz } from "./timeseries";
import { HeatmapViz } from "./heatmap";
import { HistogramViz } from "./histogram";
import { ScatterViz } from "./scatter";
import { StateTimelineViz } from "./state-timeline";
import { ServiceMapViz } from "./service-map";
import { HealthViz } from "./health";

export function Viz(props: { panel: Panel; title?: string; result?: PanelResult; dark: boolean; height: number; group: string; onSelect?: (value: string) => void; onPoint?: (selection: Selection) => void; onZoom?: (from: number, to: number) => void }) {
  const { panel, title, result, dark, height, group, onSelect } = props;
  if (panel.viz === "text") return <TextViz panel={panel} />;
  if (!result?.frame) return null;
  const next = { ...props, result };
  switch (panel.viz) {
    case "stat": return <StatViz panel={panel} result={result} />;
    case "gauge": return <GaugeViz panel={panel} title={title} result={result} dark={dark} height={height} />;
    case "timeseries": return <TimeseriesViz panel={panel} title={title} result={result} dark={dark} height={height} group={group} onSelect={onSelect} />;
    case "bar": return <BarViz panel={panel} title={title} result={result} dark={dark} height={height} onSelect={onSelect} />;
    case "table": return <TableViz panel={panel} result={result} height={height} onSelect={onSelect} />;
    case "heatmap": return <HeatmapViz {...next} />;
    case "histogram": return <HistogramViz {...next} />;
    case "scatter": return <ScatterViz {...next} />;
    case "state_timeline": return <StateTimelineViz {...next} />;
    case "service_map": return <ServiceMapViz {...next} />;
    case "health": return <HealthViz {...next} />;
    case "logs": case "log_patterns": case "traces": return null; // Task 8 installs row renderers.
  }
}
