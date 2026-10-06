import type { Panel, PanelResult } from "../../../../panels/types";
import { BarViz } from "./bar";
import { GaugeViz } from "./gauge";
import { StatViz } from "./stat";
import { TableViz } from "./table";
import { TextViz } from "./text";
import { TimeseriesViz } from "./timeseries";
import { HealthViz } from "./health";
import { ServiceMapViz } from "./service-map";

export function Viz({ panel, title, result, dark, height, group, onSelect }: { panel: Panel; title?: string; result?: PanelResult; dark: boolean; height: number; group: string; onSelect?: (value: string) => void }) {
  if (panel.viz === "text") return <TextViz panel={panel} />;
  if (!result?.frame) return null;
  switch (panel.viz) {
    case "health": return <HealthViz result={result} dark={dark} />;
    case "service_map": return <ServiceMapViz panel={panel} title={title} result={result} dark={dark} height={height} onSelect={onSelect} />;
    case "stat": return <StatViz panel={panel} result={result} />;
    case "gauge": return <GaugeViz panel={panel} title={title} result={result} dark={dark} height={height} />;
    case "timeseries": return <TimeseriesViz panel={panel} title={title} result={result} dark={dark} height={height} group={group} onSelect={onSelect} />;
    case "bar": return <BarViz panel={panel} title={title} result={result} dark={dark} height={height} onSelect={onSelect} />;
    case "table": return <TableViz panel={panel} result={result} height={height} onSelect={onSelect} />;
  }
}
