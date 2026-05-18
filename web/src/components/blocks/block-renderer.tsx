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
  LogsBlockData,
  ComparisonData,
} from "@/lib/types";
import { TextBlock } from "./text-block";
import { MetricsBlock } from "./metrics-block";
import { TableBlock } from "./table-block";
import { TimeseriesBlock } from "./timeseries-block";
import { BarBlock } from "./bar-block";
import { HeatmapBlock } from "./heatmap-block";
import { TraceWaterfallBlock } from "./trace-waterfall-block";
import { TopologyBlock } from "./topology-block";
import { FlameGraphBlock } from "./flame-graph-block";
import { SankeyBlock } from "./sankey-block";
import { DepMatrixBlock } from "./dep-matrix-block";
import { EndpointsBlock } from "./endpoints-block";
import { CorrelationBlock } from "./correlation-block";
import { LogsBlock } from "./logs-block";
import { ComparisonBlock } from "./comparison-block";
import { GenericBlock } from "./generic-block";
import { AIActionBar } from "@/components/ai/ai-action-bar";
import { getBlockActions } from "@/lib/block-actions";

export function BlockRenderer({ block, onAction }: { block: Block; onAction?: (prompt: string) => void }) {
  const content = (() => {
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
      case "logs":
        return <LogsBlock data={block.data as LogsBlockData} onAction={onAction} />;
      case "comparison":
        return <ComparisonBlock data={block.data as ComparisonData} />;
      default:
        return <GenericBlock type={block.type} data={block.data} />;
    }
  })();

  const actions = onAction ? getBlockActions(block) : [];

  return (
    <div className="space-y-2">
      {content}
      {onAction && actions.length > 0 ? <AIActionBar actions={actions} onAction={onAction} /> : null}
    </div>
  );
}
