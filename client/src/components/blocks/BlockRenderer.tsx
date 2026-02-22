import type {
  Block,
  TextBlockData,
  MetricsBlockData,
  TableBlockData,
  TimeseriesBlockData,
  BarBlockData,
  HeatmapBlockData,
} from "@/lib/types";
import { TextBlock } from "./TextBlock";
import { MetricsBlock } from "./MetricsBlock";
import { TableBlock } from "./TableBlock";
import { TimeseriesBlock } from "./TimeseriesBlock";
import { BarBlock } from "./BarBlock";
import { HeatmapBlock } from "./HeatmapBlock";
import { GenericBlock } from "./GenericBlock";

export function BlockRenderer({ block }: { block: Block }) {
  switch (block.type) {
    case "text":
      return <TextBlock data={block.data as TextBlockData} />;
    case "metrics":
      return <MetricsBlock data={block.data as MetricsBlockData} />;
    case "table":
      return <TableBlock data={block.data as TableBlockData} />;
    case "timeseries":
      return <TimeseriesBlock data={block.data as TimeseriesBlockData} />;
    case "bar":
      return <BarBlock data={block.data as BarBlockData} />;
    case "heatmap":
      return <HeatmapBlock data={block.data as HeatmapBlockData} />;
    default:
      return <GenericBlock type={block.type} data={block.data} />;
  }
}
