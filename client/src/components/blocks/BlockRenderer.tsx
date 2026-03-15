import type {
  Block,
  TextBlockData,
  MetricsBlockData,
  TableBlockData,
  TimeseriesBlockData,
  BarBlockData,
  HeatmapBlockData,
  TraceWaterfallData,
  TopologyData,
  FlameGraphData,
  SankeyData,
  DepMatrixData,
  EndpointsData,
  CorrelationData,
  TailData,
} from "@/lib/types";
import { TextBlock } from "./TextBlock";
import { MetricsBlock } from "./MetricsBlock";
import { TableBlock } from "./TableBlock";
import { TimeseriesBlock } from "./TimeseriesBlock";
import { BarBlock } from "./BarBlock";
import { HeatmapBlock } from "./HeatmapBlock";
import { TraceWaterfallBlock } from "./TraceWaterfallBlock";
import { TopologyBlock } from "./TopologyBlock";
import { FlameGraphBlock } from "./FlameGraphBlock";
import { SankeyBlock } from "./SankeyBlock";
import { DepMatrixBlock } from "./DepMatrixBlock";
import { EndpointsBlock } from "./EndpointsBlock";
import { CorrelationBlock } from "./CorrelationBlock";
import { TailBlock } from "./TailBlock";
import { GenericBlock } from "./GenericBlock";

export function BlockRenderer({ block, onAction }: { block: Block; onAction?: (prompt: string) => void }) {
  switch (block.type) {
    case "text":
      return <TextBlock data={block.data as TextBlockData} />;
    case "metrics":
      return <MetricsBlock data={block.data as MetricsBlockData} />;
    case "table":
      return <TableBlock data={block.data as TableBlockData} onAction={onAction} />;
    case "timeseries":
      return <TimeseriesBlock data={block.data as TimeseriesBlockData} />;
    case "bar":
      return <BarBlock data={block.data as BarBlockData} />;
    case "heatmap":
      return <HeatmapBlock data={block.data as HeatmapBlockData} />;
    case "trace_waterfall":
      return <TraceWaterfallBlock data={block.data as TraceWaterfallData} onAction={onAction} />;
    case "topology":
      return <TopologyBlock data={block.data as TopologyData} onAction={onAction} />;
    case "flame_graph":
      return <FlameGraphBlock data={block.data as FlameGraphData} />;
    case "sankey":
      return <SankeyBlock data={block.data as SankeyData} />;
    case "dep_matrix":
      return <DepMatrixBlock data={block.data as DepMatrixData} />;
    case "endpoints":
      return <EndpointsBlock data={block.data as EndpointsData} />;
    case "correlation":
      return <CorrelationBlock data={block.data as CorrelationData} />;
    case "tail":
      return <TailBlock data={block.data as TailData} onAction={onAction} />;
    default:
      return <GenericBlock type={block.type} data={block.data} />;
  }
}
